// Command server is the tracker's self-hosted sync backend.
//
// It is deliberately dumb. It serves the single-file app and stores opaque
// blobs on disk under /api/kv/<key>. No database, no schema, no auth, no
// knowledge of what the app stores — just "give me the bytes for this key"
// and "save these bytes under this key". The browser decides what to sync.
//
// Security is the network boundary: it is meant to run on a LAN / Tailscale,
// not exposed to the internet.
package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const maxBlobBytes = 8 << 20 // 8 MiB per blob — years of entries.

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
	_, _ = w.Write(data)
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

// withCORS lets the standalone (file://) copy of the app talk to the server.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
