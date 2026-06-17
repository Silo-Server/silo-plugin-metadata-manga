package provider

import (
	"html"
	"regexp"
	"strings"
	"time"
)

const (
	// fetchCacheTTL only has to cover the host's search → GetMetadata gap
	// within one enrichment attempt; generous headroom is still cheap. Used by
	// the by-id banner enricher's cache (anilist_banner.go).
	fetchCacheTTL = 10 * time.Minute
	fetchCacheMax = 512
)

// searchTermReplacer normalizes typographic punctuation that MangaBaka (and
// MangaDex) search treats literally: a curly apostrophe in the query yields
// zero results for titles it would otherwise match exactly.
var searchTermReplacer = strings.NewReplacer(
	"‘", "'", // ‘
	"’", "'", // ’
	"ʼ", "'", // ʼ
	"“", `"`, // “
	"”", `"`, // ”
	"„", `"`, // „
	"–", "-", // –
	"—", "-", // —
	"―", "-", // ―
	" ", " ", // NBSP
)

func sanitizeSearchTerm(s string) string {
	return strings.TrimSpace(searchTermReplacer.Replace(s))
}

// Release-junk suffixes that make searches miss: trailing publisher / edition
// bracket groups ("[Yen Press]", "(LINE Webtoon)"), volume ranges
// ("v01-27"), "Omnibus", and scanlation-style "- One-shot" / "- archived" /
// "- Manga" tails. Stripped iteratively until the term is stable.
var scrubSuffixPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\s*[\[(][^)\]]*[\])]$`),
	regexp.MustCompile(`(?i)\s+v\d+(?:\s*-\s*v?\d+)?$`),
	regexp.MustCompile(`(?i)\s+omnibus$`),
	regexp.MustCompile(`(?i)\s*[-–]\s*one[ -]?shot$`),
	regexp.MustCompile(`(?i)\s*[-–]\s*archived$`),
	regexp.MustCompile(`(?i)\s*[-–]\s*manga$`),
	regexp.MustCompile(`(?i)\s+(?:(?:the|complete|full|deluxe|perfect|collector'?s|new|special|anniversary|definitive|color|colour|\d+(?:st|nd|rd|th))\s+)*(?:edition|collection)$`),
}

var trailingSeparators = regexp.MustCompile(`[\s\-–]+$`)

func scrubSearchTerm(s string) string {
	for {
		next := s
		for _, pattern := range scrubSuffixPatterns {
			next = pattern.ReplaceAllString(next, "")
		}
		next = trailingSeparators.ReplaceAllString(next, "")
		if next == s {
			return strings.TrimSpace(s)
		}
		s = next
	}
}

// stripLastSubtitle drops the final " - " segment ("Blade of the Immortal -
// Blood of a Thousand" -> "Blade of the Immortal"). Returns "" when there is
// no subtitle or the base is too short to be a meaningful series title.
func stripLastSubtitle(s string) string {
	idx := strings.LastIndex(s, " - ")
	if idx < 0 {
		return ""
	}
	base := strings.TrimSpace(strings.TrimRight(s[:idx], "-– "))
	if len(base) < 4 {
		return ""
	}
	return base
}

// searchTermVariants returns the ordered search attempts for a series title:
// the sanitized original, the release-junk-scrubbed form, and finally the
// scrubbed form without its last subtitle segment (matches per-volume folders
// and edition suffixes to the parent series). Duplicates are collapsed, so a
// clean title costs exactly one search.
func searchTermVariants(title string) []string {
	original := sanitizeSearchTerm(title)
	variants := []string{original}
	if scrubbed := scrubSearchTerm(original); scrubbed != "" && scrubbed != original {
		variants = append(variants, scrubbed)
	}
	last := variants[len(variants)-1]
	if base := stripLastSubtitle(last); base != "" && base != last {
		variants = append(variants, base)
	}
	return variants
}

var (
	brTagPattern        = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagPattern      = regexp.MustCompile(`<[^>]+>`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription flattens HTML present in MangaBaka (and AniList) descriptions:
// <br> runs become line breaks, remaining tags are dropped, entities decode,
// and per-line whitespace is tidied.
func cleanDescription(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return strings.TrimSpace(s)
	}
	s = brTagPattern.ReplaceAllString(s, "\n")
	s = htmlTagPattern.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	s = strings.Join(lines, "\n")
	s = multiNewlinePattern.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// mangaStatusLabel normalizes provider status enums into the display forms
// the host stores ("Ongoing", "Completed", "Hiatus", "Cancelled", "Upcoming").
func mangaStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RELEASING", "ONGOING":
		return "Ongoing"
	case "FINISHED", "COMPLETED":
		return "Completed"
	case "HIATUS":
		return "Hiatus"
	case "CANCELLED":
		return "Cancelled"
	case "NOT_YET_RELEASED":
		return "Upcoming"
	default:
		return ""
	}
}
