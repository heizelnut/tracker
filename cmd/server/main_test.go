package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := &server{dataDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /api/kv/{key}", s.get)
	mux.HandleFunc("PUT /api/kv/{key}", s.put)
	mux.HandleFunc("DELETE /api/kv/{key}", s.del)
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
	if got != blob {
		t.Fatalf("blob = %s, want %s", got, blob)
	}
	if resp2.Header.Get("X-Updated-At") == "" {
		t.Fatal("missing X-Updated-At header")
	}
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
