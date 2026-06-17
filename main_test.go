package main

import (
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func configEntry(key, value string) *pluginv1.ConfigEntry {
	s, _ := structpb.NewStruct(map[string]any{"value": value})
	return &pluginv1.ConfigEntry{Key: key, Value: s}
}

func typedEntry(key string, value any) *pluginv1.ConfigEntry {
	s, _ := structpb.NewStruct(map[string]any{"value": value})
	return &pluginv1.ConfigEntry{Key: key, Value: s}
}

// SWITCH/NUMBER controls deliver typed bool/number values (not strings).
func TestProviderOptionsFromConfigParsesTypedDumpKeys(t *testing.T) {
	opts := providerOptionsFromConfig([]*pluginv1.ConfigEntry{
		typedEntry("enable_local_dump", true),
		typedEntry("dump_path", "/mnt/dump"),
		typedEntry("dump_refresh_hours", float64(72)),
		typedEntry("enable_anilist_banners", false),
	})
	if !opts.EnableLocalDump {
		t.Fatalf("EnableLocalDump = false, want true")
	}
	if opts.DumpPath != "/mnt/dump" {
		t.Fatalf("DumpPath = %q", opts.DumpPath)
	}
	if opts.DumpRefreshHours != 72 {
		t.Fatalf("DumpRefreshHours = %d, want 72", opts.DumpRefreshHours)
	}
	if !opts.DisableAniListBanners {
		t.Fatalf("DisableAniListBanners = false, want true (banners disabled)")
	}
}

// String forms must still parse (defensive: hand-set values, older configs).
func TestProviderOptionsFromConfigParsesStringDumpKeys(t *testing.T) {
	opts := providerOptionsFromConfig([]*pluginv1.ConfigEntry{
		configEntry("enable_local_dump", "true"),
		configEntry("dump_refresh_hours", "72"),
	})
	if !opts.EnableLocalDump {
		t.Fatalf("EnableLocalDump = false, want true (string form)")
	}
	if opts.DumpRefreshHours != 72 {
		t.Fatalf("DumpRefreshHours = %d, want 72 (string form)", opts.DumpRefreshHours)
	}
}

func TestProviderOptionsFromConfigDefaults(t *testing.T) {
	opts := providerOptionsFromConfig(nil)
	if opts.EnableLocalDump {
		t.Fatalf("dump should default off")
	}
	if opts.DisableAniListBanners {
		t.Fatalf("banners should default on")
	}
}
