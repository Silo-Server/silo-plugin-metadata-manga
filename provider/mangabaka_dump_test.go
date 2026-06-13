package provider

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestResolveDumpDirPrefersConfigPath(t *testing.T) {
	got, err := resolveDumpDir("/custom/dump/path")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/custom/dump/path" {
		t.Fatalf("dir = %q, want /custom/dump/path", got)
	}
}

func TestResolveDumpDirDefaultsUnderCache(t *testing.T) {
	got, err := resolveDumpDir("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(got) != "silo-manga-metadata" {
		t.Fatalf("default dir = %q, want .../silo-manga-metadata", got)
	}
}

func TestDownloadAndInstallAtomic(t *testing.T) {
	// A tiny JSONL "dump" compressed with zstd, served over HTTP.
	raw := []byte(`{"id":1,"title":"Naruto","type":"manga"}` + "\n")
	var zbuf bytes.Buffer
	enc, _ := zstd.NewWriter(&zbuf)
	_, _ = enc.Write(raw)
	_ = enc.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zbuf.Bytes())
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := dir + "/series.jsonl"

	if err := downloadAndDecompress(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("decompressed = %q, want %q", got, raw)
	}
	// No leftover temp file alongside the destination.
	if _, err := os.Stat(dest + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file not cleaned up")
	}
}

func TestBuildIndexAndLookup(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := dir + "/series.jsonl"
	dbPath := dir + "/index.sqlite"

	jsonl := `{"id":1677,"title":"Chainsaw Man","type":"manga","secondary_titles":{"en":[{"type":"alternative","title":"Chain Saw Man"}]},"cover":{"raw":{"url":"https://img/cs.png"}},"source":{"anilist":{"id":105778}}}
{"id":2,"title":"Naruto","type":"manga"}
`
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	idx, err := buildDumpIndex(context.Background(), jsonlPath, dbPath)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	defer idx.close()

	got, err := idx.lookup(context.Background(), "Chainsaw Man")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) == 0 || got[0].ID != 1677 {
		t.Fatalf("primary lookup = %+v", got)
	}

	got2, err := idx.lookup(context.Background(), "Chain Saw Man")
	if err != nil {
		t.Fatalf("lookup secondary: %v", err)
	}
	if len(got2) == 0 || got2[0].ID != 1677 {
		t.Fatalf("secondary lookup = %+v", got2)
	}

	none, err := idx.lookup(context.Background(), "Does Not Exist")
	if err != nil {
		t.Fatalf("lookup miss: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no rows, got %+v", none)
	}
}

func TestDumpBackendReadyAfterEnsure(t *testing.T) {
	dir := t.TempDir()
	// Pre-place a built index so ensure() does not need the network.
	jsonlPath := dir + "/series.jsonl"
	if err := os.WriteFile(jsonlPath, []byte(`{"id":1,"title":"Naruto","type":"manga"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	idx, err := buildDumpIndex(context.Background(), jsonlPath, dir+"/index.sqlite")
	if err != nil {
		t.Fatalf("seed index: %v", err)
	}
	_ = idx.close()

	b := newDumpBackend(dir, 168)
	if b.ready() {
		t.Fatalf("backend should not be ready before openExisting")
	}
	if !b.openExisting() {
		t.Fatalf("openExisting should load the seeded index")
	}
	if !b.ready() {
		t.Fatalf("backend should be ready after openExisting")
	}

	got, err := b.search(context.Background(), "Naruto")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("search = %+v", got)
	}
}

func TestIsStale(t *testing.T) {
	if !isStale(time.Now().Add(-200*time.Hour), 168) {
		t.Fatalf("200h-old dump should be stale at 168h threshold")
	}
	if isStale(time.Now().Add(-10*time.Hour), 168) {
		t.Fatalf("10h-old dump should be fresh")
	}
}

func TestLookupPartBlindCandidate(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := dir + "/series.jsonl"
	dbPath := dir + "/index.sqlite"
	// Stored title has an explicit "Part 2"; a base-title query must still
	// surface it as a candidate via the part-blind index (the strict matcher
	// applies the final confidence bar downstream).
	jsonl := `{"id":7,"title":"JoJo's Bizarre Adventure Part 2","type":"manga"}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	idx, err := buildDumpIndex(context.Background(), jsonlPath, dbPath)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer idx.close()
	got, err := idx.lookup(context.Background(), "JoJo's Bizarre Adventure")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) == 0 || got[0].ID != 7 {
		t.Fatalf("part-blind candidate not found: %+v", got)
	}
}
