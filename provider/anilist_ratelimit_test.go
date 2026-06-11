package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSearchAniListRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[{"id":1,"title":{"romaji":"X"},"format":"MANGA"}]}}}`))
	}))
	defer srv.Close()
	media, err := searchAniList(context.Background(), http.DefaultClient, srv.URL, "X")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if len(media) != 1 || media[0].ID != 1 {
		t.Fatalf("bad result after retry: %+v", media)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls (429 then 200), got %d", calls)
	}
}
