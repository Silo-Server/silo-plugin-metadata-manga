package provider

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

type cachedMangaBakaSeries struct {
	series  mangaBakaSeries
	expires time.Time
}

// mangaBakaBackend is the lookup surface shared by the live REST backend and
// the offline dump backend. MangaBakaSource picks between them per call.
type mangaBakaBackend interface {
	ready() bool
	search(ctx context.Context, term string) ([]mangaBakaSeries, error)
	fetch(ctx context.Context, id string) (*mangaBakaSeries, error)
}

// bannerFunc fetches an AniList banner by numeric id. It is injected so the
// source can be tested without HTTP and so enrichment can be disabled (nil).
type bannerFunc func(ctx context.Context, anilistID int) (string, error)

// lifecycleBackend is implemented by backends that own a cancellable background
// lifecycle (the dump backend). It is kept separate from mangaBakaBackend so the
// query-only live and stub backends are unaffected.
type lifecycleBackend interface {
	start()
	stop()
}

// MangaBakaSource is the canonical manga metadata source. It prefers the local
// dump backend when ready (offline, no rate limit) and otherwise uses the live
// REST backend (also the cold-start path while a freshly enabled dump builds).
type MangaBakaSource struct {
	dump   mangaBakaBackend
	live   mangaBakaBackend
	banner bannerFunc

	mu     sync.Mutex
	recent map[string]cachedMangaBakaSeries
}

func newMangaBakaSourceWithBackends(dump, live mangaBakaBackend, banner bannerFunc) *MangaBakaSource {
	return &MangaBakaSource{
		dump:   dump,
		live:   live,
		banner: banner,
		recent: make(map[string]cachedMangaBakaSeries),
	}
}

func (s *MangaBakaSource) cachePut(series mangaBakaSeries) {
	id := strconv.Itoa(series.ID)
	if id == "" || id == "0" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recent) >= fetchCacheMax {
		for k, entry := range s.recent {
			if now.After(entry.expires) {
				delete(s.recent, k)
			}
		}
		if len(s.recent) >= fetchCacheMax {
			s.recent = make(map[string]cachedMangaBakaSeries)
		}
	}
	s.recent[id] = cachedMangaBakaSeries{series: series, expires: now.Add(fetchCacheTTL)}
}

func (s *MangaBakaSource) cacheGet(id string) (mangaBakaSeries, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recent[id]
	if !ok || time.Now().After(entry.expires) {
		return mangaBakaSeries{}, false
	}
	return entry.series, true
}

func (s *MangaBakaSource) ID() string { return mangaBakaProviderID }

// Start begins the dump backend's background lifecycle (download + periodic
// refresh). It is a no-op when the dump backend is disabled. Lifecycle is owned
// by the caller (main.go) so a reconfigure can cleanly stop the old source.
func (s *MangaBakaSource) Start() {
	if lc, ok := s.dump.(lifecycleBackend); ok {
		lc.start()
	}
}

// Close stops the dump backend's background lifecycle, cancelling any in-flight
// download. It is a no-op when the dump backend is disabled.
func (s *MangaBakaSource) Close() error {
	if lc, ok := s.dump.(lifecycleBackend); ok {
		lc.stop()
	}
	return nil
}

// backend returns the dump backend when it is ready, else the live backend.
func (s *MangaBakaSource) backend() mangaBakaBackend {
	if s.dump != nil && s.dump.ready() {
		return s.dump
	}
	return s.live
}

func (s *MangaBakaSource) Search(ctx context.Context, q metadata.SearchQuery) ([]metadata.Match, error) {
	backend := s.backend()
	if backend == nil {
		return nil, nil
	}
	for _, term := range searchTermVariants(q.Title) {
		candidates, err := backend.search(ctx, term)
		if err != nil {
			return nil, err
		}
		best := pickConfidentMangaBakaMatch(term, candidates)
		if best == nil {
			continue
		}
		s.cachePut(*best)
		match := toMatchFromMangaBaka(*best)
		s.enrichBanner(ctx, *best, &match)
		return []metadata.Match{match}, nil
	}
	return nil, nil
}

func (s *MangaBakaSource) Fetch(ctx context.Context, id string) (*metadata.Match, error) {
	id = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(id), mangaBakaProviderID+":"))
	if series, ok := s.cacheGet(id); ok {
		match := toMatchFromMangaBaka(series)
		s.enrichBanner(ctx, series, &match)
		return &match, nil
	}
	backend := s.backend()
	if backend == nil {
		return nil, nil
	}
	series, err := backend.fetch(ctx, id)
	if err != nil {
		return nil, err
	}
	if series == nil {
		return nil, nil
	}
	match := toMatchFromMangaBaka(*series)
	s.enrichBanner(ctx, *series, &match)
	return &match, nil
}

// enrichBanner fills BannerURL from AniList when a banner func is configured
// and the record carries an anilist id. Failure is non-fatal: a cover-only
// result still returns.
func (s *MangaBakaSource) enrichBanner(ctx context.Context, series mangaBakaSeries, match *metadata.Match) {
	if s.banner == nil {
		return
	}
	ref, ok := series.Source["anilist"]
	if !ok {
		return
	}
	id, err := strconv.Atoi(ref.idString())
	if err != nil || id <= 0 {
		return
	}
	if url, err := s.banner(ctx, id); err == nil && url != "" {
		match.BannerURL = url
	}
}

// NewMangaBakaSource builds the production source. The dump backend struct is
// created only when opts.EnableLocalDump is set; the constructor does NO I/O and
// launches NO goroutine — the caller drives the lifecycle via Start()/Close() so
// a filtered-out source never downloads and a reconfigure cancels the old one.
// Until the dump is ready the source serves from the live backend. Banner
// enrichment is enabled unless opts.DisableAniListBanners is set.
func NewMangaBakaSource(opts Options) *MangaBakaSource {
	live := newLiveBackend()

	var dump mangaBakaBackend
	if opts.EnableLocalDump {
		dir, err := resolveDumpDir(opts.DumpPath)
		if err == nil {
			dump = newDumpBackend(dir, opts.DumpRefreshHours)
		}
	}

	var banner bannerFunc
	if !opts.DisableAniListBanners {
		banner = newAniListBannerEnricher().banner
	}
	return newMangaBakaSourceWithBackends(dump, live, banner)
}
