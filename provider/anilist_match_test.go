package provider

import "testing"

func med(id int, romaji, english, format string) aniListMedia {
	var m aniListMedia
	m.ID = id
	m.Title.Romaji = romaji
	m.Title.English = english
	m.Format = format
	return m
}

func TestPickConfidentMatch(t *testing.T) {
	cands := []aniListMedia{med(1, "One Punch-Man", "One-Punch Man", "MANGA"), med(2, "Onepunch", "", "MANGA")}
	m := pickConfidentMatch("One-Punch Man", cands)
	if m == nil || m.ID != 1 {
		t.Fatalf("exact english match expected id 1, got %v", m)
	}
	// near-exact (punctuation/case) still matches
	if m := pickConfidentMatch("404 demons", []aniListMedia{med(3, "404 Demons", "", "MANGA")}); m == nil || m.ID != 3 {
		t.Fatalf("near-exact expected id 3, got %v", m)
	}
	// ambiguous: two equally-good matches → no match
	if m := pickConfidentMatch("Berserk", []aniListMedia{med(4, "Berserk", "", "MANGA"), med(5, "Berserk", "", "MANGA")}); m != nil {
		t.Fatalf("ambiguous tie should be no-match, got %v", m)
	}
	// serialization + same-titled pilot one-shot: the unique MANGA wins
	// (e.g. AniList lists "Black Clover" as both MANGA 2015 and ONE_SHOT 2014)
	if m := pickConfidentMatch("Black Clover", []aniListMedia{
		med(10, "Black Clover", "Black Clover", "MANGA"),
		med(11, "Black Clover", "", "ONE_SHOT"),
	}); m == nil || m.ID != 10 {
		t.Fatalf("MANGA format should break the pilot one-shot tie, got %v", m)
	}
	// two MANGA-format exact matches stay ambiguous
	if m := pickConfidentMatch("Dup", []aniListMedia{
		med(12, "Dup", "", "MANGA"),
		med(13, "Dup", "", "MANGA"),
		med(14, "Dup", "", "ONE_SHOT"),
	}); m != nil {
		t.Fatalf("two MANGA exact matches should remain no-match, got %v", m)
	}
	// leading zeros in digit runs normalize away ("Part 01" ≡ "Part 1")
	if m := pickConfidentMatch("Ascendance Part 01", []aniListMedia{med(15, "Ascendance Part 1", "", "MANGA")}); m == nil || m.ID != 15 {
		t.Fatalf("leading-zero digits should normalize, got %v", m)
	}
	// NOVEL format excluded even on exact title
	if m := pickConfidentMatch("Overlord", []aniListMedia{med(6, "Overlord", "", "NOVEL")}); m != nil {
		t.Fatalf("NOVEL must be excluded, got %v", m)
	}
	// no candidates → nil
	if m := pickConfidentMatch("Nonexistent", nil); m != nil {
		t.Fatalf("expected nil, got %v", m)
	}
}

func TestPickConfidentMatchSynonyms(t *testing.T) {
	withSyn := func(m aniListMedia, syns ...string) aniListMedia {
		m.Synonyms = syns
		return m
	}

	// no title matches, exactly one candidate carries the query as a synonym
	cands := []aniListMedia{
		withSyn(med(1, "Kase-san Series", "Kase-san and...", "MANGA"), "Morning Glory and Kase-san.", "Kase-san and Bento"),
		med(2, "Yamada to Kase-san.", "Kase-san and Yamada", "MANGA"),
	}
	if m := pickConfidentMatch("Kase-san and Bento", cands); m == nil || m.ID != 1 {
		t.Fatalf("unique synonym match expected id 1, got %v", m)
	}

	// a unique exact TITLE match wins before synonyms are consulted
	cands = []aniListMedia{
		withSyn(med(3, "Alpha", "", "MANGA"), "Shared Name"),
		med(4, "Shared Name", "", "MANGA"),
	}
	if m := pickConfidentMatch("Shared Name", cands); m == nil || m.ID != 4 {
		t.Fatalf("title match must take precedence, got %v", m)
	}

	// the same synonym on two candidates is ambiguous → no match
	cands = []aniListMedia{
		withSyn(med(5, "A", "", "MANGA"), "Common Alias"),
		withSyn(med(6, "B", "", "MANGA"), "Common Alias"),
	}
	if m := pickConfidentMatch("Common Alias", cands); m != nil {
		t.Fatalf("ambiguous synonym should be no-match, got %v", m)
	}

	// synonym on a NOVEL stays excluded
	if m := pickConfidentMatch("Light Novel Alias", []aniListMedia{withSyn(med(7, "X", "", "NOVEL"), "Light Novel Alias")}); m != nil {
		t.Fatalf("NOVEL must be excluded, got %v", m)
	}
}

func medPop(id int, romaji, format string, popularity int) aniListMedia {
	m := med(id, romaji, "", format)
	m.Popularity = popularity
	return m
}

// Ampersand reads as "and" ("A Man & His Cat" matches "A Man and His Cat").
func TestPickConfidentMatchAmpersand(t *testing.T) {
	if m := pickConfidentMatch("A Man & His Cat", []aniListMedia{med(16, "A Man and His Cat", "", "MANGA")}); m == nil || m.ID != 16 {
		t.Fatalf("& should normalize to 'and', got %v", m)
	}
}

