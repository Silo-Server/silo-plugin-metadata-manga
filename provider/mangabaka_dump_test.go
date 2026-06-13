package provider

import (
	"path/filepath"
	"testing"
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
