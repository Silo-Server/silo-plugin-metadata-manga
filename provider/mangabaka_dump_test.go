package provider

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
