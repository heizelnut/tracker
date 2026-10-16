package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- incremental sync API ----------------------------------------------------

func TestSyncPushAndDelta(t *testing.T) {
	srv := newTestServer(t)

	push(t, srv.URL, "default", `{"records":[{"collection":"entries","id":"2026-01-01","data":{"date":"2026-01-01","weight":70},"updated":100}]}`)

	delta := pullDelta(t, srv.URL, "default", 0)
	if len(delta.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(delta.Records))
	}
	if delta.Records[0].Updated != 100 {
		t.Fatalf("updated = %d, want 100", delta.Records[0].Updated)
	}
	var data map[string]any
	if err := json.Unmarshal(delta.Records[0].Data, &data); err != nil || data["weight"].(float64) != 70 {
		t.Fatalf("data = %s", delta.Records[0].Data)
	}

	// Nothing new after the watermark.
	if got := len(pullDelta(t, srv.URL, "default", 100).Records); got != 0 {
		t.Fatalf("delta after watermark = %d, want 0", got)
	}

	// An older push must not overwrite the newer record.
	push(t, srv.URL, "default", `{"records":[{"collection":"entries","id":"2026-01-01","data":{"date":"2026-01-01","weight":99},"updated":50}]}`)
	again := pullDelta(t, srv.URL, "default", 0)
	if w := mustWeight(t, again.Records[0].Data); w != 70 {
		t.Fatalf("older push won: weight = %v", w)
	}

	// A newer push does.
	push(t, srv.URL, "default", `{"records":[{"collection":"entries","id":"2026-01-01","data":{"date":"2026-01-01","weight":71},"updated":200}]}`)
	if w := mustWeight(t, pullDelta(t, srv.URL, "default", 0).Records[0].Data); w != 71 {
		t.Fatalf("newer push lost: weight = %v", w)
	}
}

func TestSyncDeltaIncludesTombstones(t *testing.T) {
	srv := newTestServer(t)
	push(t, srv.URL, "default", `{"records":[{"collection":"foods","id":"f1","data":{"id":"f1","name":"Pollo"},"updated":10}]}`)
	push(t, srv.URL, "default", `{"records":[{"collection":"foods","id":"f1","deleted":true,"updated":20}]}`)

	delta := pullDelta(t, srv.URL, "default", 0)
	if len(delta.Records) != 1 || !delta.Records[0].Deleted {
		t.Fatalf("expected a single tombstone, got %+v", delta.Records)
	}
}

func TestSyncIsolatesKeys(t *testing.T) {
	srv := newTestServer(t)
	push(t, srv.URL, "alice", `{"records":[{"collection":"entries","id":"2026-01-01","data":{"date":"2026-01-01","weight":70},"updated":10}]}`)
	if got := len(pullDelta(t, srv.URL, "bob", 0).Records); got != 0 {
		t.Fatalf("bob sees alice's records: %d", got)
	}
}

// --- SSE ---------------------------------------------------------------------

