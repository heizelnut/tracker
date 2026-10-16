package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(filepath.Join(dir, "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{dataDir: dir, store: st, hub: newHub()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /api/kv/{key}", s.get)
	mux.HandleFunc("PUT /api/kv/{key}", s.put)
	mux.HandleFunc("DELETE /api/kv/{key}", s.del)
	mux.HandleFunc("GET /api/sync/{key}", s.syncPull)
	mux.HandleFunc("POST /api/sync/{key}", s.syncPush)
	mux.HandleFunc("GET /api/events/{key}", s.events)
	mux.HandleFunc("/mcp", s.mcpRoute)
	srv := httptest.NewServer(withCORS(mux))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	resp, body := do(t, http.MethodGet, srv.URL+"/healthz", "")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	blob := `{"entries":[{"date":"2026-01-10","weight":72.5}],"foods":[],"diary":{}}`

	resp, _ := do(t, http.MethodPut, srv.URL+"/api/kv/default", blob)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put status = %d, want 204", resp.StatusCode)
	}

	resp2, got := do(t, http.MethodGet, srv.URL+"/api/kv/default", "")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", resp2.StatusCode)
	}
	if resp2.Header.Get("X-Updated-At") == "" {
		t.Fatal("missing X-Updated-At header")
	}
	// The blob is materialised from the store, not echoed byte-for-byte.
	var state map[string]any
	if err := json.Unmarshal([]byte(got), &state); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	entries, ok := state["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v", state["entries"])
	}
	if entries[0].(map[string]any)["weight"].(float64) != 72.5 {
		t.Fatalf("weight = %v", entries[0])
	}
}

// entryBlob builds a valid, compressible blob with n distinct weight entries.
func entryBlob(n int) string {
	entries := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, map[string]any{
			"date":     fmt.Sprintf("2026-%02d-%02d", i/28+1, i%28+1),
			"weight":   70 + float64(i)/10,
			"calories": 1800,
			"type":     "training",
			"note":     strings.Repeat("allenamento ", 6),
		})
	}
	return string(mustRaw(map[string]any{"entries": entries}))
}

func TestGetMissing(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := do(t, http.MethodGet, srv.URL+"/api/kv/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestBadKey(t *testing.T) {
	srv := newTestServer(t)
	for _, key := range []string{"..", "a/b", "has space"} {
		resp, _ := do(t, http.MethodGet, srv.URL+"/api/kv/"+key, "")
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("key %q: status = %d, want 400/404", key, resp.StatusCode)
		}
	}
}

func TestDelete(t *testing.T) {
	srv := newTestServer(t)
	do(t, http.MethodPut, srv.URL+"/api/kv/x", `{"a":1}`)
	resp, _ := do(t, http.MethodDelete, srv.URL+"/api/kv/x", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}
	resp2, _ := do(t, http.MethodGet, srv.URL+"/api/kv/x", "")
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete status = %d, want 404", resp2.StatusCode)
	}
}

func TestPreflight(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := do(t, http.MethodOptions, srv.URL+"/api/kv/default", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("options status = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header")
	}
}

// --- gzip -------------------------------------------------------------------

// rawClient does not transparently decompress, so tests can inspect the wire.
func rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

func doWith(t *testing.T, c *http.Client, method, url string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                    false,
		"gzip":                true,
		"GZIP":                true,
		"gzip;q=0":            false,
		"gzip; q=0.0":         false,
		"gzip;q=0.5":          true,
		"deflate, gzip;q=0.8": true,
		"deflate, br":         false,
		"*":                   true,
		"*;q=0":               false,
		"identity":            false,
	}
	for hdr, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if hdr != "" {
			r.Header.Set("Accept-Encoding", hdr)
		}
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", hdr, got, want)
		}
	}
}

