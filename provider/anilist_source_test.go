package provider

import "testing"

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
