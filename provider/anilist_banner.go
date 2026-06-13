package provider

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// aniListBannerEnricher fetches only the bannerImage for a known AniList id.
// MangaBaka records carry source.anilist.id, so this never searches — it is a
// single cached by-id GraphQL lookup, gated by the shared aniListLimiter.
type aniListBannerEnricher struct {
	client   *http.Client
	endpoint string

	mu    sync.Mutex
	cache map[int]bannerCacheEntry
}

type bannerCacheEntry struct {
	url     string
	expires time.Time
}

func newAniListBannerEnricher() *aniListBannerEnricher {
	return newAniListBannerEnricherWithEndpoint(aniListEndpoint)
}

func newAniListBannerEnricherWithEndpoint(endpoint string) *aniListBannerEnricher {
	return &aniListBannerEnricher{
		client:   &http.Client{Timeout: 15 * time.Second},
		endpoint: endpoint,
		cache:    make(map[int]bannerCacheEntry),
	}
}

func (e *aniListBannerEnricher) banner(ctx context.Context, anilistID int) (string, error) {
	if anilistID <= 0 {
		return "", nil
	}
	if url, ok := e.cacheGet(anilistID); ok {
		return url, nil
	}
	if err := aniListLimiter.Wait(ctx); err != nil {
		return "", err
	}
	media, err := fetchAniListByID(ctx, e.client, e.endpoint, anilistID)
	if err != nil {
		return "", err
	}
	url := ""
	if media != nil {
		url = strings.TrimSpace(media.BannerImage)
	}
	e.cachePut(anilistID, url)
	return url, nil
}

func (e *aniListBannerEnricher) cacheGet(id int) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.cache[id]
	if !ok || time.Now().After(entry.expires) {
		return "", false
	}
	return entry.url, true
}

func (e *aniListBannerEnricher) cachePut(id int, url string) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cache) >= fetchCacheMax {
		for k, entry := range e.cache {
			if now.After(entry.expires) {
				delete(e.cache, k)
			}
		}
		if len(e.cache) >= fetchCacheMax {
			e.cache = make(map[int]bannerCacheEntry)
		}
	}
	e.cache[id] = bannerCacheEntry{url: url, expires: now.Add(fetchCacheTTL)}
}
