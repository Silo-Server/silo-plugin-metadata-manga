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
