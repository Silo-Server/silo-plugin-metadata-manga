package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// aniListLimiter is a package-level shared rate limiter for all AniList
// outbound requests. AniList has run in a degraded mode with a hard
// 30 requests/min ceiling for years; pacing at 1 request/second (observed
// live, 2026-06-11) exhausts every window after ~30 requests and turns the
// rest of a sweep into a solid 429 wall, with the single retry doubling the
// pressure. One request per 2.1s (≈28/min) stays just under the ceiling.
// Burst of 1 means no bursting beyond the steady rate.
var aniListLimiter = rate.NewLimiter(rate.Every(2100*time.Millisecond), 1)

const aniListEndpoint = "https://graphql.anilist.co"

type aniListMedia struct {
	ID          int    `json:"id"`
	BannerImage string `json:"bannerImage"`
}

const aniListByIDQuery = `query ($id: Int) {
  Media(id: $id, type: MANGA) {
    id
    bannerImage
  }
}`

func parseAniListByID(body []byte) (*aniListMedia, error) {
	var resp struct {
		Data struct {
			Media *aniListMedia `json:"Media"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("anilist: decode: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("anilist: %s", resp.Errors[0].Message)
	}
	return resp.Data.Media, nil
}

// doAniListQuery POSTs a GraphQL query+variables to the AniList endpoint and
// returns the (size-limited) response body, classifying transient failures.
// It proactively rate-limits via aniListLimiter (≈28 req/min) and retries
// once on HTTP 429, honoring the Retry-After header (default 2s, max 10s).
func doAniListQuery(ctx context.Context, client *http.Client, endpoint, query string, variables map[string]any) ([]byte, error) {
	// Marshal payload once; we re-read from a fresh bytes.Reader each attempt.
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})

	// attempt executes one HTTP round-trip. On HTTP 429 it returns
	// (nil, retryDelay, err) where retryDelay is the parsed Retry-After
	// and is always >= 0; the caller uses the non-nil err to detect 429.
	attempt := func() (raw []byte, retryDelay time.Duration, err error) {
		// Proactive rate limit: block until a token is available or ctx is cancelled.
		if waitErr := aniListLimiter.Wait(ctx); waitErr != nil {
			return nil, 0, waitErr
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if reqErr != nil {
			return nil, 0, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, doErr := client.Do(req)
		if doErr != nil {
			return nil, 0, fmt.Errorf("anilist: request: %w", doErr)
		}
		defer resp.Body.Close()
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		if resp.StatusCode == http.StatusTooManyRequests {
			// Parse Retry-After (seconds). Default 2s, cap at 10s.
			// We return the parsed delay (may be 0) alongside a non-nil error
			// so the outer loop can distinguish "retry-able 429" from other errors.
			d := 2 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, parseErr := strconv.Atoi(ra); parseErr == nil {
					d = time.Duration(secs) * time.Second
				}
			}
			if d > 10*time.Second {
				d = 10 * time.Second
			}
			return nil, d, fmt.Errorf("anilist: transient HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return nil, -1, fmt.Errorf("anilist: transient HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, -1, fmt.Errorf("anilist: HTTP %d", resp.StatusCode)
		}
		return raw, -1, nil
	}

	raw, retryDelay, err := attempt()
	if err != nil && retryDelay >= 0 {
		// HTTP 429: wait the Retry-After delay (may be zero), then retry once.
		if retryDelay > 0 {
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		raw, _, err = attempt()
	}
	return raw, err
}

func fetchAniListByID(ctx context.Context, client *http.Client, endpoint string, id int) (*aniListMedia, error) {
	raw, err := doAniListQuery(ctx, client, endpoint, aniListByIDQuery, map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	return parseAniListByID(raw)
}