func TestSSEBroadcastsChanges(t *testing.T) {
	srv := newTestServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/events/default", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	reader := bufio.NewReader(resp.Body)
	if line, _ := reader.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first SSE line = %q", line)
	}

	push(t, srv.URL, "default", `{"records":[{"collection":"foods","id":"f9","data":{"id":"f9","name":"Skyr"},"updated":42}]}`)

	payload := readSSEData(t, reader, 5*time.Second)
	var event struct {
		Records []record `json:"records"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("event payload %q: %v", payload, err)
	}
	if len(event.Records) != 1 || event.Records[0].ID != "f9" {
		t.Fatalf("event records = %+v", event.Records)
	}
}

func readSSEData(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- result{"", err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				ch <- result{strings.TrimPrefix(line, "data: "), nil}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read SSE: %v", res.err)
		}
		return res.line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an SSE event")
		return ""
	}
}

// --- legacy migration --------------------------------------------------------

func TestLegacyBlobMigration(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(filepath.Join(dir, "tracker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &server{dataDir: dir, store: st, hub: newHub()}

	blob := `{
		"entries":[{"id":"e1","date":"2026-01-01","weight":70,"calories":1700,"type":"rest"}],
		"foods":[{"id":"f1","name":"Pollo","p":23,"g":2,"c":0,"kcal":110}],
		"diary":{"2026-01-01":{"type":"rest","items":[]}},
		"workouts":[{"id":"w1","session":"Giorno 1","name":"Panca","sets":"3"}],
		"done":{"2026-01-01":{"w1":true}},
		"meta":{"updated":{"entries":{"2026-01-01":111},"foods":{"f1":222}},"tombstones":{}}
	}`
	if err := os.WriteFile(filepath.Join(dir, "default"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.ensureImported("default"); err != nil {
		t.Fatalf("ensureImported: %v", err)
	}
	// Second call must be a no-op (idempotent).
	if err := s.ensureImported("default"); err != nil {
		t.Fatalf("second ensureImported: %v", err)
	}

	byKey := map[string]record{}
	recs, err := s.store.all("default")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		byKey[r.Collection+"\x00"+r.ID] = r
	}
	if len(recs) != 5 {
		t.Fatalf("imported %d records, want 5: %+v", len(recs), recs)
	}
	if byKey["entries\x002026-01-01"].Updated != 111 {
		t.Fatalf("entry timestamp lost: %+v", byKey["entries\x002026-01-01"])
	}
	if byKey["foods\x00f1"].Updated != 222 {
		t.Fatalf("food timestamp lost: %+v", byKey["foods\x00f1"])
	}
	if byKey["diary\x002026-01-01"].Updated == 0 {
		t.Fatal("diary day imported without a timestamp")
	}
	if _, ok := byKey["done\x002026-01-01\x00w1"]; !ok {
		t.Fatalf("done record missing: %+v", recs)
	}
}

func TestLegacyMigrationFromGzipFile(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(filepath.Join(dir, "tracker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &server{dataDir: dir, store: st, hub: newHub()}

	blob := `{"entries":[{"date":"2026-02-02","weight":68}]}`
	gz, err := gzipBytes([]byte(blob))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gzkey"), gz, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureImported("gzkey"); err != nil {
		t.Fatalf("ensureImported: %v", err)
	}
	recs, _ := s.store.all("gzkey")
	if len(recs) != 1 || recs[0].ID != "2026-02-02" {
		t.Fatalf("records = %+v", recs)
	}
}

// --- store rules -------------------------------------------------------------

func TestStoreLastWriteWins(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rec := func(ts int64, weight float64) record {
		return record{Collection: "entries", ID: "d", Data: mustRaw(map[string]any{"date": "d", "weight": weight}), Updated: ts}
	}
	weight := func() float64 {
		got, ok, err := st.get("k", "entries", "d")
		if err != nil || !ok {
			t.Fatalf("get: ok=%v err=%v", ok, err)
		}
		return mustWeight(t, got.Data)
	}

	if _, err := st.upsert("k", []record{rec(100, 70)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.upsert("k", []record{rec(50, 99)}); err != nil {
		t.Fatal(err)
	}
	if w := weight(); w != 70 {
		t.Fatalf("older write won: %v", w)
	}
	if _, err := st.upsert("k", []record{rec(100, 71)}); err != nil {
		t.Fatal(err)
	}
	if w := weight(); w != 71 {
		t.Fatalf("equal-timestamp write lost: %v", w)
	}
	// At equal timestamps a tombstone beats a record.
	if _, err := st.upsert("k", []record{{Collection: "entries", ID: "d", Updated: 100, Deleted: true}}); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := st.get("k", "entries", "d"); !ok || !got.Deleted {
		t.Fatalf("tombstone did not win the tie: ok=%v rec=%+v", ok, got)
	}
}

// --- helpers -----------------------------------------------------------------

type deltaBody struct {
	Records []record `json:"records"`
}

func pullDelta(t *testing.T, base, key string, since int64) deltaBody {
	t.Helper()
	resp, body := do(t, http.MethodGet, base+"/api/sync/"+key+"?since="+strconv.FormatInt(since, 10), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync pull status = %d (%s)", resp.StatusCode, body)
	}
	var out deltaBody
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode delta %q: %v", body, err)
	}
	return out
}

func push(t *testing.T, base, key, body string) {
	t.Helper()
	resp, txt := do(t, http.MethodPost, base+"/api/sync/"+key, body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("sync push status = %d (%s)", resp.StatusCode, txt)
	}
}

func mustWeight(t *testing.T, data json.RawMessage) float64 {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode record data %s: %v", data, err)
	}
	return m["weight"].(float64)
}
