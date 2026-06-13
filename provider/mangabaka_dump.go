package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
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

const dumpDownloadURL = "https://api.mangabaka.org/v1/database/series.jsonl.zst"

// downloadAndDecompress streams a .zst file from url, decompresses it, and
// installs it at dest atomically: it writes to dest+".tmp", fsyncs, then
// renames over dest. A partial or corrupt download therefore never replaces a
// good file. The caller is responsible for staleness decisions.
func downloadAndDecompress(ctx context.Context, url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("mangabaka dump: status %d", res.StatusCode)
	}

	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cleanup := func() { _ = out.Close(); _ = os.Remove(tmp) }

	decoder, err := zstd.NewReader(res.Body)
	if err != nil {
		cleanup()
		return err
	}
	defer decoder.Close()

	if _, err := io.Copy(out, decoder); err != nil {
		cleanup()
		return err
	}
	if err := out.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
