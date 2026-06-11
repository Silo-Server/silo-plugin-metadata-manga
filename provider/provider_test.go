package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

type fakeSource struct {
	id      string
	matches []metadata.Match
	err     error
}

func (f *fakeSource) ID() string { return f.id }

func (f *fakeSource) Search(context.Context, metadata.SearchQuery) ([]metadata.Match, error) {
	return f.matches, f.err
}

func (f *fakeSource) Fetch(context.Context, string) (*metadata.Match, error) {
	if len(f.matches) == 0 {
		return nil, f.err
	}
	m := f.matches[0]
	return &m, f.err
}

// A transient source failure (timeout, 429, network) must surface as a Search
// error: the host stamps an empty-success result as a terminal "no match",
// permanently excluding the item from future sweeps.
func TestSearchAllSourcesErroredReturnsError(t *testing.T) {
	wantErr := errors.New("anilist: transient HTTP 429")
	p := NewProviderWithSources([]Source{&fakeSource{id: "anilist", err: wantErr}})

	matches, err := p.Search(context.Background(), metadata.SearchQuery{Title: "X"})
	if err == nil {
		t.Fatalf("Search err = nil, want error wrapping %v", wantErr)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search err = %v, want errors.Is(%v)", err, wantErr)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %v, want empty", matches)
	}
}

// A genuine empty result set (sources answered, nothing matched) stays a
// non-error so the host can legitimately mark the item as having no match.
func TestSearchNoMatchesNoErrorsReturnsEmpty(t *testing.T) {
	p := NewProviderWithSources([]Source{&fakeSource{id: "anilist"}})

	matches, err := p.Search(context.Background(), metadata.SearchQuery{Title: "X"})
	if err != nil {
		t.Fatalf("Search err = %v, want nil", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %v, want empty", matches)
	}
}

// If one source errors but another produced a match, the match wins: partial
// provider trouble must not block enrichment.
func TestSearchPartialErrorWithMatchSucceeds(t *testing.T) {
	match := metadata.Match{Provider: "anilist", ProviderID: "1", Title: "X"}
	p := NewProviderWithSources([]Source{
		&fakeSource{id: "broken", err: errors.New("boom")},
		&fakeSource{id: "anilist", matches: []metadata.Match{match}},
	})

	matches, err := p.Search(context.Background(), metadata.SearchQuery{Title: "X"})
	if err != nil {
		t.Fatalf("Search err = %v, want nil when a match exists", err)
	}
	if len(matches) != 1 || matches[0].ProviderID != "1" {
		t.Fatalf("matches = %v, want the anilist match", matches)
	}
}

// Sources are consulted in registration order: the first confident match
// wins and later sources are never reached.
func TestSearchPrefersFirstSourceMatch(t *testing.T) {
	first := metadata.Match{Provider: "anilist", ProviderID: "1", Title: "X"}
	second := metadata.Match{Provider: "mangadex", ProviderID: "uuid", Title: "X"}
	p := NewProviderWithSources([]Source{
		&fakeSource{id: "anilist", matches: []metadata.Match{first}},
		&fakeSource{id: "mangadex", matches: []metadata.Match{second}},
	})

	matches, err := p.Search(context.Background(), metadata.SearchQuery{Title: "X"})
	if err != nil || len(matches) != 1 {
		t.Fatalf("Search = %v, %v; want one match", matches, err)
	}
	if matches[0].Provider != "anilist" {
		t.Fatalf("match provider = %q, want the first source", matches[0].Provider)
	}
}

// A source with no confident match falls through to the next source.
func TestSearchFallsBackToSecondSource(t *testing.T) {
	second := metadata.Match{Provider: "mangadex", ProviderID: "uuid", Title: "X"}
	p := NewProviderWithSources([]Source{
		&fakeSource{id: "anilist"},
		&fakeSource{id: "mangadex", matches: []metadata.Match{second}},
	})

	matches, err := p.Search(context.Background(), metadata.SearchQuery{Title: "X"})
	if err != nil || len(matches) != 1 {
		t.Fatalf("Search = %v, %v; want fallback match", matches, err)
	}
	if matches[0].Provider != "mangadex" {
		t.Fatalf("match provider = %q, want the fallback source", matches[0].Provider)
	}
}
