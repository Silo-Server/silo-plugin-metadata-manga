package provider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

const (
	// providerTimeout bounds one Search/Fetch call. It must absorb the worst
	// realistic AniList path: up to three variant searches (original /
	// scrubbed / subtitle-stripped), each paying a limiter queue wait behind
	// concurrent workers (~8s), plus a 429 Retry-After sleep (capped at 10s)
	// with a second queue wait for the retry token. The original 10s budget
	// made every 429 — and many plain queue waits — fail with context
	// deadline exceeded.
	providerTimeout = 45 * time.Second
)

type Source interface {
	ID() string
	Search(context.Context, metadata.SearchQuery) ([]metadata.Match, error)
	Fetch(context.Context, string) (*metadata.Match, error)
}

type Provider struct {
	sources []Source
	byID    map[string]Source
}

type Options struct {
	EnabledSources        []string
	DefaultRegion         string
	EnableLocalDump       bool
	DumpPath              string
	DumpRefreshHours      int
	DisableAniListBanners bool
}

func NewProvider() *Provider {
	return NewProviderWithOptions(Options{})
}

func NewProviderWithOptions(options Options) *Provider {
	return NewProviderWithSources(defaultSources(options))
}

func NewProviderWithSources(sources []Source) *Provider {
	p := &Provider{
		sources: make([]Source, 0, len(sources)),
		byID:    make(map[string]Source, len(sources)),
	}

	for _, source := range sources {
		if source == nil {
			continue
		}
		id := strings.TrimSpace(source.ID())
		if id == "" {
			continue
		}
		p.sources = append(p.sources, source)
		p.byID[id] = source
	}

	return p
}

// Search consults sources sequentially in registration order: the first
// source with a confident match wins, and later sources are only reached as
// fallbacks (MangaBaka is canonical; MangaDex covers its gaps). Each source gets
// its OWN providerTimeout budget (a child of ctx) so a throttled/slow leading
// source cannot consume the trailing source's window and starve the fallback.
func (p *Provider) Search(ctx context.Context, q metadata.SearchQuery) ([]metadata.Match, error) {
	var sourceErrs []error
	for _, source := range p.sources {
		matches, err := p.searchSource(ctx, source, q)
		if err != nil {
			log.Printf("manga-metadata: provider %s search error: %v", strings.TrimSpace(source.ID()), err)
			sourceErrs = append(sourceErrs, fmt.Errorf("%s: %w", strings.TrimSpace(source.ID()), err))
			continue
		}
		if len(matches) > 0 {
			return matches, nil
		}
	}

	// With zero matches the host cannot tell "no match" from "sources were
	// unreachable", and a no-match is recorded terminally. Surface the error
	// so transient trouble is retried instead of stamped.
	if len(sourceErrs) > 0 {
		return nil, errors.Join(sourceErrs...)
	}
	return nil, nil
}

// searchSource runs one source under its own providerTimeout budget.
func (p *Provider) searchSource(ctx context.Context, source Source, q metadata.SearchQuery) ([]metadata.Match, error) {
	sctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	return source.Search(sctx, q)
}

func (p *Provider) Fetch(ctx context.Context, q metadata.SearchQuery) (*metadata.Match, error) {
	tctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	for _, source := range p.sources {
		sourceID := strings.TrimSpace(source.ID())
		providerID := strings.TrimSpace(q.ProviderIDs[sourceID])
		if sourceID == "" || providerID == "" {
			continue
		}
		return source.Fetch(tctx, providerID)
	}

	if sourceID, providerID := metadata.ParseCapabilityProviderID(q.ProviderIDs[metadata.CapabilityID]); sourceID != "" {
		if source := p.byID[sourceID]; source != nil {
			return source.Fetch(tctx, providerID)
		}
	}

	return nil, nil
}

// defaultSources returns the metadata sources in fallback order: MangaBaka is
// the canonical source; MangaDex fills its gaps (licensed/western titles,
// long-tail series). The enabled_sources config entry filters the set when
// present.
func defaultSources(options Options) []Source {
	all := []Source{NewMangaBakaSource(options), NewMangaDexSource(options)}
	enabled := enabledSourceSet(options.EnabledSources)
	if len(enabled) == 0 {
		return all
	}
	filtered := make([]Source, 0, len(all))
	for _, source := range all {
		if enabled[strings.ToLower(strings.TrimSpace(source.ID()))] {
			filtered = append(filtered, source)
		}
	}
	if len(filtered) == 0 {
		return all
	}
	return filtered
}

func enabledSourceSet(values []string) map[string]bool {
	out := make(map[string]bool)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				out[part] = true
			}
		}
	}
	return out
}
