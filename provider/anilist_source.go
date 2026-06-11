package provider

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

const aniListProviderID = "anilist"

const (
	// fetchCacheTTL only has to cover the host's search → GetMetadata gap
	// within one enrichment attempt; generous headroom is still cheap.
	fetchCacheTTL = 10 * time.Minute
	fetchCacheMax = 512
)

type cachedMedia struct {
	media   aniListMedia
	expires time.Time
}

// AniListSource is a metadata.Source backed by the AniList GraphQL API. It
// searches the MANGA collection, confirms a confident title match, and maps the
// AniList media record into a metadata.Match.
type AniListSource struct {
	client        *http.Client
	endpoint      string
	preferredLang string

	// recent caches media records returned by Search so the host's immediate
	// GetMetadata for the matched ID does not spend a second rate-limited
	// AniList request on data the search response already carried.
	mu     sync.Mutex
	recent map[int]cachedMedia
}

// NewAniListSource builds an AniListSource. preferredLang is derived from
// Options.DefaultRegion: "english" selects the English title, anything else
// (default) prefers the romaji title.
func NewAniListSource(opts Options) *AniListSource {
	lang := "romaji"
	if strings.EqualFold(strings.TrimSpace(opts.DefaultRegion), "english") {
		lang = "english"
	}
	return &AniListSource{
		client:        &http.Client{Timeout: 15 * time.Second},
		endpoint:      aniListEndpoint,
		preferredLang: lang,
		recent:        make(map[int]cachedMedia),
	}
}

func (s *AniListSource) cachePut(m *aniListMedia) {
	if m == nil || m.ID == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recent) >= fetchCacheMax {
		for id, entry := range s.recent {
			if now.After(entry.expires) {
				delete(s.recent, id)
			}
		}
		if len(s.recent) >= fetchCacheMax {
			s.recent = make(map[int]cachedMedia)
		}
	}
	s.recent[m.ID] = cachedMedia{media: *m, expires: now.Add(fetchCacheTTL)}
}

func (s *AniListSource) cacheGet(id int) *aniListMedia {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recent[id]
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	media := entry.media
	return &media
}

func (s *AniListSource) ID() string { return aniListProviderID }

// searchTermReplacer normalizes typographic punctuation that AniList's search
// treats literally: a curly apostrophe in the query yields zero results for
// titles it would otherwise match exactly.
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
	" ", " ", // NBSP
)

func sanitizeSearchTerm(s string) string {
	return strings.TrimSpace(searchTermReplacer.Replace(s))
}

// Release-junk suffixes that make AniList searches miss: trailing publisher /
// edition bracket groups ("[Yen Press]", "(LINE Webtoon)"), volume ranges
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
// clean title costs exactly one AniList request.
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

func (s *AniListSource) Search(ctx context.Context, q metadata.SearchQuery) ([]metadata.Match, error) {
	for _, term := range searchTermVariants(q.Title) {
		media, err := searchAniList(ctx, s.client, s.endpoint, term)
		if err != nil {
			return nil, err
		}
		// Each variant is matched against the term that was actually searched,
		// keeping the exact-after-normalize confidence bar per attempt.
		best := pickConfidentMatch(term, media)
		if best == nil {
			continue
		}
		s.cachePut(best)
		return []metadata.Match{s.toMatch(best)}, nil
	}
	// No confident match is not an error; surface an empty result set.
	return nil, nil
}

func (s *AniListSource) Fetch(ctx context.Context, id string) (*metadata.Match, error) {
	numericID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(id), aniListProviderID+":"))
	parsed, err := strconv.Atoi(numericID)
	if err != nil {
		return nil, nil
	}
	if cached := s.cacheGet(parsed); cached != nil {
		match := s.toMatch(cached)
		return &match, nil
	}
	media, err := fetchAniListByID(ctx, s.client, s.endpoint, parsed)
	if err != nil {
		return nil, err
	}
	if media == nil {
		return nil, nil
	}
	s.cachePut(media)
	match := s.toMatch(media)
	return &match, nil
}

var (
	brTagPattern        = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagPattern      = regexp.MustCompile(`<[^>]+>`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription flattens the HTML AniList embeds in descriptions even when
// asked for plain text (asHtml:false still carries <i>, <br> and entities):
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

// toMatch maps an AniList media record into a metadata.Match. Story/art staff
// are both folded into Authors (the only people representation on the Match).
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

func (s *AniListSource) toMatch(m *aniListMedia) metadata.Match {
	return metadata.Match{
		Provider:    aniListProviderID,
		ProviderID:  strconv.Itoa(m.ID),
		Title:       s.preferredTitle(m),
		Description: cleanDescription(m.Description),
		CoverURL:    coverURL(m),
		BannerURL:   strings.TrimSpace(m.BannerImage),
		Status:      mangaStatusLabel(m.Status),
		Genres:      m.Genres,
		Authors:     authors(m),
		PublishYear: m.StartDate.Year,
	}
}

func (s *AniListSource) preferredTitle(m *aniListMedia) string {
	romaji := strings.TrimSpace(m.Title.Romaji)
	english := strings.TrimSpace(m.Title.English)
	native := strings.TrimSpace(m.Title.Native)

	order := []string{romaji, english, native}
	if s.preferredLang == "english" {
		order = []string{english, romaji, native}
	}
	for _, t := range order {
		if t != "" {
			return t
		}
	}
	return ""
}

func coverURL(m *aniListMedia) string {
	if xl := strings.TrimSpace(m.CoverImage.ExtraLarge); xl != "" {
		return xl
	}
	return strings.TrimSpace(m.CoverImage.Large)
}

// authors collects the names of staff whose role denotes story or art credit.
// metadata.Match has no separate artist field, so both kinds are returned in a
// single ordered slice (story credits first), de-duplicated by name.
func authors(m *aniListMedia) []string {
	var stories, arts []string
	seen := map[string]bool{}
	add := func(dst *[]string, name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		*dst = append(*dst, name)
	}
	for _, edge := range m.Staff.Edges {
		role := strings.ToLower(edge.Role)
		switch {
		case strings.Contains(role, "story"):
			add(&stories, edge.Node.Name.Full)
		case strings.Contains(role, "art"):
			add(&arts, edge.Node.Name.Full)
		}
	}
	if len(stories) == 0 && len(arts) == 0 {
		return nil
	}
	return append(stories, arts...)
}
