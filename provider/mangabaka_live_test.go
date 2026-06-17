package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLiveBackendSearchAndFetch(t *testing.T) {
	searchBody := `{"status":200,"data":[
		{"id":1677,"state":"active","title":"Chainsaw Man","type":"manga",
		 "cover":{"raw":{"url":"https://img/cover.png"}},"year":2018,"status":"completed",
		 "source":{"anilist":{"id":105778}}}]}`
	fetchBody := `{"status":200,"data":
		{"id":1677,"state":"active","title":"Chainsaw Man","type":"manga",
		 "cover":{"raw":{"url":"https://img/cover.png"}},"year":2018,"status":"completed",
		 "source":{"anilist":{"id":105778}}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/series/search" {
			_, _ = w.Write([]byte(searchBody))
			return
		}
		if r.URL.Path == "/v1/series/1677" {
			_, _ = w.Write([]byte(fetchBody))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	b := newLiveBackendWithEndpoint(srv.URL)

	results, err := b.search(context.Background(), "Chainsaw Man")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].ID != 1677 {
		t.Fatalf("search results = %+v", results)
	}

	one, err := b.fetch(context.Background(), "1677")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if one == nil || one.ID != 1677 || one.Cover.Raw.URL != "https://img/cover.png" {
		t.Fatalf("fetch result = %+v", one)
	}
}

// TestLiveBackend429RetrySucceeds verifies that a 429 on the first request is
// retried and the second (200) response is used successfully.
func TestLiveBackend429RetrySucceeds(t *testing.T) {
	searchBody := `{"status":200,"data":[{"id":1,"title":"Test","type":"manga"}]}`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchBody))
	}))
	defer srv.Close()

	b := newLiveBackendWithEndpoint(srv.URL)
	results, err := b.search(context.Background(), "Test")
	if err != nil {
		t.Fatalf("search should succeed after retry, got: %v", err)
	}
	if len(results) != 1 || results[0].ID != 1 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server was hit %d times, want 2", got)
	}
}

// TestLiveBackend429NoRetryLeft verifies that when both attempts return 429
// the error is propagated to the caller.
func TestLiveBackend429NoRetryLeft(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	b := newLiveBackendWithEndpoint(srv.URL)
	_, err := b.search(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected an error when server always 429s, got nil")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server was hit %d times, want 2", got)
	}
}
