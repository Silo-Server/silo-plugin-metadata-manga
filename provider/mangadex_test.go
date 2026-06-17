package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

const mangaDexSearchFixture = `{"result":"ok","data":[
  {"id":"uuid-1","attributes":{
     "title":{"en":"Karate Heat"},
     "altTitles":[{"ja-ro":"Bukatsu Shoujo to Karate Kid"}],
     "description":{"en":"A karate story."},
     "year":2019,"status":"completed",
     "tags":[{"attributes":{"name":{"en":"Sports"},"group":"genre"}},
             {"attributes":{"name":{"en":"School Life"},"group":"theme"}}]},
   "relationships":[
     {"type":"author","attributes":{"name":"Some Author"}},
     {"type":"artist","attributes":{"name":"Some Artist"}},
     {"type":"cover_art","attributes":{"fileName":"cover.jpg"}}]},
  {"id":"uuid-2","attributes":{
     "title":{"en":"Karate Heat Gaiden"},
     "altTitles":[],"description":{},"year":2021,"status":"ongoing","tags":[]},
   "relationships":[]}
]}`

func TestMangaDexSearchConfidentMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua == "" || ua == "Go-http-client/1.1" {
			t.Errorf("MangaDex requires a descriptive User-Agent, got %q", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mangaDexSearchFixture))
	}))
	defer srv.Close()

	s := NewMangaDexSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Karate Heat"})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %v, want exactly one", matches)
	}
	m := matches[0]
	if m.Provider != "mangadex" || m.ProviderID != "uuid-1" {
		t.Fatalf("match identity = %s:%s, want mangadex:uuid-1", m.Provider, m.ProviderID)
	}
	if m.Title != "Karate Heat" {
		t.Fatalf("Title = %q", m.Title)
	}
	if m.CoverURL != "https://uploads.mangadex.org/covers/uuid-1/cover.jpg" {
		t.Fatalf("CoverURL = %q", m.CoverURL)
	}
	if len(m.Genres) != 1 || m.Genres[0] != "Sports" {
		t.Fatalf("Genres = %v, want genre-group tags only", m.Genres)
	}
	if len(m.Authors) != 2 || m.Authors[0] != "Some Author" || m.Authors[1] != "Some Artist" {
		t.Fatalf("Authors = %v", m.Authors)
	}
	if m.PublishYear != 2019 {
		t.Fatalf("PublishYear = %d", m.PublishYear)
	}
}

func TestMangaDexMatchViaAltTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mangaDexSearchFixture))
	}))
	defer srv.Close()

	s := NewMangaDexSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Bukatsu Shoujo to Karate Kid"})
	if err != nil || len(matches) != 1 || matches[0].ProviderID != "uuid-1" {
		t.Fatalf("alt-title match = %v, %v; want uuid-1", matches, err)
	}
}

func TestMangaDexAmbiguousTieStaysNil(t *testing.T) {
	body := `{"result":"ok","data":[
	  {"id":"a","attributes":{"title":{"en":"Dup"},"altTitles":[],"description":{},"tags":[]},"relationships":[]},
	  {"id":"b","attributes":{"title":{"ja-ro":"Dup"},"altTitles":[],"description":{},"tags":[]},"relationships":[]}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewMangaDexSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Dup"})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("ambiguous tie must stay no-match, got %v", matches)
	}
}

func TestMangaDexFetchServedFromSearchCache(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mangaDexSearchFixture))
	}))
	defer srv.Close()

	s := NewMangaDexSource(Options{})
	s.endpoint = srv.URL

	if _, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Karate Heat"}); err != nil {
		t.Fatalf("Search err = %v", err)
	}
	got, err := s.Fetch(context.Background(), "mangadex:uuid-1")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if got == nil || got.ProviderID != "uuid-1" {
		t.Fatalf("Fetch = %+v", got)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (fetch served from cache)", calls)
	}
}

// MangaDex official English titles often append the full subtitle to the
// licensed short name; a folder named with the licensed title must match as a
// unique prefix.
func TestMangaDexPrefixMatch(t *testing.T) {
	body := `{"result":"ok","data":[
	  {"id":"long","attributes":{"title":{"ja-ro":"Konyaku Haki o Neratte"},
	    "altTitles":[{"en":"Fake It to Break It! I Faked Amnesia to Break off My Engagement"}],
	    "description":{},"tags":[]},"relationships":[]},
	  {"id":"other","attributes":{"title":{"en":"Lunch Break"},"altTitles":[],"description":{},"tags":[]},"relationships":[]}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewMangaDexSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Fake It to Break It!"})
	if err != nil || len(matches) != 1 || matches[0].ProviderID != "long" {
		t.Fatalf("prefix match = %v, %v; want the long-titled candidate", matches, err)
	}

	// short queries never prefix-match
	short, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Lunch"})
	if err != nil || len(short) != 0 {
		t.Fatalf("short prefix should not match, got %v, %v", short, err)
	}
}
