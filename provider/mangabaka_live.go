package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const mangaBakaEndpoint = "https://api.mangabaka.org"

// MangaBaka publishes per-IP leaky-bucket limits: 30/min for search, 120/min
// for lookup. Pace just under each (search ≈ 1 per 2.1s, lookup ≈ 1 per 0.55s)
// with burst 1. Cached edge responses do not count against the bucket, so this
// is a conservative floor, not the real throughput.
var (
	mangaBakaSearchLimiter = rate.NewLimiter(rate.Every(2100*time.Millisecond), 1)
	mangaBakaLookupLimiter = rate.NewLimiter(rate.Every(550*time.Millisecond), 1)
)

type liveBackend struct {
	client   *http.Client
	endpoint string
}

func newLiveBackend() *liveBackend {
	return newLiveBackendWithEndpoint(mangaBakaEndpoint)
}

func newLiveBackendWithEndpoint(endpoint string) *liveBackend {
	return &liveBackend{
		client:   &http.Client{Timeout: 15 * time.Second},
		endpoint: strings.TrimRight(endpoint, "/"),
	}
}

func (b *liveBackend) ready() bool { return true }

func (b *liveBackend) search(ctx context.Context, term string) ([]mangaBakaSeries, error) {
	if err := mangaBakaSearchLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/v1/series/search?q=%s", b.endpoint, url.QueryEscape(term))
	var resp struct {
		Data []mangaBakaSeries `json:"data"`
	}
	if err := b.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (b *liveBackend) fetch(ctx context.Context, id string) (*mangaBakaSeries, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if err := mangaBakaLookupLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/v1/series/%s", b.endpoint, url.PathEscape(id))
	var resp struct {
		Data *mangaBakaSeries `json:"data"`
	}
	if err := b.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (b *liveBackend) getJSON(ctx context.Context, endpoint string, out any) error {
	const maxAttempts = 2
	for attempt := 1; ; attempt++ {
		retryAfter, err := b.getJSONOnce(ctx, endpoint, out)
		if err == nil {
			return nil
		}
		if retryAfter < 0 || attempt >= maxAttempts {
			return err // not a 429, or out of attempts
		}
		// 429: wait Retry-After (capped) then retry once.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryAfter):
		}
	}
}

// getJSONOnce performs one request. On HTTP 429 it returns (waitDuration, err)
// with waitDuration >= 0; on any other error it returns (-1, err); on success
// (0, nil). The 429 wait is parsed from Retry-After (seconds), defaulting to 2s
// and capped at 10s.
func (b *liveBackend) getJSONOnce(ctx context.Context, endpoint string, out any) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		return -1, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return parseRetryAfter(res.Header.Get("Retry-After")), fmt.Errorf("mangabaka: rate limited (429)")
	}
	if res.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("mangabaka: unexpected status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return -1, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return -1, fmt.Errorf("mangabaka: decode: %w", err)
	}
	return 0, nil
}

// parseRetryAfter reads a Retry-After seconds value, defaulting to 2s and
// capping at 10s.
func parseRetryAfter(h string) time.Duration {
	const def, max = 2 * time.Second, 10 * time.Second
	if h = strings.TrimSpace(h); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 0 {
			d := time.Duration(n) * time.Second
			if d > max {
				return max
			}
			return d
		}
	}
	return def
}