func TestGetCompressesPlainBlob(t *testing.T) {
	srv := newTestServer(t)
	blob := entryBlob(120)
	doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/default", []byte(blob), nil)

	resp, body := doWith(t, rawClient(), http.MethodGet, srv.URL+"/api/kv/default", nil,
		map[string]string{"Accept-Encoding": "gzip"})
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	if !isGzip(body) {
		t.Fatal("body is not gzip on the wire")
	}
	plain, err := gunzipLimit(body, maxBlobBytes)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if len(body) >= len(plain) {
		t.Fatalf("gzip did not shrink: %d >= %d", len(body), len(plain))
	}
	var state map[string]any
	if err := json.Unmarshal(plain, &state); err != nil {
		t.Fatalf("gunzipped body is not JSON: %v", err)
	}
	if got := len(state["entries"].([]any)); got != 120 {
		t.Fatalf("entries = %d, want 120", got)
	}
}

func TestGetPlainWhenGzipNotAccepted(t *testing.T) {
	srv := newTestServer(t)
	blob := entryBlob(120)
	doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/default", []byte(blob), nil)

	resp, body := doWith(t, rawClient(), http.MethodGet, srv.URL+"/api/kv/default", nil, nil)
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	var state map[string]any
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got := len(state["entries"].([]any)); got != 120 {
		t.Fatalf("entries = %d, want 120", got)
	}
}

func TestGetSkipsTinyBlob(t *testing.T) {
	srv := newTestServer(t)
	blob := `{"entries":[{"date":"2026-01-10","weight":70}]}`
	doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/small", []byte(blob), nil)

	resp, body := doWith(t, rawClient(), http.MethodGet, srv.URL+"/api/kv/small", nil,
		map[string]string{"Accept-Encoding": "gzip"})
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for a tiny blob", got)
	}
	if !strings.Contains(string(body), "2026-01-10") {
		t.Fatalf("body = %s", body)
	}
}

func TestGzippedUploadImportedAndServed(t *testing.T) {
	srv := newTestServer(t)
	blob := entryBlob(120)
	gz, err := gzipBytes([]byte(blob))
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}

	resp, _ := doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/gz", gz,
		map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put status = %d, want 204", resp.StatusCode)
	}

	// A gzip-capable client gets a compressed, fully populated blob.
	resp2, body2 := doWith(t, rawClient(), http.MethodGet, srv.URL+"/api/kv/gz", nil,
		map[string]string{"Accept-Encoding": "gzip"})
	if resp2.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", resp2.Header.Get("Content-Encoding"))
	}
	plain, err := gunzipLimit(body2, maxBlobBytes)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(plain, &state); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got := len(state["entries"].([]any)); got != 120 {
		t.Fatalf("entries = %d, want 120", got)
	}
}

func TestPutRejectsFakeGzip(t *testing.T) {
	srv := newTestServer(t)
	fake := append([]byte{0x1f, 0x8b}, []byte("not actually gzip")...)
	resp, _ := doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/bad", fake,
		map[string]string{"Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPutIgnoresMisleadingContentEncoding(t *testing.T) {
	// A proxy that decompressed the body in flight leaves a lying header; the
	// bytes win, so the plain JSON is imported all the same.
	srv := newTestServer(t)
	blob := `{"entries":[{"date":"2026-01-10","weight":70}]}`
	resp, _ := doWith(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/kv/lie", []byte(blob),
		map[string]string{"Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	resp2, body := doWith(t, rawClient(), http.MethodGet, srv.URL+"/api/kv/lie", nil, nil)
	if got := resp2.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	var state map[string]any
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got := len(state["entries"].([]any)); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
}

func TestGunzipLimitRejectsBomb(t *testing.T) {
	bomb, err := gzipBytes(bytes.Repeat([]byte("a"), 4096))
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	if _, err := gunzipLimit(bomb, 1024); err == nil {
		t.Fatal("gunzipLimit accepted a blob that expands past the limit")
	}
	if _, err := gunzipLimit(bomb, 8192); err != nil {
		t.Fatalf("gunzipLimit rejected a blob within the limit: %v", err)
	}
}

func TestPreflightAllowsContentEncoding(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doWith(t, rawClient(), http.MethodOptions, srv.URL+"/api/kv/default", nil,
		map[string]string{
			"Origin":                         "null",
			"Access-Control-Request-Method":  "PUT",
			"Access-Control-Request-Headers": "content-encoding",
		})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if h := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(h, "Content-Encoding") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to include Content-Encoding", h)
	}
	if h := resp.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(h, "X-Updated-At") {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it to include X-Updated-At", h)
	}
}
