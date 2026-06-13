package provider

import "testing"

func mbSeries(id int, title string, secondary ...string) mangaBakaSeries {
	s := mangaBakaSeries{ID: id, Title: title, Type: "manga"}
	if len(secondary) > 0 {
		group := make([]mangaBakaSecondaryTitle, 0, len(secondary))
		for _, t := range secondary {
			group = append(group, mangaBakaSecondaryTitle{Type: "alternative", Title: t})
		}
		s.SecondaryTitles = map[string][]mangaBakaSecondaryTitle{"en": group}
	}
	return s
}

func TestPickConfidentMangaBakaMatchExact(t *testing.T) {
	cands := []mangaBakaSeries{
		mbSeries(1, "Chainsaw Man"),
		mbSeries(2, "Fire Punch"),
	}
	got := pickConfidentMangaBakaMatch("chainsaw man", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("got %+v, want id 1", got)
	}
}

func TestPickConfidentMangaBakaMatchSecondaryTitle(t *testing.T) {
	cands := []mangaBakaSeries{mbSeries(1, "Kanojo, Okarishimasu", "Rent-A-Girlfriend")}
	got := pickConfidentMangaBakaMatch("Rent-A-Girlfriend", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("secondary-title match failed: %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchAmbiguousIsNil(t *testing.T) {
	cands := []mangaBakaSeries{
		mbSeries(1, "Bleach"),
		mbSeries(2, "Bleach"),
	}
	if got := pickConfidentMangaBakaMatch("bleach", cands); got != nil {
		t.Fatalf("ambiguous exact match must be nil, got %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchNoMatch(t *testing.T) {
	cands := []mangaBakaSeries{mbSeries(1, "Naruto")}
	if got := pickConfidentMangaBakaMatch("One Piece", cands); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func mbSeriesChapters(id int, title, chapters string) mangaBakaSeries {
	s := mbSeries(id, title)
	s.TotalChapters = chapters
	return s
}

func TestPickConfidentMangaBakaMatchChapterDominanceResolves(t *testing.T) {
	// A real serialization (200 chapters) vs a same-titled side-story (3): the
	// dominant serialization wins instead of resolving to no match.
	cands := []mangaBakaSeries{
		mbSeriesChapters(1, "Spy x Family", "200"),
		mbSeriesChapters(2, "Spy x Family", "3"),
	}
	got := pickConfidentMangaBakaMatch("Spy x Family", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("chapter-dominant candidate should win, got %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchComparableChaptersStayAmbiguous(t *testing.T) {
	// Two genuine works of comparable length (the "One Punch Man" manga vs its
	// webcomic): neither dominates, so it stays a no match rather than a guess.
	cands := []mangaBakaSeries{
		mbSeriesChapters(1, "One Punch Man", "200"),
		mbSeriesChapters(2, "One Punch Man", "180"),
	}
	if got := pickConfidentMangaBakaMatch("One Punch Man", cands); got != nil {
		t.Fatalf("comparable-length works must stay ambiguous (nil), got %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchNovelExcluded(t *testing.T) {
	// Two candidates with the same exact title: one manga (id 1), one novel (id 2).
	// The matcher must return the manga, NOT nil from ambiguity and NOT the novel.
	manga := mbSeries(1, "Sword Art Online")
	novel := mbSeries(2, "Sword Art Online")
	novel.Type = "novel"
	cands := []mangaBakaSeries{manga, novel}
	got := pickConfidentMangaBakaMatch("Sword Art Online", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("expected manga (id 1), got %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchLightNovelExcluded(t *testing.T) {
	// A single candidate with Type:"light_novel" and an exact title must return nil.
	s := mbSeries(1, "Overlord")
	s.Type = "light_novel"
	cands := []mangaBakaSeries{s}
	if got := pickConfidentMangaBakaMatch("Overlord", cands); got != nil {
		t.Fatalf("light_novel candidate must be excluded, got %+v", got)
	}
}

// TestPickConfidentMangaBakaMatchSuffixCJKCoverage verifies Finding A: the
// suffix coverage gate counts runes, not bytes. The candidate's normalized
// title is a multi-byte (CJK) prefix followed by an ASCII suffix equal to the
// query. The query covers >= half the title by RUNE count, but under the old
// byte-length coverage check the multi-byte prefix inflated len(normalized) and
// wrongly rejected the match.
func TestPickConfidentMangaBakaMatchSuffixCJKCoverage(t *testing.T) {
	// Query "Shield Hero" normalizes to "shieldhero" (10 runes). The candidate
	// "シールドShield Hero" normalizes to "シールド" (4 CJK runes, 12 bytes) +
	// "shieldhero" (10 runes/bytes) = 14 runes / 22 bytes total.
	//   rune coverage: 10*2 = 20 >= 14  -> passes
	//   byte coverage: 10*2 = 20 >= 22  -> FAILS (the old, wrong check)
	// So this match only surfaces once coverage counts runes.
	cands := []mangaBakaSeries{
		mbSeries(1, "シールドShield Hero"),
	}
	got := pickConfidentMangaBakaMatch("Shield Hero", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("CJK-prefixed suffix match failed (rune coverage): %+v", got)
	}
}

func TestAcceptMangaBakaType(t *testing.T) {
	cases := []struct {
		t      string
		accept bool
	}{
		{"manga", true},
		{"manhwa", true},
		{"", true},
		{"unknown", true},
		{"novel", false},
		{"light_novel", false},
		{"light novel", false},
		{"Novel", false},
		{"Light_Novel", false},
	}
	for _, tc := range cases {
		if got := acceptMangaBakaType(tc.t); got != tc.accept {
			t.Errorf("acceptMangaBakaType(%q) = %v, want %v", tc.t, got, tc.accept)
		}
	}
}
