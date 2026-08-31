# Contributing to the Manga Metadata Plugin

The [Silo contribution guide](https://github.com/Silo-Server/.github/blob/main/CONTRIBUTING.md)
covers project-wide coordination, focused changes, evidence, AI disclosure, and
pull request expectations. Those requirements apply here; this guide adds the
plugin-specific workflow.

## Before you start

Open an [issue](https://github.com/Silo-Server/silo-plugin-metadata-manga/issues)
before adding a source or changing matching, source fallback order, dump
storage, configuration, or the advertised capability. This repository owns
manga provider behavior; plugin contracts belong in
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk), while host
metadata orchestration belongs in
[`silo-server`](https://github.com/Silo-Server/silo-server).

## Development setup

Use the Go version declared in `go.mod`. A local `go.work` may point at a sibling
SDK checkout while developing both repositories, but committed code and CI must
resolve the tagged SDK dependency with `GOWORK=off`. Never commit local dump
data, cache paths, or a local filesystem `replace` directive.

## Validate your change

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./...
gofmt -l .
```

`gofmt -l .` should print nothing. If it reports unrelated pre-existing drift,
none of the Go files touched by your change may appear in the output; do not add
to the output, and report what remains. Add focused coverage for title
normalization, ambiguity handling, source fallback, dump refreshes, and failure
isolation when those behaviors change.

The normal suite is hermetic. `TestLiveMangaBakaIntegration` is skipped unless
`MANGABAKA_LIVE=1`; run it separately for live API or banner-enrichment changes
and report its result separately. For banner-enrichment changes, confirm that
the verbose output reports a non-empty banner URL; the live test does not assert
that condition today.

```sh
MANGABAKA_LIVE=1 GOWORK=off go test ./provider -run TestLiveMangaBakaIntegration -v
```

## Open the pull request

Use a Conventional Commit title, explain any matching, storage, or upstream
service risk, and paste the actual validation results. Read the
[AI-assisted contribution policy](https://github.com/Silo-Server/silo-server/blob/main/docs/ai-contributions.md)
and include its disclosure block.
