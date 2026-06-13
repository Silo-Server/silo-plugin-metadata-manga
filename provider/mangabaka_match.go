package provider

import "strings"

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
		matched := false
		for _, title := range mangaBakaTitleValues(*c) {
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
		if len(want) >= suffixMinQueryLen {
			for _, title := range mangaBakaTitleValues(*c) {
				normalized := normalizeTitle(title)
				if strings.HasSuffix(normalized, want) && len(want)*2 >= len(normalized) {
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
