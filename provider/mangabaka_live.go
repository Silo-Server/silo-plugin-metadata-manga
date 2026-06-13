package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("mangabaka: rate limited (429)")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("mangabaka: unexpected status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("mangabaka: decode: %w", err)
	}
	return nil
}
