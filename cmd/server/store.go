package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite, keeps CGO_ENABLED=0
)

// store is the per-record home of the tracker data. It replaces the opaque
// gzip blob: one row per record, keyed by (sync key, collection, record id),
// with the client's last-modified timestamp. A change is a single UPSERT and a
// pull is `updated > since`, so syncs are small and SQLite is the authority.
//
// The row layout is deliberately generic — it mirrors the collections and the
// per-record merge the browser already does, so nothing is lost in translation
// and the MCP tools can keep working on plain JSON records.
type record struct {
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Data       json.RawMessage `json:"data,omitempty"`
	Updated    int64           `json:"updated"`
	Deleted    bool            `json:"deleted,omitempty"`
}

type store struct {
	db *sql.DB
	mu sync.Mutex // one writer at a time; reads are cheap and serialised too
}

func openStore(path string) (*store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection is plenty for a LAN tracker and sidesteps write
	// contention entirely.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	st := &store{db: db}
	if err := st.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS records (
			key        TEXT    NOT NULL,
			collection TEXT    NOT NULL,
			rec_id     TEXT    NOT NULL,
			data       TEXT    NOT NULL,
			updated    INTEGER NOT NULL,
			deleted    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (key, collection, rec_id)
		) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS records_updated ON records(key, updated);
		-- One row per sync key that has already been imported from a legacy
		-- blob file, so the migration runs exactly once per key.
		CREATE TABLE IF NOT EXISTS imports (
			key         TEXT PRIMARY KEY,
			imported_at INTEGER NOT NULL
		);
	`)
	return err
}

const recordColumns = `collection, rec_id, data, updated, deleted`

// upsert applies records with last-write-wins, the same rule the browser uses:
// newer timestamp wins, and at equal timestamps a tombstone beats a record.
// It returns only the rows that actually changed, ready to broadcast over SSE.
func (s *store) upsert(key string, recs []record) ([]record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var changed []record
	for _, rec := range recs {
		if rec.Collection == "" || rec.ID == "" {
			continue
		}
		var curUpdated int64
		var curDeleted int
		err := tx.QueryRow(
			`SELECT updated, deleted FROM records WHERE key=? AND collection=? AND rec_id=?`,
			key, rec.Collection, rec.ID,
		).Scan(&curUpdated, &curDeleted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// brand new
		case err != nil:
			return nil, err
		default:
			storedDeleted := curDeleted != 0
			if rec.Updated < curUpdated {
				continue // stored copy is newer
			}
			if rec.Updated == curUpdated {
				// At equal timestamps a tombstone beats a record; between two
				// records the incoming one wins, because the client only pushes a
				// record after resolving its own merge locally.
				if !rec.Deleted && storedDeleted {
					continue
				}
			}
		}
		data := string(rec.Data)
		if data == "" {
			data = "null"
		}
		if _, err := tx.Exec(
			`INSERT INTO records (key, collection, rec_id, data, updated, deleted)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(key, collection, rec_id) DO UPDATE SET
			   data=excluded.data, updated=excluded.updated, deleted=excluded.deleted`,
			key, rec.Collection, rec.ID, data, rec.Updated, boolToInt(rec.Deleted),
		); err != nil {
			return nil, err
		}
		changed = append(changed, record{
			Collection: rec.Collection,
			ID:         rec.ID,
			Data:       json.RawMessage(data),
			Updated:    rec.Updated,
			Deleted:    rec.Deleted,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

// delta returns every record touched after `since` (tombstones included), which
// is exactly what an incremental client needs.
func (s *store) delta(key string, since int64) ([]record, error) {
	return s.scan(`SELECT `+recordColumns+` FROM records WHERE key=? AND updated>? ORDER BY updated`, key, since)
}

// all returns every record for a key, tombstones included.
func (s *store) all(key string) ([]record, error) {
	return s.scan(`SELECT `+recordColumns+` FROM records WHERE key=?`, key)
}

// live returns the non-deleted records of a collection.
func (s *store) live(key, collection string) ([]record, error) {
	return s.scan(`SELECT `+recordColumns+` FROM records WHERE key=? AND collection=? AND deleted=0`, key, collection)
}

func (s *store) get(key, collection, id string) (record, bool, error) {
	row := s.db.QueryRow(`SELECT `+recordColumns+` FROM records WHERE key=? AND collection=? AND rec_id=?`, key, collection, id)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, err
	}
	return rec, true, nil
}

func (s *store) maxUpdated(key string) (int64, error) {
	var n sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(updated) FROM records WHERE key=?`, key).Scan(&n); err != nil {
		return 0, err
	}
	if n.Valid {
		return n.Int64, nil
	}
	return 0, nil
}

// deleteKey wipes a key entirely (legacy DELETE /api/kv/{key}).
func (s *store) deleteKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM records WHERE key=?`, key); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM imports WHERE key=?`, key)
	return err
}

// imported reports whether the legacy blob for a key was already migrated.
func (s *store) imported(key string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM imports WHERE key=?`, key).Scan(&n)
	return n > 0, err
}

func (s *store) markImported(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT OR IGNORE INTO imports (key, imported_at) VALUES (?, ?)`, key, time.Now().UnixMilli())
	return err
}

// --- row scanning ------------------------------------------------------------

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(sc scanner) (record, error) {
	var rec record
	var data string
	var deleted int
	if err := sc.Scan(&rec.Collection, &rec.ID, &data, &rec.Updated, &deleted); err != nil {
		return record{}, err
	}
	rec.Data = json.RawMessage(data)
	rec.Deleted = deleted != 0
	return rec, nil
}

func (s *store) scan(query string, args ...any) ([]record, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []record{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var errBadState = errors.New("malformed state")

func unmarshalObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", errBadState, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
