package provider

import "testing"

func TestToMatchFromMangaBakaMapsCoreFields(t *testing.T) {
	s := mangaBakaSeries{
		ID:             1677,
		Title:          "Chainsaw Man",
		NativeTitle:    "チェンソーマン",
		RomanizedTitle: "Chainsaw Man",
		Description:    "Denji was a small-time devil hunter.",
		Year:           2018,
		Status:         "completed",
		ContentRating:  "suggestive",
		Type:           "manga",
		Genres:         []string{"action", "horror"},
		Authors:        []string{"Tatsuki Fujimoto"},
		Artists:        []string{"Tatsuki Fujimoto"},
	}
	s.Cover.Raw.URL = "https://images.mangabaka.dev/cover.png"
	s.Source = map[string]mangaBakaSourceRef{
		"anilist":       {ID: []byte("105778")},
		"my_anime_list": {ID: []byte("\"116778\"")},
	}

	m := toMatchFromMangaBaka(s)

	if m.Provider != "mangabaka" || m.ProviderID != "1677" {
		t.Fatalf("provider/id = %q/%q", m.Provider, m.ProviderID)
	}
	if m.Title != "Chainsaw Man" {
		t.Fatalf("title = %q", m.Title)
	}
	if m.CoverURL != "https://images.mangabaka.dev/cover.png" {
		t.Fatalf("cover = %q", m.CoverURL)
	}
	if m.Status != "Completed" {
		t.Fatalf("status = %q, want Completed", m.Status)
	}
	if m.PublishYear != 2018 {
		t.Fatalf("year = %d", m.PublishYear)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Tatsuki Fujimoto" {
		t.Fatalf("authors = %v", m.Authors)
	}
	if m.ExternalIDs["anilist"] != "105778" || m.ExternalIDs["my_anime_list"] != "116778" {
		t.Fatalf("external ids = %v", m.ExternalIDs)
	}
	if m.ContentRating != "suggestive" {
		t.Fatalf("content rating = %q", m.ContentRating)
	}
}

func TestAuthorsMergeDedupesArtists(t *testing.T) {
	s := mangaBakaSeries{
		Authors: []string{"A", "B"},
		Artists: []string{"B", "C"},
	}
	got := mangaBakaPeople(s)
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("people = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("people[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
