// Command server is the tracker's self-hosted backend.
//
// It serves the single-file app and stores the tracker's records in SQLite
// (one row per record, synced incrementally under /api/sync/<key>), so a sync
// only moves what changed and other clients can be pushed the change over SSE
// at /api/events/<key>.
//
// For older clients it also keeps the opaque-blob API at /api/kv/<key>: a GET
// materialises the blob from the records, a PUT imports it back per record.
// That is the migration path — no client change is needed to move the data
// into SQLite. An MCP server at /mcp exposes the same records to agents.
//
// Security is the network boundary: it is meant to run on a private network / LAN,
// not exposed to the internet.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBlobBytes = 8 << 20 // 8 MiB per blob — years of entries.

// gzipMagic identifies a stored blob as gzip-compressed. Blobs are
// self-describing: the server still knows nothing about the JSON inside, it just
// sniffs these two bytes to decide whether a response needs Content-Encoding.
var gzipMagic = []byte{0x1f, 0x8b}

// minGzipBytes skips compression for small bodies (404 pages, empty blobs),
// where gzip adds framing overhead for no gain.
const minGzipBytes = 860

// Keys map 1:1 to filenames, so keep them boring (no path traversal).
var keyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type server struct {
	dataDir string
	store   *store
	hub     *hub
}

func main() {
	dataDir := env("DATA_DIR", "./data")
	staticDir := env("STATIC_DIR", ".")
	port := env("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	s := &server{dataDir: dataDir}
	st, err := openStore(filepath.Join(dataDir, "tracker.db"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	s.store = st
	s.hub = newHub()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /api/kv/{key}", s.get)
	mux.HandleFunc("PUT /api/kv/{key}", s.put)
	mux.HandleFunc("DELETE /api/kv/{key}", s.del)
	// Incremental record sync + realtime push.
	mux.HandleFunc("GET /api/sync/{key}", s.syncPull)
	mux.HandleFunc("POST /api/sync/{key}", s.syncPush)
	mux.HandleFunc("GET /api/events/{key}", s.events)
	// MCP (Streamable HTTP) so agents can inspect and extend the tracker.
	mux.HandleFunc("/mcp", s.mcpRoute)
	// Everything else is the single-file app (index.html is served for "/").
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	log.Printf("tracker listening on :%s (data=%s, static=%s)", port, dataDir, staticDir)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func (s *server) path(key string) string { return filepath.Join(s.dataDir, key) }

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	if err := s.ensureImported(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recs, err := s.store.all(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// An empty key is "no data yet": old clients treat 404 as "push mine".
	if len(recs) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	state, err := stateFromRecords(recs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if updated, err := s.store.maxUpdated(key); err == nil && updated > 0 {
		w.Header().Set("X-Updated-At", time.UnixMilli(updated).UTC().Format(time.RFC3339Nano))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")

	// The blob is built on the fly (plain); compress it for clients that ask.
	if acceptsGzip(r) && len(data) >= minGzipBytes {
		if gz, err := gzipBytes(data); err == nil {
			serveBody(w, r, gz, "gzip")
			return
		}
	}
	serveBody(w, r, data, "")
}

// serveBody writes body with an explicit Content-Length. The encoding is passed
// in by the caller because it is decided from the stored bytes, not the body.
func serveBody(w http.ResponseWriter, r *http.Request, body []byte, encoding string) {
	if encoding != "" {
		w.Header().Set("Content-Encoding", encoding)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (s *server) put(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlobBytes+1))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if len(body) > maxBlobBytes {
		http.Error(w, "blob too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Content-Encoding is deliberately ignored: the bytes are the source of
	// truth, so a proxy that decompressed the request in flight cannot corrupt
	// us.
	plain := body
	if isGzip(body) {
		plain, err = gunzipLimit(body, maxBlobBytes)
		if err != nil {
			http.Error(w, "bad gzip body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.importBlob(key, plain); err != nil {
		http.Error(w, "bad blob: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.markImported(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Keep the legacy file as a rollback backup (best effort only).
	_ = s.write(key, plain)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) del(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	if err := s.store.deleteKey(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ensureImported migrates a legacy blob file into SQLite, exactly once per key.
// A corrupt legacy file is logged and skipped rather than blocked forever.
func (s *server) ensureImported(key string) error {
	done, err := s.store.imported(key)
	if err != nil || done {
		return err
	}
	raw, err := s.readPlain(key)
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		if err := s.importBlob(key, raw); err != nil {
			log.Printf("import legacy blob %q: %v", key, err)
		}
	}
	return s.store.markImported(key)
}

// write stores body atomically (temp file + rename) so a reader never sees a
// half-written blob.
func (s *server) write(key string, body []byte) error {
	f, err := os.CreateTemp(s.dataDir, key+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path(key))
}

// isGzip reports whether b starts with the gzip magic number.
func isGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == gzipMagic[0] && b[1] == gzipMagic[1]
}

// gunzipLimit decompresses b, refusing to produce more than max bytes so a
// small upload cannot expand into an unbounded response.
func gunzipLimit(b []byte, max int) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(out) > max {
		return nil, errors.New("decompressed blob too large")
	}
	return out, nil
}

// gzipBytes compresses b at BestSpeed: this data is JSON with long runs of
// repeated keys, so the cheap setting already gets ~23x.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// acceptsGzip reports whether the client asked for gzip, honouring q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name != "gzip" && name != "*" {
			continue
		}
		q := 1.0
		for _, p := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(p)), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		return q > 0
	}
	return false
}

// withCORS lets the standalone (file://) copy of the app talk to the server.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding, Accept, MCP-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
		// autoSync reads X-Updated-At to pick last-write-wins; without this the
		// file:// copy cannot see the header and would push every time.
		w.Header().Set("Access-Control-Expose-Headers", "X-Updated-At")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