// AniList carries duplicate same-format entries for one work (re-releases,
// special-chapter collections: "10DANCE" twice, "Accel World" +
// "Accel World."). A same-format tie resolves to the dominant entry only when
// its popularity dwarfs every other contender; near-peers stay ambiguous.
func TestPickConfidentMatchPopularityDominance(t *testing.T) {
	if m := pickConfidentMatch("10 Dance", []aniListMedia{
		medPop(20, "10DANCE", "MANGA", 9000),
		medPop(21, "10DANCE", "MANGA", 150),
	}); m == nil || m.ID != 20 {
		t.Fatalf("dominant popularity should break the tie, got %v", m)
	}
	// near-peers: no dominance, stays no-match
	if m := pickConfidentMatch("Berserk", []aniListMedia{
		medPop(22, "Berserk", "MANGA", 9000),
		medPop(23, "Berserk", "MANGA", 4000),
	}); m != nil {
		t.Fatalf("near-peer popularity must stay ambiguous, got %v", m)
	}
	// dominance ratio alone is not enough on tiny absolute numbers
	if m := pickConfidentMatch("Obscure", []aniListMedia{
		medPop(24, "Obscure", "MANGA", 90),
		medPop(25, "Obscure", "MANGA", 2),
	}); m != nil {
		t.Fatalf("tiny absolute popularity must stay ambiguous, got %v", m)
	}
}

// Localized folder names often drop the franchise prefix ("Gurren Lagann" for
// "Tengen Toppa Gurren Lagann"). A unique candidate whose normalized title
// ends with the query, with the query covering at least half the title,
// counts as a title match; multiple suffix matches stay ambiguous.
func TestPickConfidentMatchSuffix(t *testing.T) {
	cands := []aniListMedia{
		med(30, "Tengen Toppa Gurren Lagann", "", "MANGA"),
		med(31, "Tengen Toppa Gurren Lagann: Rasen Shounentan", "", "MANGA"),
	}
	if m := pickConfidentMatch("Gurren Lagann", cands); m == nil || m.ID != 30 {
		t.Fatalf("unique suffix match expected id 30, got %v", m)
	}
	// query too short relative to the title: not confident
	if m := pickConfidentMatch("Lagann", cands); m != nil {
		t.Fatalf("short suffix must stay no-match, got %v", m)
	}
	// suffix shared by two candidates: ambiguous
	if m := pickConfidentMatch("Gurren Lagann", []aniListMedia{
		med(32, "Tengen Toppa Gurren Lagann", "", "MANGA"),
		med(33, "Chougattai Gurren Lagann", "", "MANGA"),
	}); m != nil {
		t.Fatalf("shared suffix must stay ambiguous, got %v", m)
	}
	// exact matches always win before suffix matches are considered
	if m := pickConfidentMatch("Gurren Lagann", []aniListMedia{
		med(34, "Gurren Lagann", "", "MANGA"),
		med(35, "Tengen Toppa Gurren Lagann", "", "MANGA"),
	}); m == nil || m.ID != 34 {
		t.Fatalf("exact match must beat suffix match, got %v", m)
	}
}

// Part numbering is release metadata: a candidate matches when query and
// title are equal once "part <n>" runs are removed from both sides
// ("JoJo's Bizarre Adventure - Battle Tendency" matches
// "JoJo's Bizarre Adventure Part 2: Battle Tendency").
func TestPickConfidentMatchPartNumberBlind(t *testing.T) {
	cands := []aniListMedia{
		med(40, "JoJo no Kimyou na Bouken Part 2", "JoJo's Bizarre Adventure Part 2: Battle Tendency", "MANGA"),
		med(41, "JoJo no Kimyou na Bouken Part 3", "JoJo's Bizarre Adventure Part 3: Stardust Crusaders", "MANGA"),
	}
	if m := pickConfidentMatch("JoJo's Bizarre Adventure - Battle Tendency", cands); m == nil || m.ID != 40 {
		t.Fatalf("part-blind match expected id 40, got %v", m)
	}
	// part-blind equality colliding on two candidates stays ambiguous
	if m := pickConfidentMatch("Title X", []aniListMedia{
		med(42, "Title Part 1 X", "", "MANGA"),
		med(43, "Title Part 2 X", "", "MANGA"),
	}); m != nil {
		t.Fatalf("part-blind tie must stay ambiguous, got %v", m)
	}
}

// The part-blind tier must not match a query naming one part to a candidate
// naming a different part. Uses subtitle-free "Part N" titles so the match
// actually flows through the part-blind tier (where both sides strip to the
// same stem) rather than the exact tier.
func TestPickConfidentMatchPartBlindRejectsDifferentPart(t *testing.T) {
	cands := []aniListMedia{med(50, "Franchise Part 7", "Franchise Part 7", "MANGA")}
	if m := pickConfidentMatch("Franchise Part 3", cands); m != nil {
		t.Fatalf("Part 3 query must not match a Part 7 candidate, got %v", m)
	}
	if m := pickConfidentMatch("Franchise Part 7", cands); m == nil || m.ID != 50 {
		t.Fatalf("matching part should still match, got %v", m)
	}
}
