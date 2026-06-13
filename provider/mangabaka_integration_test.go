package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

// TestLiveMangaBakaIntegration exercises the real MangaBaka REST API end to end:
// live search -> strict match -> field mapping -> AniList banner enrichment ->
// the search->fetch in-memory cache. It is network-gated and skipped unless
// MANGABAKA_LIVE=1 so the normal test suite stays hermetic.
func TestLiveMangaBakaIntegration(t *testing.T) {
	if os.Getenv("MANGABAKA_LIVE") != "1" {
		t.Skip("set MANGABAKA_LIVE=1 to run the live MangaBaka integration test")
	}

	src := NewMangaBakaSource(Options{}) // live backend, banners on, no dump

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Unambiguous titles that must resolve to a confident match end to end.
	titles := []string{
		"Chainsaw Man",                  // mainstream manga
		"Solo Leveling",                 // manhwa (long-tail for AniList/MangaDex)
		"The Rising of the Shield Hero", // long title / suffix case
		"Vinland Saga",                  // mainstream manga
	}

	// Known-ambiguous: MangaBaka keeps two distinct active "manga" records that
	// both normalize to "onepunchman" (the Murata redraw and the ONE webcomic),
	// so the strict matcher safely declines rather than guess. Logged, not asserted.
	if ms, err := src.Search(ctx, metadata.SearchQuery{Title: "One Punch Man"}); err == nil {
		t.Logf("ambiguity demo  One Punch Man -> %d confident match(es) (expected 0; distinct same-titled works)", len(ms))
	}

	for _, title := range titles {
		matches, err := src.Search(ctx, metadata.SearchQuery{Title: title})
		if err != nil {
			t.Errorf("%q: search error: %v", title, err)
			continue
		}
		if len(matches) == 0 {
			t.Errorf("%q: no confident match", title)
			continue
		}
		m := matches[0]
		t.Logf("%-32s -> title=%q id=%s year=%d status=%q type-mapped cover=%v banner=%v genres=%d authors=%v extIDs=%v rating=%q",
			title, m.Title, m.ProviderID, m.PublishYear, m.Status,
			m.CoverURL != "", m.BannerURL != "", len(m.Genres), m.Authors, m.ExternalIDs, m.ContentRating)

		if m.Provider != "mangabaka" {
			t.Errorf("%q: provider = %q, want mangabaka", title, m.Provider)
		}
		if m.CoverURL == "" {
			t.Errorf("%q: empty cover URL", title)
		}
		if m.ExternalIDs["anilist"] == "" && m.ExternalIDs["my_anime_list"] == "" {
			t.Errorf("%q: no cross-provider IDs mapped", title)
		}

		// Search->fetch cache: the matched id must resolve without a backend call.
		fm, err := src.Fetch(ctx, "mangabaka:"+m.ProviderID)
		if err != nil {
			t.Errorf("%q: fetch error: %v", title, err)
			continue
		}
		if fm == nil || fm.ProviderID != m.ProviderID {
			t.Errorf("%q: fetch did not return the cached match", title)
		}
	}
}
