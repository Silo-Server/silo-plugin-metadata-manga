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
	return pickConfidentMatch(query, matchConfig[mangaBakaSeries]{
		candidates:   candidates,
		titlesOf:     func(c *mangaBakaSeries) []string { return mangaBakaTitleValues(*c) },
		enablePrefix: false,
		accept:       func(c *mangaBakaSeries) bool { return acceptMangaBakaType(c.Type) },
	})
}
