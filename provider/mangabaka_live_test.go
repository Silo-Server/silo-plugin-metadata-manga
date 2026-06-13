package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
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
