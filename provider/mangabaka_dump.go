package provider

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		insertKey := func(norm string) error {
			if norm == "" || seen[norm] {
				return nil
			}
			seen[norm] = true
			_, err := insTitle.ExecContext(ctx, norm, s.ID)
			return err
		}
		for _, title := range mangaBakaTitleValues(s) {
			// Index both the exact-normalized and part-blind-normalized forms so
			// the matcher's part-blind tier ("X Part 2" ≡ "X") has candidates
			// offline, matching the live backend's recall.
			if err := insertKey(normalizeTitle(title)); err != nil {
				_ = tx.Rollback()
				return err
			}
			if err := insertKey(normalizePartBlind(title)); err != nil {
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
	// Query the exact and part-blind normalized forms; the strict matcher
	// (pickConfidentMangaBakaMatch) applies the final confidence bar and
	// rejects mismatched explicit part numbers, so over-broad candidates here
	// are harmless.
	partBlind := normalizePartBlind(title)
	rows, err := i.db.QueryContext(ctx,
		`SELECT s.json FROM titles t JOIN series s ON s.id = t.series_id WHERE t.norm = ? OR t.norm = ?`, norm, partBlind)
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

const (
	dumpJSONLFile = "series.jsonl"
	dumpIndexFile = "index.sqlite"
)

// dumpBackend manages the local dump lifecycle and answers lookups offline once
// an index is loaded. It is safe for concurrent use; refreshes run in the
// background under a single-flight guard so scans keep using the current index.
type dumpBackend struct {
	dir          string
	refreshHours int

	mu         sync.RWMutex
	index      *dumpIndex
	refreshing bool
}

func newDumpBackend(dir string, refreshHours int) *dumpBackend {
	if refreshHours <= 0 {
		refreshHours = 168
	}
	return &dumpBackend{dir: dir, refreshHours: refreshHours}
}

func (b *dumpBackend) indexPath() string { return filepath.Join(b.dir, dumpIndexFile) }
func (b *dumpBackend) jsonlPath() string { return filepath.Join(b.dir, dumpJSONLFile) }

func (b *dumpBackend) ready() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.index != nil
}

// openExisting loads an already-built index from disk without any network use.
// Returns false when no usable index exists yet (cold start).
func (b *dumpBackend) openExisting() bool {
	if _, err := os.Stat(b.indexPath()); err != nil {
		return false
	}
	idx, err := openDumpIndex(b.indexPath())
	if err != nil {
		return false
	}
	b.mu.Lock()
	if b.index != nil {
		_ = b.index.close()
	}
	b.index = idx
	b.mu.Unlock()
	return true
}

// maybeRefresh downloads a new dump and rebuilds the index when the current
// index is missing or stale. It runs at most one refresh at a time; callers
// invoke it in a goroutine so scanning is never blocked.
func (b *dumpBackend) maybeRefresh(ctx context.Context) {
	b.mu.Lock()
	if b.refreshing {
		b.mu.Unlock()
		return
	}
	if info, err := os.Stat(b.indexPath()); err == nil && !isStale(info.ModTime(), b.refreshHours) {
		b.mu.Unlock()
		if !b.ready() {
			b.openExisting()
		}
		return
	}
	b.refreshing = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.refreshing = false
		b.mu.Unlock()
	}()

	if err := downloadAndDecompress(ctx, dumpDownloadURL, b.jsonlPath()); err != nil {
		log.Printf("manga-metadata: dump download failed: %v", err)
		return
	}
	idx, err := buildDumpIndex(ctx, b.jsonlPath(), b.indexPath())
	if err != nil {
		log.Printf("manga-metadata: dump index build failed: %v", err)
		return
	}
	b.mu.Lock()
	if b.index != nil {
		_ = b.index.close()
	}
	b.index = idx
	b.mu.Unlock()
	_ = os.Remove(b.jsonlPath()) // index is built; the raw JSONL is no longer needed
}

func (b *dumpBackend) search(ctx context.Context, term string) ([]mangaBakaSeries, error) {
	b.mu.RLock()
	idx := b.index
	b.mu.RUnlock()
	if idx == nil {
		return nil, nil
	}
	return idx.lookup(ctx, term)
}

// fetch resolves a series by MangaBaka id from the local index.
func (b *dumpBackend) fetch(ctx context.Context, id string) (*mangaBakaSeries, error) {
	b.mu.RLock()
	idx := b.index
	b.mu.RUnlock()
	if idx == nil {
		return nil, nil
	}
	return idx.fetchByID(ctx, id)
}

func isStale(modTime time.Time, refreshHours int) bool {
	return time.Since(modTime) > time.Duration(refreshHours)*time.Hour
}

func (i *dumpIndex) fetchByID(ctx context.Context, id string) (*mangaBakaSeries, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	row := i.db.QueryRowContext(ctx, `SELECT json FROM series WHERE id = ?`, id)
	var raw string
	switch err := row.Scan(&raw); err {
	case nil:
	case sql.ErrNoRows:
		return nil, nil
	default:
		return nil, err
	}
	var s mangaBakaSeries
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	return &s, nil
}
