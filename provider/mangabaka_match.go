package provider

import (
	"strings"
	"unicode/utf8"
)

// acceptMangaBakaType excludes prose-novel records (which MangaBaka aggregates
// alongside comics) so a light novel sharing a title cannot match a manga
// folder. Unknown/empty types are accepted (lenient) to avoid over-rejecting.
func acceptMangaBakaType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "novel", "light_novel", "light novel":
		return false
	default:
		return true
	}
}

// pickConfidentMangaBakaMatch applies the same strict, normalize-then-exact
// confidence bar used for the other sources, matched across every localized
// and secondary title. MangaBaka de-duplicates upstream records (merged_with),
// so unlike AniList there is no popularity tie-break: any tier with more than
// one contender is genuine ambiguity and resolves to nil rather than guessing.
//
// Tiers, strongest first:
//  1. exact: a normalized title equals the query (incl. part-blind equality);
//  2. suffix: a unique title ends with the query and the query covers at least
//     half of it (localized folder names dropping a franchise prefix).
func pickConfidentMangaBakaMatch(query string, candidates []mangaBakaSeries) *mangaBakaSeries {
	want := normalizeTitle(query)
	if want == "" {
		return nil
	}
	wantPartBlind := normalizePartBlind(query)
	queryPart := partNumber(query)

	var exact, suffix []*mangaBakaSeries
	for i := range candidates {
		c := &candidates[i]
		if !acceptMangaBakaType(c.Type) {
			continue
		}
		// Compute the candidate's title set once and reuse it across both the
		// exact and suffix tiers (#15).
		titles := mangaBakaTitleValues(*c)
		matched := false
		for _, title := range titles {
			normalized := normalizeTitle(title)
			if normalized == want {
				exact = append(exact, c)
				matched = true
				break
			}
			if normalizePartBlind(title) == wantPartBlind {
				if tp := partNumber(title); queryPart != "" && tp != "" && queryPart != tp {
					continue
				}
				exact = append(exact, c)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if utf8.RuneCountInString(want) >= suffixMinQueryLen {
			for _, title := range titles {
				normalized := normalizeTitle(title)
				// Coverage compares rune counts (not bytes) so multi-byte (CJK)
				// prefixes do not inflate the length and wrongly reject a valid
				// suffix match (Finding A).
				if strings.HasSuffix(normalized, want) &&
					utf8.RuneCountInString(want)*2 >= utf8.RuneCountInString(normalized) {
					suffix = append(suffix, c)
					break
				}
			}
		}
	}

	for _, tier := range [][]*mangaBakaSeries{exact, suffix} {
		if len(tier) == 1 {
			return tier[0]
		}
		if len(tier) > 1 {
			return nil
		}
	}
	return nil
}
