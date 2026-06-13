package provider

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
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

// dumpIndex is a read handle to the local SQLite index. The schema is owned by
// this plugin (built from MangaBaka's JSONL), so it never couples to MangaBaka's
// own sqlite-utils layout. series holds the full record JSON; titles maps every
// normalized title (primary + secondary) to a series id.
type dumpIndex struct {
	db *sql.DB
}

const dumpIndexSchema = `
CREATE TABLE IF NOT EXISTS series (id INTEGER PRIMARY KEY, json TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS titles (norm TEXT NOT NULL, series_id INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_titles_norm ON titles(norm);
`

// buildDumpIndex streams the decompressed JSONL at jsonlPath and writes a fresh
// SQLite index at dbPath (atomically: build at dbPath+".tmp", then rename). It
// returns an open read handle to the installed index.
func buildDumpIndex(ctx context.Context, jsonlPath, dbPath string) (*dumpIndex, error) {
	tmpDB := dbPath + ".tmp"
	_ = os.Remove(tmpDB)

	db, err := sql.Open("sqlite", tmpDB)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, dumpIndexSchema); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := ingestJSONL(ctx, db, jsonlPath); err != nil {
		_ = db.Close()
		_ = os.Remove(tmpDB)
		return nil, err
	}
	if err := db.Close(); err != nil {
		_ = os.Remove(tmpDB)
		return nil, err
	}
	if err := os.Rename(tmpDB, dbPath); err != nil {
		_ = os.Remove(tmpDB)
		return nil, err
	}
	return openDumpIndex(dbPath)
}

func ingestJSONL(ctx context.Context, db *sql.DB, jsonlPath string) error {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return err
	}
	defer f.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	insSeries, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO series(id, json) VALUES(?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	insTitle, err := tx.PrepareContext(ctx, "INSERT INTO titles(norm, series_id) VALUES(?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20) // some records are large
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var s mangaBakaSeries
		if err := json.Unmarshal(line, &s); err != nil {
			continue // skip malformed lines rather than abort the whole import
		}
		if s.ID == 0 {
			continue
		}
		if _, err := insSeries.ExecContext(ctx, s.ID, string(line)); err != nil {
			_ = tx.Rollback()
			return err
		}
		seen := make(map[string]bool)
		for _, title := range mangaBakaTitleValues(s) {
			norm := normalizeTitle(title)
			if norm == "" || seen[norm] {
				continue
			}
			seen[norm] = true
			if _, err := insTitle.ExecContext(ctx, norm, s.ID); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func openDumpIndex(dbPath string) (*dumpIndex, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	return &dumpIndex{db: db}, nil
}

func (i *dumpIndex) close() error {
	if i == nil || i.db == nil {
		return nil
	}
	return i.db.Close()
}

// lookup returns every series whose normalized primary or secondary title
// equals the normalized query. Confidence/tie-breaking is the matcher's job
// (pickConfidentMangaBakaMatch); this only narrows the candidate set.
func (i *dumpIndex) lookup(ctx context.Context, title string) ([]mangaBakaSeries, error) {
	norm := normalizeTitle(title)
	if norm == "" {
		return nil, nil
	}
	rows, err := i.db.QueryContext(ctx,
		`SELECT s.json FROM titles t JOIN series s ON s.id = t.series_id WHERE t.norm = ?`, norm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []mangaBakaSeries
	seen := make(map[int]bool)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var s mangaBakaSeries
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			continue
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out, rows.Err()
}
