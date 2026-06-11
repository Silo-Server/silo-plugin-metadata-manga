# silo-plugin-manga-metadata

A [Silo](https://github.com/Silo-Server/silo-server) metadata provider plugin
for **manga** libraries. It enriches `type='manga'` series with cover art,
synopsis, genres, author/artist credits, publication year, a hero banner, and
publication status by matching against [AniList](https://anilist.co) and, as a
fallback, [MangaDex](https://mangadex.org).

## Capability

- Implements `metadata_provider.v1`, capability id `manga-metadata`.
- Manifest declares `default_priority { manga: 1 }`, so it auto-enables for
  manga libraries on install.

## Sources

Sources are consulted sequentially; the first confident match wins:

1. **AniList** (canonical) — GraphQL, no API key. Cover (extra-large),
   description (HTML-stripped), genres, story/art staff → authors, start year,
   `bannerImage` → backdrop, and status (`RELEASING`/`FINISHED`/… normalized to
   Ongoing/Completed/Hiatus/Cancelled/Upcoming).
2. **MangaDex** (fallback) — REST, no API key. Only consulted when AniList has
   no confident match (long-tail manga/manhwa/webtoons, licensed/western
   titles). Covers via the MangaDex CDN, genre-group tags, author+artist.

### Matching

Matching is deliberately strict to avoid wrong covers: exact-after-normalize on
every localized title and alternate title, with tie-break tiers (unique
MANGA-format over pilot one-shots, then popularity dominance), plus
part-blind, prefix, and suffix tiers for franchise/edition naming. Ambiguous
ties resolve to **no match** rather than guessing. The outbound search term is
sanitized (typographic punctuation) and tried in scrubbed variants
(release-group/edition junk, per-volume subtitle folders).

Each source is rate-limited independently (AniList ≈28 req/min to stay under
its degraded-mode ceiling; MangaDex ≈1 req/s) with 429 retry, and a matched
record is cached so the host's follow-up `GetMetadata` does not spend a second
request.

## Configuration

| Key               | Values                         | Default              |
|-------------------|--------------------------------|----------------------|
| `enabled_sources` | comma list of `anilist`,`mangadex` | both             |
| `default_region`  | `english` \| anything else     | romaji-preferred     |

## Build

```sh
make build        # → ./plugin (host-installable binary, version baked from git)
make test
make build-all    # cross-compile linux/amd64, linux/arm64, darwin/arm64 → dist/
```

## License

See [LICENSE](LICENSE).
