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
CREATE TABLE IF NOT EXISTS titles (norm TEXT NOT NULL, rev TEXT NOT NULL, series_id INTEGER NOT NULL, UNIQUE(norm, series_id));
CREATE INDEX IF NOT EXISTS idx_titles_norm ON titles(norm);
CREATE INDEX IF NOT EXISTS idx_titles_rev ON titles(rev);
CREATE TABLE IF NOT EXISTS _meta (key TEXT PRIMARY KEY, value TEXT);
`

// reverseRunes returns s with its runes in reverse order. Used to store a
// reversed normalized title so a "titles ending with X" (suffix) query becomes
// an index-friendly prefix GLOB on the reversed column.
func reverseRunes(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// metaBuiltAtKey is the _meta row that records when the index was built. The
// dump backend uses this recorded build time (not the index file's mtime) to
// decide staleness, so touch/restore/remount of the file cannot fool refresh.
const metaBuiltAtKey = "built_at"

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
	// Record the build time inside the index so staleness is decided by the
	// recorded version, not the file's mtime (#11).
	if _, err := db.ExecContext(ctx,
		"INSERT OR REPLACE INTO _meta(key, value) VALUES(?, ?)",
		metaBuiltAtKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = db.Close()
		_ = os.Remove(tmpDB)
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
	// INSERT OR IGNORE relies on the UNIQUE(norm, series_id) constraint to
	// collapse duplicate (norm, id) rows (e.g. when the exact and part-blind
	// forms of a title are identical).
	insTitle, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO titles(norm, rev, series_id) VALUES(?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20) // some records are large
	// One reusable per-record dedup set, cleared (not reallocated) each record.
	seen := make(map[string]bool)
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
		clear(seen)
		insertKey := func(norm string) error {
			if norm == "" || seen[norm] {
				return nil
			}
			seen[norm] = true
			_, err := insTitle.ExecContext(ctx, norm, reverseRunes(norm), s.ID)
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

// builtAt returns the time recorded in _meta when the index was built. ok is
// false when the row is missing or unparseable, in which case callers treat the
// index as stale (forcing a safe rebuild).
func (i *dumpIndex) builtAt() (time.Time, bool) {
	if i == nil || i.db == nil {
		return time.Time{}, false
	}
	row := i.db.QueryRowContext(context.Background(),
		"SELECT value FROM _meta WHERE key = ?", metaBuiltAtKey)
	var raw string
	if err := row.Scan(&raw); err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
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
	query := `SELECT s.json FROM titles t JOIN series s ON s.id = t.series_id WHERE t.norm = ? OR t.norm = ?`
	args := []any{norm, partBlind}
	// Surface suffix candidates (titles ENDING WITH the query) via a reversed-norm
	// prefix GLOB so the matcher's suffix tier has candidates offline too. Gated by
	// suffixMinQueryLen (same threshold the matcher uses) to avoid over-broad scans.
	// normalizeTitle output is lowercase alphanumeric only, so it contains no GLOB
	// metacharacters; the trailing "*" prefix glob is safe and index-usable.
	if len([]rune(norm)) >= suffixMinQueryLen {
		query += ` OR t.rev GLOB ?`
		args = append(args, reverseRunes(norm)+"*")
	}
	rows, err := i.db.QueryContext(ctx, query, args...)
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

// dumpRetryInterval is how long the run loop waits before retrying after a
// failed or not-yet-ready refresh (cold-start download failure, build error).
// Once the index is ready it switches to the configured refreshHours cadence.
const dumpRetryInterval = 15 * time.Minute

// dumpBackend manages the local dump lifecycle and answers lookups offline once
// an index is loaded. It is safe for concurrent use. The constructor does no I/O
// and launches no goroutine; call start() to begin the background lifecycle and
// stop() to cancel it. Refreshes run in a single cancellable goroutine so scans
// keep using the current index.
type dumpBackend struct {
	dir          string
	refreshHours int

	mu     sync.RWMutex
	index  *dumpIndex
	cancel context.CancelFunc
}

func newDumpBackend(dir string, refreshHours int) *dumpBackend {
	if refreshHours <= 0 {
		refreshHours = 168
	}
	return &dumpBackend{dir: dir, refreshHours: refreshHours}
}

// start launches the background refresh loop. It is safe to call once per
// backend; a second start replaces the previous loop (the old one is cancelled).
func (b *dumpBackend) start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	b.cancel = cancel
	b.mu.Unlock()
	go b.run(ctx)
}

// stop cancels the background refresh loop. It is idempotent.
func (b *dumpBackend) stop() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	if b.index != nil {
		_ = b.index.close()
		b.index = nil
	}
	b.mu.Unlock()
}

// run is the background lifecycle loop: attempt a refresh immediately, then wait
// either the full refresh interval (once an index is ready) or the short retry
// interval (cold start not yet ready / last attempt failed), all cancellable.
func (b *dumpBackend) run(ctx context.Context) {
	for {
		b.refreshIfNeeded(ctx)

		wait := dumpRetryInterval
		if b.ready() {
			wait = time.Duration(b.refreshHours) * time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
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

// refreshIfNeeded loads any on-disk index without network, then downloads and
// rebuilds only when the loaded index is missing or stale. Staleness is decided
// by the build time recorded inside the index (#11), never the file's mtime. On
// any download/build error it logs and returns, keeping the current index in
// service; the run loop retries on its short cadence (#8).
func (b *dumpBackend) refreshIfNeeded(ctx context.Context) {
	// Load an existing on-disk index first so a fresh process can serve offline
	// without any network use, and so staleness reads the recorded build time.
	if !b.ready() {
		b.openExisting()
	}

	if !b.isStale() {
		return // current index is fresh enough; keep serving it.
	}

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

// isStale reports whether the in-memory index must be rebuilt. No index at all
// is stale; an index whose recorded built_at is older than refreshHours is
// stale; an index with a missing/unparseable built_at is treated as stale so a
// safe rebuild records a fresh marker.
func (b *dumpBackend) isStale() bool {
	b.mu.RLock()
	idx := b.index
	b.mu.RUnlock()
	if idx == nil {
		return true
	}
	builtAt, ok := idx.builtAt()
	if !ok {
		return true
	}
	return time.Since(builtAt) > time.Duration(b.refreshHours)*time.Hour
}

// search holds the read lock for the whole query so a concurrent index swap
// (which takes the write lock to close the old index) cannot close the handle
// mid-query (#3).
func (b *dumpBackend) search(ctx context.Context, term string) ([]mangaBakaSeries, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.index == nil {
		return nil, nil
	}
	return b.index.lookup(ctx, term)
}

// fetch resolves a series by MangaBaka id from the local index. Like search, it
// holds the read lock for the whole query to stay safe against index swaps (#3).
func (b *dumpBackend) fetch(ctx context.Context, id string) (*mangaBakaSeries, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.index == nil {
		return nil, nil
	}
	return b.index.fetchByID(ctx, id)
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
