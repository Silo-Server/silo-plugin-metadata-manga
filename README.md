# silo-plugin-manga-metadata

A [Silo](https://github.com/Silo-Server/silo-server) metadata provider plugin
for **manga** libraries. It enriches `type='manga'` series with cover art,
synopsis, genres, author/artist credits, publication year, a hero banner, and
publication status by matching against [MangaBaka](https://mangabaka.org) and,
as a deep fallback, [MangaDex](https://mangadex.org).

## Capability

- Implements `metadata_provider.v1`, capability id `manga-metadata`.
- Manifest declares `default_priority { manga: 1 }`, so it auto-enables for
  manga libraries on install.

## Sources

1. **MangaBaka** (canonical) — aggregates AniList, MyAnimeList, MangaUpdates,
   Kitsu, Anime-Planet, and Shikimori. Used live via its REST API, or — when
   `enable_local_dump` is on — via a locally-stored copy of its nightly
   database for offline, rate-limit-free matching. Covers come from the
   MangaBaka image CDN.
2. **MangaDex** (deep fallback) — only consulted for titles MangaBaka does not
   track (long-tail/scanlation). REST, no API key.

Hero **banners/backdrops** are fetched from AniList by id (MangaBaka records
carry the AniList id), controllable via `enable_anilist_banners`.

### Matching

Matching is deliberately strict to avoid wrong covers: exact-after-normalize
across every localized and secondary title, with a part-blind tier for
franchise/edition naming and a suffix tier for localized folder names that drop
a franchise prefix. Ambiguous ties resolve to **no match** rather than guessing.

### Configuration

| Key | Default | Purpose |
|-----|---------|---------|
| `enabled_sources` | _(all)_ | Comma-separated source filter (mangabaka, mangadex). |
| `enable_local_dump` | off | Offline dump mode (~1.5GB disk). |
| `dump_path` | OS cache dir | Override dump storage location. |
| `dump_refresh_hours` | 168 | Dump refresh interval. |
| `enable_anilist_banners` | on | AniList banner backdrops. |

## Attribution

Metadata is sourced from [MangaBaka](https://mangabaka.org). MangaBaka original
data is licensed under
[CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/);
upstream provider data is subject to each provider's own terms.

## Build

```sh
make build        # → ./plugin (host-installable binary, version baked from git)
make test
make build-all    # cross-compile linux/amd64, linux/arm64, darwin/arm64 → dist/
```

## License

See [LICENSE](LICENSE).
