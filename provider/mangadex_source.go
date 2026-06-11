package provider

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

const mangaDexProviderID = "mangadex"

type cachedMangaDexManga struct {
	manga   mangaDexManga
	expires time.Time
}

// MangaDexSource is a fallback metadata.Source backed by the MangaDex API.
// It runs after AniList in the provider chain and only ever answers for
// titles AniList could not confidently match (long-tail manga, manhwa,
// webtoons). The same exact-after-normalize confidence bar applies, matched
// across every localized title and alt title MangaDex carries.
type MangaDexSource struct {
	client   *http.Client
	endpoint string

	mu     sync.Mutex
	recent map[string]cachedMangaDexManga
}

func NewMangaDexSource(_ Options) *MangaDexSource {
	return &MangaDexSource{
		client:   &http.Client{Timeout: 15 * time.Second},
		endpoint: mangaDexEndpoint,
		recent:   make(map[string]cachedMangaDexManga),
	}
}

func (s *MangaDexSource) ID() string { return mangaDexProviderID }

func (s *MangaDexSource) cachePut(m *mangaDexManga) {
	if m == nil || m.ID == "" {
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
			s.recent = make(map[string]cachedMangaDexManga)
		}
	}
	s.recent[m.ID] = cachedMangaDexManga{manga: *m, expires: now.Add(fetchCacheTTL)}
}

func (s *MangaDexSource) cacheGet(id string) *mangaDexManga {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recent[id]
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	manga := entry.manga
	return &manga
}

// prefixMinQueryLen gates the prefix tier: MangaDex official English titles
// often append the full subtitle to the licensed short name ("Fake It to
// Break It! I Faked Amnesia to Break off My Engagement..."), so a library
// folder named with the licensed title is a prefix, not an equal. A prefix
// match needs a long enough query to be trustworthy.
const prefixMinQueryLen = 12

// pickConfidentMangaDexMatch applies the strict confidence bar in tiers,
// strongest first: exact title/alt-title equality (incl. part-blind), then a
// unique candidate whose title starts with the query (licensed-name prefix of
// the official long title), then a unique ends-with match (mirroring the
// AniList suffix tier). Every tier requires exactly one matching candidate —
// MangaDex search has no popularity signal to break ties, so any ambiguity
// stays nil rather than guessing.
func pickConfidentMangaDexMatch(query string, candidates []mangaDexManga) *mangaDexManga {
	want := normalizeTitle(query)
	if want == "" {
		return nil
	}
	wantPartBlind := normalizePartBlind(query)
	queryPart := partNumber(query)

	var exact, prefix, suffix []*mangaDexManga
	for i := range candidates {
		c := &candidates[i]
		matched := false
		for _, title := range c.titleValues() {
			if normalizeTitle(title) == want {
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
		for _, title := range c.titleValues() {
			normalized := normalizeTitle(title)
			if len(want) >= prefixMinQueryLen && strings.HasPrefix(normalized, want) {
				prefix = append(prefix, c)
				matched = true
				break
			}
			if len(want) >= suffixMinQueryLen &&
				strings.HasSuffix(normalized, want) && len(want)*2 >= len(normalized) {
				suffix = append(suffix, c)
				matched = true
				break
			}
		}
	}

	for _, tier := range [][]*mangaDexManga{exact, prefix, suffix} {
		if len(tier) == 1 {
			return tier[0]
		}
		if len(tier) > 1 {
			return nil
		}
	}
	return nil
}

func (s *MangaDexSource) Search(ctx context.Context, q metadata.SearchQuery) ([]metadata.Match, error) {
	for _, term := range searchTermVariants(q.Title) {
		candidates, err := searchMangaDex(ctx, s.client, s.endpoint, term)
		if err != nil {
			return nil, err
		}
		best := pickConfidentMangaDexMatch(term, candidates)
		if best == nil {
			continue
		}
		s.cachePut(best)
		return []metadata.Match{s.toMatch(best)}, nil
	}
	return nil, nil
}

func (s *MangaDexSource) Fetch(ctx context.Context, id string) (*metadata.Match, error) {
	mangaID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(id), mangaDexProviderID+":"))
	if mangaID == "" {
		return nil, nil
	}
	if cached := s.cacheGet(mangaID); cached != nil {
		match := s.toMatch(cached)
		return &match, nil
	}
	manga, err := fetchMangaDexByID(ctx, s.client, s.endpoint, mangaID)
	if err != nil {
		return nil, err
	}
	if manga == nil {
		return nil, nil
	}
	s.cachePut(manga)
	match := s.toMatch(manga)
	return &match, nil
}

// preferredMangaDexTitle picks the English title, falling back to romanized
// Japanese, then any title value.
func preferredMangaDexTitle(m *mangaDexManga) string {
	if title := strings.TrimSpace(m.Attributes.Title["en"]); title != "" {
		return title
	}
	if title := strings.TrimSpace(m.Attributes.Title["ja-ro"]); title != "" {
		return title
	}
	// Deterministic fallback: Go map iteration order is random, so pick the
	// lexicographically-first locale key rather than an arbitrary one (keeps the
	// persisted title stable across re-enrichment runs).
	keys := make([]string, 0, len(m.Attributes.Title))
	for k := range m.Attributes.Title {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if trimmed := strings.TrimSpace(m.Attributes.Title[k]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *MangaDexSource) toMatch(m *mangaDexManga) metadata.Match {
	description := strings.TrimSpace(m.Attributes.Description["en"])
	return metadata.Match{
		Provider:    mangaDexProviderID,
		ProviderID:  m.ID,
		Title:       preferredMangaDexTitle(m),
		Description: cleanDescription(description),
		CoverURL:    m.coverURL(),
		Genres:      m.genres(),
		Authors:     m.people(),
		PublishYear: m.Attributes.Year,
		Status:      mangaStatusLabel(m.Attributes.Status),
	}
}
