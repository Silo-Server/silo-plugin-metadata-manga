package provider

import (
	"os"
	"path/filepath"
	"strings"
)

const dumpDirName = "silo-manga-metadata"

// resolveDumpDir picks where the local dump lives. An explicit config path wins.
// Otherwise it defaults to <user cache dir>/silo-manga-metadata, which survives
// plugin upgrades (the install dir is wiped on upgrade). If no cache dir
// resolves (e.g. HOME unset in a container), it falls back to the OS temp dir.
func resolveDumpDir(configPath string) (string, error) {
	if p := strings.TrimSpace(configPath); p != "" {
		return p, nil
	}
	if cache, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cache) != "" {
		return filepath.Join(cache, dumpDirName), nil
	}
	return filepath.Join(os.TempDir(), dumpDirName), nil
}
