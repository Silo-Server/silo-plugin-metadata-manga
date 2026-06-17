package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBannerEnricherFetchesByID(t *testing.T) {
	body := `{"data":{"Media":{"id":105778,"bannerImage":"https://anilist/banner.jpg"}}}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	e := newAniListBannerEnricherWithEndpoint(srv.URL)

	got, err := e.banner(context.Background(), 105778)
	if err != nil {
		t.Fatalf("banner: %v", err)
	}
	if got != "https://anilist/banner.jpg" {
		t.Fatalf("banner = %q", got)
	}

	// Second call for the same id must be served from cache (no extra HTTP).
	if _, err := e.banner(context.Background(), 105778); err != nil {
		t.Fatalf("banner cached: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls)
	}
}

func TestBannerEnricherZeroIDReturnsEmpty(t *testing.T) {
	e := newAniListBannerEnricherWithEndpoint("http://unused")
	got, err := e.banner(context.Background(), 0)
	if err != nil || got != "" {
		t.Fatalf("zero id => (%q, %v)", got, err)
	}
}

// TestBannerEnricherExpiredEntryEvictedOnPut verifies that stale entries are
// removed during the eviction pass triggered when the cache is at capacity,
// rather than wiping all hot entries with a full flush.
func TestBannerEnricherExpiredEntryEvictedOnPut(t *testing.T) {
	e := newAniListBannerEnricherWithEndpoint("http://unused")

	// Fill the cache to capacity using ids 1..fetchCacheMax.
	// All entries are live (unexpired) except id=1, which we expire below.
	for i := 1; i <= fetchCacheMax; i++ {
		e.cachePut(i, "https://anilist/banner.jpg")
	}

	// Manually expire id=1 so the next cachePut evicts it rather than flushing.
	e.mu.Lock()
	entry := e.cache[1]
	entry.expires = entry.expires.Add(-2 * fetchCacheTTL)
	e.cache[1] = entry
	e.mu.Unlock()

	// Adding one more entry must trigger the eviction pass: id=1 (expired) is
	// deleted; the remaining fetchCacheMax-1 hot entries must survive.
	const newID = fetchCacheMax + 1
	e.cachePut(newID, "https://anilist/new.jpg")

	e.mu.Lock()
	_, expiredStillPresent := e.cache[1]
	_, newPresent := e.cache[newID]
	size := len(e.cache)
	e.mu.Unlock()

	if expiredStillPresent {
		t.Errorf("expired entry for id=1 was not evicted")
	}
	if !newPresent {
		t.Errorf("new entry for id=%d was not inserted", newID)
	}
	// After evicting id=1 and inserting newID, size should be fetchCacheMax.
	if size != fetchCacheMax {
		t.Errorf("cache size = %d, want %d", size, fetchCacheMax)
	}
}

// TestBannerEnricherFullFlushWhenAllHot verifies that cachePut falls back to a
// full flush only when no expired entries can free capacity (all entries are hot).
func TestBannerEnricherFullFlushWhenAllHot(t *testing.T) {
	e := newAniListBannerEnricherWithEndpoint("http://unused")

	// Fill the cache to capacity; all entries are unexpired.
	for i := 1; i <= fetchCacheMax; i++ {
		e.cachePut(i, "https://anilist/banner.jpg")
	}

	// Adding one more entry when everything is hot must fall back to a full
	// flush — the new entry is the only thing in the cache afterwards.
	const newID = fetchCacheMax + 1
	e.cachePut(newID, "https://anilist/new.jpg")

	e.mu.Lock()
	_, newPresent := e.cache[newID]
	size := len(e.cache)
	e.mu.Unlock()

	if !newPresent {
		t.Errorf("new entry for id=%d was not inserted after flush", newID)
	}
	if size != 1 {
		t.Errorf("cache size after full flush = %d, want 1", size)
	}
}
