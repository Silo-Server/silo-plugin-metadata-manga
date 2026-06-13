package provider

import (
	"strings"
	"unicode/utf8"
)

// matchConfig parameterizes the strict candidate matcher across metadata
// sources. It captures the only things that differ between sources: the
// candidate type, how titles are extracted from a candidate, whether the
// prefix tier is enabled, and an optional accept filter applied before
// matching.
type matchConfig[T any] struct {
	candidates   []T
	titlesOf     func(*T) []string
	enablePrefix bool
	// accept, when non-nil, pre-filters candidates: any candidate for which
	// accept returns false is skipped entirely (e.g. MangaBaka novel/type
	// exclusion). nil accepts every candidate.
	accept func(*T) bool
}

// pickConfidentMatch applies the strict, normalize-then-exact confidence bar in
// tiers, strongest first:
//  1. exact: a normalized title equals the query (incl. part-blind equality,
//     with different-explicit-part records rejected);
//  2. prefix (only when cfg.enablePrefix): a title starts with the query and
//     the query is at least prefixMinQueryLen long (licensed-name prefix of an
//     official long title);
//  3. suffix: a title ends with the query and the query covers at least half of
//     it by rune count (localized folder names dropping a franchise prefix).
//
// It returns the unique candidate of the strongest non-empty tier; a tier with
// more than one contender is genuine ambiguity and resolves to nil rather than
// guessing, and no match at all returns nil.
func pickConfidentMatch[T any](query string, cfg matchConfig[T]) *T {
	want := normalizeTitle(query)
	if want == "" {
		return nil
	}
	wantPartBlind := normalizePartBlind(query)
	queryPart := partNumber(query)
	wantRunes := utf8.RuneCountInString(want)

	var exact, prefix, suffix []*T
	for i := range cfg.candidates {
		c := &cfg.candidates[i]
		if cfg.accept != nil && !cfg.accept(c) {
			continue
		}
		// Compute the candidate's title set once and reuse it across all tiers.
		titles := cfg.titlesOf(c)
		matched := false
		for _, title := range titles {
			normalized := normalizeTitle(title)
			if normalized == want {
				exact = append(exact, c)
				matched = true
				break
			}
			if normalizePartBlind(title) == wantPartBlind {
				// Don't let a part-blind match cross different explicit parts.
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
		for _, title := range titles {
			normalized := normalizeTitle(title)
			if cfg.enablePrefix && wantRunes >= prefixMinQueryLen &&
				strings.HasPrefix(normalized, want) {
				prefix = append(prefix, c)
				break
			}
			// Coverage compares rune counts (not bytes) so multi-byte (CJK)
			// prefixes do not inflate the length and wrongly reject a valid
			// suffix match (Finding A).
			if wantRunes >= suffixMinQueryLen &&
				strings.HasSuffix(normalized, want) &&
				wantRunes*2 >= utf8.RuneCountInString(normalized) {
				suffix = append(suffix, c)
				break
			}
		}
	}

	for _, tier := range [][]*T{exact, prefix, suffix} {
		if len(tier) == 1 {
			return tier[0]
		}
		if len(tier) > 1 {
			return nil
		}
	}
	return nil
}
