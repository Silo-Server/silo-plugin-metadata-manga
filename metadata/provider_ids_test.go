package metadata

import "testing"

func TestProviderIDsFromMatchMergesExternalIDs(t *testing.T) {
	m := Match{
		Provider:   "mangabaka",
		ProviderID: "1677",
		ExternalIDs: map[string]string{
			"anilist":       "105778",
			"my_anime_list": "116778",
		},
	}
	ids := ProviderIDsFromMatch(m)
	if ids["mangabaka"] != "1677" {
		t.Fatalf("mangabaka id = %q, want 1677", ids["mangabaka"])
	}
	if ids["manga-metadata"] != "mangabaka:1677" {
		t.Fatalf("capability id = %q, want mangabaka:1677", ids["manga-metadata"])
	}
	if ids["anilist"] != "105778" || ids["my_anime_list"] != "116778" {
		t.Fatalf("external ids not merged: %v", ids)
	}
}
