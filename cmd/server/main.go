// Command server is the tracker's self-hosted sync backend.
//
// It is deliberately dumb. It serves the single-file app and stores opaque
// blobs on disk under /api/kv/<key>. No database, no schema, no auth, no
// knowledge of what the app stores — just "give me the bytes for this key"
// and "save these bytes under this key". The browser decides what to sync.
//
// Blobs may be gzip-compressed in transit and at rest. That is still not
// schema knowledge: the server only sniffs the gzip magic number to decide
// whether a response needs Content-Encoding, and never parses the payload.
//
// Security is the network boundary: it is meant to run on a LAN / Tailscale,
// not exposed to the internet.
package main

import (
	"bytes"
	"compress/gzip"
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
}

func main() {
	dataDir := env("DATA_DIR", "./data")
	staticDir := env("STATIC_DIR", ".")
	port := env("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	s := &server{dataDir: dataDir}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /api/kv/{key}", s.get)
	mux.HandleFunc("PUT /api/kv/{key}", s.put)
	mux.HandleFunc("DELETE /api/kv/{key}", s.del)
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
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info, err := os.Stat(s.path(key)); err == nil {
		w.Header().Set("X-Updated-At", info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")

	// The diary blob is ~320 KiB of highly repetitive JSON that gzips ~23x.
	wantGzip := acceptsGzip(r)
	storedGzip := isGzip(data)

	switch {
	case storedGzip && wantGzip:
		// Compressed at rest and the client can decode it: pass through
		// untouched, no CPU spent.
		serveBody(w, r, data, "gzip")
	case storedGzip:
		// Client cannot decode gzip (curl, old client): decompress on the way
		// out so the bytes are always plain JSON for whoever asks.
		plain, err := gunzipLimit(data, maxBlobBytes)
		if err != nil {
			http.Error(w, "corrupt blob", http.StatusInternalServerError)
			return
		}
		serveBody(w, r, plain, "")
	case wantGzip && len(data) >= minGzipBytes:
		// Stored plain (written by an older client): compress now.
		gz, err := gzipBytes(data)
		if err != nil {
			serveBody(w, r, data, "")
			return
		}
		serveBody(w, r, gz, "gzip")
	default:
		serveBody(w, r, data, "")
	}
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
	// A gzipped upload is stored as-is: the bytes stay opaque, and get() sniffs
	// the magic number to decide how to serve them. We only insist that it is
	// real gzip and that it cannot inflate past the blob limit. Content-Encoding
	// is deliberately ignored — the bytes themselves are the source of truth, so
	// a proxy that transparently decompressed the request cannot corrupt us.
	if isGzip(body) {
		if _, err := gunzipLimit(body, maxBlobBytes); err != nil {
			http.Error(w, "bad gzip body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.write(key, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) del(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !keyRE.MatchString(key) {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding")
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
