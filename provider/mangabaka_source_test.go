package provider

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

// stubBackend is an in-memory mangaBakaBackend for testing source behavior
// without network or disk.
type stubBackend struct {
	isReady bool
	series  []mangaBakaSeries
}

func (s *stubBackend) ready() bool { return s.isReady }
func (s *stubBackend) search(_ context.Context, term string) ([]mangaBakaSeries, error) {
	return s.series, nil
}
func (s *stubBackend) fetch(_ context.Context, id string) (*mangaBakaSeries, error) {
	for i := range s.series {
		if s.series[i].ProviderIDString() == id {
			return &s.series[i], nil
		}
	}
	return nil, nil
}

// countingBackend wraps a stubBackend and counts fetch calls so tests can
// assert whether a backend round-trip happened.
type countingBackend struct {
	stub       *stubBackend
	fetchCalls int
}

func (c *countingBackend) ready() bool { return c.stub.isReady }
func (c *countingBackend) search(ctx context.Context, term string) ([]mangaBakaSeries, error) {
	return c.stub.search(ctx, term)
}
func (c *countingBackend) fetch(ctx context.Context, id string) (*mangaBakaSeries, error) {
	c.fetchCalls++
	return c.stub.fetch(ctx, id)
}

func TestMangaBakaSourcePrefersDumpWhenReady(t *testing.T) {
	dump := &stubBackend{isReady: true, series: []mangaBakaSeries{mbSeries(1, "Chainsaw Man")}}
	live := &stubBackend{isReady: true, series: []mangaBakaSeries{mbSeries(2, "Wrong")}}
	src := newMangaBakaSourceWithBackends(dump, live, nil)

	got, err := src.Search(context.Background(), metadata.SearchQuery{Title: "Chainsaw Man"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ProviderID != "1" {
		t.Fatalf("expected dump match id 1, got %+v", got)
	}
}

func TestMangaBakaSourceFallsBackToLiveWhenDumpNotReady(t *testing.T) {
	dump := &stubBackend{isReady: false}
	live := &stubBackend{isReady: true, series: []mangaBakaSeries{mbSeries(2, "Chainsaw Man")}}
	src := newMangaBakaSourceWithBackends(dump, live, nil)

	got, err := src.Search(context.Background(), metadata.SearchQuery{Title: "Chainsaw Man"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ProviderID != "2" {
		t.Fatalf("expected live match id 2, got %+v", got)
	}
}

// TestMangaBakaSourceStartCloseNoDumpIsNoop verifies that with the dump backend
// disabled, Start()/Close() neither panic nor create any files.
func TestMangaBakaSourceStartCloseNoDumpIsNoop(t *testing.T) {
	src := NewMangaBakaSource(Options{})
	src.Start()
	if err := src.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil", err)
	}
	// Start/Close again to confirm idempotency without panic.
	src.Start()
	if err := src.Close(); err != nil {
		t.Fatalf("second Close err = %v, want nil", err)
	}
}

func TestMangaBakaSourceEnrichesBanner(t *testing.T) {
	s := mbSeries(1, "Chainsaw Man")
	s.Source = map[string]mangaBakaSourceRef{"anilist": {ID: []byte("105778")}}
	dump := &stubBackend{isReady: true, series: []mangaBakaSeries{s}}
	bannerFn := func(_ context.Context, id int) (string, error) {
		if id == 105778 {
			return "https://anilist/banner.jpg", nil
		}
		return "", nil
	}
	src := newMangaBakaSourceWithBackends(dump, &stubBackend{}, bannerFn)

	got, err := src.Search(context.Background(), metadata.SearchQuery{Title: "Chainsaw Man"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].BannerURL != "https://anilist/banner.jpg" {
		t.Fatalf("banner not enriched: %+v", got)
	}
}

// TestMangaBakaSourceFetchUsesSearchCache verifies that a Fetch for an id
// previously seen via Search is served from the in-memory cache, without
// calling the backend's fetch method.
func TestMangaBakaSourceFetchUsesSearchCache(t *testing.T) {
	series := mbSeries(42, "Chainsaw Man")
	cb := &countingBackend{stub: &stubBackend{isReady: true, series: []mangaBakaSeries{series}}}
	src := newMangaBakaSourceWithBackends(cb, &stubBackend{}, nil)

	// Populate the cache via Search.
	results, err := src.Search(context.Background(), metadata.SearchQuery{Title: "Chainsaw Man"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ProviderID != "42" {
		t.Fatalf("Search returned unexpected results: %+v", results)
	}

	// Fetch must not call the backend — served from cache.
	match, err := src.Fetch(context.Background(), "mangabaka:42")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if match == nil || match.ProviderID != "42" {
		t.Fatalf("Fetch returned unexpected match: %+v", match)
	}
	if cb.fetchCalls != 0 {
		t.Fatalf("expected 0 backend fetch calls, got %d", cb.fetchCalls)
	}
}

// TestMangaBakaSourceFetchCallsBackendForUnseenID verifies that Fetch for an
// id not previously populated by Search does call the backend.
func TestMangaBakaSourceFetchCallsBackendForUnseenID(t *testing.T) {
	series := mbSeries(99, "Fire Punch")
	cb := &countingBackend{stub: &stubBackend{isReady: true, series: []mangaBakaSeries{series}}}
	src := newMangaBakaSourceWithBackends(cb, &stubBackend{}, nil)

	// Do NOT call Search first — the cache should be empty for id "99".
	match, err := src.Fetch(context.Background(), "mangabaka:99")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if match == nil || match.ProviderID != "99" {
		t.Fatalf("Fetch returned unexpected match: %+v", match)
	}
	if cb.fetchCalls != 1 {
		t.Fatalf("expected 1 backend fetch call, got %d", cb.fetchCalls)
	}
}
