package provider

import (
	"strings"
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
// confidence bar used for the other sources, matched across every localized and
// secondary title, with novel records excluded.
//
// When an exact tier still has more than one contender, a conservative
// chapter-count dominance tie-break resolves the clearly-safe ambiguities (a
// real serialization vs a same-titled one-shot/side-story); two genuine works
// of comparable length stay a no match rather than a guess.
//
// Tiers, strongest first:
//  1. exact: a normalized title equals the query (incl. part-blind equality);
//  2. suffix: a unique title ends with the query and the query covers at least
//     half of it (localized folder names dropping a franchise prefix).
func pickConfidentMangaBakaMatch(query string, candidates []mangaBakaSeries) *mangaBakaSeries {
	return pickConfidentMatch(query, matchConfig[mangaBakaSeries]{
		candidates:     candidates,
		titlesOf:       func(c *mangaBakaSeries) []string { return mangaBakaTitleValues(*c) },
		enablePrefix:   false,
		accept:         func(c *mangaBakaSeries) bool { return acceptMangaBakaType(c.Type) },
		dominanceScore: func(c *mangaBakaSeries) int { return mangaBakaChapterCount(*c) },
	})
}
