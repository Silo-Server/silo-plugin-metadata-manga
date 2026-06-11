package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

func newMedia() aniListMedia {
	m := aniListMedia{
		ID:          30002,
		Description: "A great manga.",
		Genres:      []string{"Action", "Comedy"},
		Format:      "MANGA",
		Status:      "RELEASING",
	}
	m.Title.Romaji = "One Punch Man"
	m.Title.English = "One-Punch Man"
	m.Title.Native = "ワンパンマン"
	m.CoverImage.ExtraLarge = "https://example.test/xl.jpg"
	m.CoverImage.Large = "https://example.test/lg.jpg"
	m.BannerImage = "https://example.test/banner.png"
	m.StartDate.Year = 2012
	m.Staff.Edges = []struct {
		Role string `json:"role"`
		Node struct {
			Name struct {
				Full string `json:"full"`
			} `json:"name"`
		} `json:"node"`
	}{
		{Role: "Story"},
		{Role: "Art"},
	}
	m.Staff.Edges[0].Node.Name.Full = "ONE"
	m.Staff.Edges[1].Node.Name.Full = "Yusuke Murata"
	return m
}

func TestToMatchMapsFields(t *testing.T) {
	s := NewAniListSource(Options{})
	m := newMedia()
	got := s.toMatch(&m)

	if got.Provider != "anilist" {
		t.Fatalf("Provider = %q want anilist", got.Provider)
	}
	if got.ProviderID != "30002" {
		t.Fatalf("ProviderID = %q want 30002", got.ProviderID)
	}
	if got.Title != "One Punch Man" {
		t.Fatalf("Title = %q want romaji One Punch Man", got.Title)
	}
	if got.Description != "A great manga." {
		t.Fatalf("Description = %q", got.Description)
	}
	if got.CoverURL != "https://example.test/xl.jpg" {
		t.Fatalf("CoverURL = %q want extraLarge", got.CoverURL)
	}
	if got.BannerURL != "https://example.test/banner.png" {
		t.Fatalf("BannerURL = %q want banner image", got.BannerURL)
	}
	if got.PublishYear != 2012 {
		t.Fatalf("PublishYear = %d want 2012", got.PublishYear)
	}
	if len(got.Genres) != 2 || got.Genres[0] != "Action" {
		t.Fatalf("Genres = %v", got.Genres)
	}
	if len(got.Authors) != 2 || got.Authors[0] != "ONE" || got.Authors[1] != "Yusuke Murata" {
		t.Fatalf("Authors = %v want [ONE, Yusuke Murata]", got.Authors)
	}
}

func TestToMatchCoverFallback(t *testing.T) {
	s := NewAniListSource(Options{})
	m := newMedia()
	m.CoverImage.ExtraLarge = ""
	if got := s.toMatch(&m); got.CoverURL != "https://example.test/lg.jpg" {
		t.Fatalf("CoverURL fallback = %q want large", got.CoverURL)
	}
}

func TestToMatchPreferredLangEnglish(t *testing.T) {
	s := NewAniListSource(Options{DefaultRegion: "english"})
	m := newMedia()
	if got := s.toMatch(&m); got.Title != "One-Punch Man" {
		t.Fatalf("Title = %q want english One-Punch Man", got.Title)
	}
}

func TestToMatchTitleFallback(t *testing.T) {
	s := NewAniListSource(Options{DefaultRegion: "english"})
	m := newMedia()
	m.Title.English = ""
	// preferred english missing -> romaji
	if got := s.toMatch(&m); got.Title != "One Punch Man" {
		t.Fatalf("Title fallback = %q want romaji", got.Title)
	}
	m.Title.Romaji = ""
	if got := s.toMatch(&m); got.Title != "ワンパンマン" {
		t.Fatalf("Title fallback = %q want native", got.Title)
	}
}

func TestSearchNoConfidentMatchReturnsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// two NOVEL results -> no manga-format match -> nil
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[]}}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Nonexistent"})
	if err != nil {
		t.Fatalf("Search err = %v want nil", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %v want empty", matches)
	}
}

func TestSearchConfidentMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[
			{"id":30002,"title":{"romaji":"One Punch Man","english":"One-Punch Man","native":"x"},
			 "coverImage":{"extraLarge":"xl","large":"lg"},"description":"d","genres":["Action"],
			 "format":"MANGA","startDate":{"year":2012},"staff":{"edges":[]}}
		]}}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "One Punch Man"})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if len(matches) != 1 || matches[0].ProviderID != "30002" {
		t.Fatalf("matches = %v want one match id 30002", matches)
	}
}

// AniList's search treats typographic punctuation literally: a curly
// apostrophe in the query returns zero results for titles it would otherwise
// match exactly (verified live: "Junji Ito’s Cat Diary" → 0 results,
// "Junji Ito's Cat Diary" → 1 exact match). The outbound search term must be
// normalized to ASCII punctuation.
func TestSearchSanitizesTypographicPunctuation(t *testing.T) {
	var gotSearches []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Search string `json:"search"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotSearches = append(gotSearches, body.Variables.Search)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[]}}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	_, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Junji Ito’s “Cat Diary” – Yon & Mu"})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if want := `Junji Ito's "Cat Diary" - Yon & Mu`; len(gotSearches) == 0 || gotSearches[0] != want {
		t.Fatalf("first search term sent = %v, want %q", gotSearches, want)
	}
}

// The host always follows a successful Search with GetMetadata for the same
// record, so Fetch must serve the media the search already returned instead
// of spending a second rate-limited AniList request on it.
func TestFetchServedFromSearchCache(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[
			{"id":30002,"title":{"romaji":"One Punch Man"},"coverImage":{"extraLarge":"xl"},
			 "description":"d","genres":["Action"],"format":"MANGA","startDate":{"year":2012},"staff":{"edges":[]}}
		]}}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "One Punch Man"})
	if err != nil || len(matches) != 1 {
		t.Fatalf("Search = %v, %v; want one match", matches, err)
	}
	if calls != 1 {
		t.Fatalf("calls after search = %d, want 1", calls)
	}

	m, err := s.Fetch(context.Background(), "30002")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if m == nil || m.ProviderID != "30002" || m.Title != "One Punch Man" {
		t.Fatalf("Fetch = %+v, want cached One Punch Man", m)
	}
	if calls != 1 {
		t.Fatalf("calls after fetch = %d, want 1 (served from cache)", calls)
	}
}

func TestSearchTermVariants(t *testing.T) {
	cases := []struct {
		title string
		want  []string
	}{
		// trailing publisher/edition brackets and volume ranges are scrubbed
		{"Aoharu X Machinegun [Yen Press]", []string{"Aoharu X Machinegun [Yen Press]", "Aoharu X Machinegun"}},
		{"Animal Land v01-14", []string{"Animal Land v01-14", "Animal Land"}},
		{"Air Gear Omnibus", []string{"Air Gear Omnibus", "Air Gear"}},
		{"A Day in the Life - One-shot", []string{"A Day in the Life - One-shot", "A Day in the Life"}},
		{"Amid the Changing Seasons (LINE Webtoon) [2022-2025]", []string{"Amid the Changing Seasons (LINE Webtoon) [2022-2025]", "Amid the Changing Seasons"}},
		// edition/collection phrases are scrubbed even without a dash
		{"Highschool of the Dead Full Color Edition", []string{"Highschool of the Dead Full Color Edition", "Highschool of the Dead"}},
		// the edition scrub preserves a real subtitle the dash-strip would lose
		{"Attack on Titan - No Regrets Complete Color Edition", []string{"Attack on Titan - No Regrets Complete Color Edition", "Attack on Titan - No Regrets", "Attack on Titan"}},
		// the last " - " subtitle segment is a final fallback
		{"Blade of the Immortal - Blood of a Thousand", []string{"Blade of the Immortal - Blood of a Thousand", "Blade of the Immortal"}},
		{"Abara - Complete Deluxe Edition", []string{"Abara - Complete Deluxe Edition", "Abara"}},
		// scrub + subtitle strip stack up to three attempts
		{"Basara v01-27 [VIZ Media]", []string{"Basara v01-27 [VIZ Media]", "Basara"}},
		{"ACCA - 13-ku Kansatsuka - P.S [Yen Press]", []string{"ACCA - 13-ku Kansatsuka - P.S [Yen Press]", "ACCA - 13-ku Kansatsuka - P.S", "ACCA - 13-ku Kansatsuka"}},
		// clean titles get exactly one attempt
		{"One Piece", []string{"One Piece"}},
		// a short base is not a meaningful subtitle strip
		{"Ai - Re", []string{"Ai - Re"}},
	}
	for _, tc := range cases {
		got := searchTermVariants(tc.title)
		if len(got) != len(tc.want) {
			t.Fatalf("searchTermVariants(%q) = %v, want %v", tc.title, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("searchTermVariants(%q) = %v, want %v", tc.title, got, tc.want)
			}
		}
	}
}

