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