// When the first search yields no confident match, Search retries with
// scrubbed variants and matches each result set against the variant term it
// actually searched.
func TestSearchFallsBackToScrubbedVariants(t *testing.T) {
	var terms []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Search string `json:"search"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		terms = append(terms, body.Variables.Search)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Search == "Animal Land" {
			_, _ = w.Write([]byte(`{"data":{"Page":{"media":[
				{"id":7,"title":{"romaji":"Doubutsu no Kuni","english":"Animal Land"},"coverImage":{"extraLarge":"xl"},
				 "format":"MANGA","startDate":{"year":2009},"staff":{"edges":[]}}
			]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Page":{"media":[]}}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	matches, err := s.Search(context.Background(), metadata.SearchQuery{Title: "Animal Land v01-14"})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if len(matches) != 1 || matches[0].ProviderID != "7" {
		t.Fatalf("matches = %v, want Animal Land via scrubbed variant", matches)
	}
	if len(terms) != 2 || terms[0] != "Animal Land v01-14" || terms[1] != "Animal Land" {
		t.Fatalf("search terms = %v, want original then scrubbed", terms)
	}
}

func TestFetchByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Media":
			{"id":30002,"title":{"romaji":"One Punch Man"},"coverImage":{"extraLarge":"xl"},
			 "description":"d","genres":["Action"],"format":"MANGA","startDate":{"year":2012},"staff":{"edges":[]}}
		}}`))
	}))
	defer srv.Close()

	s := NewAniListSource(Options{})
	s.endpoint = srv.URL

	for _, id := range []string{"30002", "anilist:30002"} {
		m, err := s.Fetch(context.Background(), id)
		if err != nil {
			t.Fatalf("Fetch(%q) err = %v", id, err)
		}
		if m == nil || m.ProviderID != "30002" || m.Title != "One Punch Man" {
			t.Fatalf("Fetch(%q) = %+v", id, m)
		}
	}
}

// AniList descriptions embed HTML even when requested asHtml:false (<i>,
// <br>, entities). The stored overview must be plain text: tags stripped,
// <br> runs as line breaks, entities decoded.
func TestCleanDescription(t *testing.T) {
	in := `<i>&ldquo;I have no interest in real girls!&rdquo;</i> <br><br> So claims Okumura &amp; co. <br><br> <i>Notes:<br> - Won 4th place.<br> - Includes 40 extra chapters.</i>`
	got := cleanDescription(in)
	want := "“I have no interest in real girls!”\n\nSo claims Okumura & co.\n\nNotes:\n- Won 4th place.\n- Includes 40 extra chapters."
	if got != want {
		t.Fatalf("cleanDescription:\ngot:  %q\nwant: %q", got, want)
	}
	if cleanDescription("plain text") != "plain text" {
		t.Fatalf("plain text must pass through unchanged")
	}
}

func TestMangaStatusLabel(t *testing.T) {
	cases := map[string]string{
		"RELEASING":        "Ongoing",
		"ongoing":          "Ongoing",
		"FINISHED":         "Completed",
		"completed":        "Completed",
		"HIATUS":           "Hiatus",
		"CANCELLED":        "Cancelled",
		"NOT_YET_RELEASED": "Upcoming",
		"":                 "",
		"WHATEVER":         "",
	}
	for in, want := range cases {
		if got := mangaStatusLabel(in); got != want {
			t.Fatalf("mangaStatusLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToMatchMapsStatus(t *testing.T) {
	s := NewAniListSource(Options{})
	m := newMedia()
	if got := s.toMatch(&m); got.Status != "Ongoing" {
		t.Fatalf("Status = %q want Ongoing (from RELEASING)", got.Status)
	}
}
