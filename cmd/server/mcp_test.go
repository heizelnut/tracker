package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpPost sends a raw JSON-RPC body to /mcp and decodes the response.
func mcpPost(t *testing.T, srv *httptest.Server, payload map[string]any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, text := do(t, http.MethodPost, srv.URL+"/mcp", string(body))
	var out map[string]any
	if strings.TrimSpace(text) != "" {
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("decode response %q: %v", text, err)
		}
	}
	return resp.StatusCode, out
}

// mcpCall runs a tool and returns its structuredContent.
func mcpCall(t *testing.T, srv *httptest.Server, name string, args map[string]any) map[string]any {
	t.Helper()
	code, resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if code != http.StatusOK {
		t.Fatalf("tools/call %s: status %d (%v)", name, code, resp)
	}
	if e, ok := resp["error"]; ok {
		t.Fatalf("tools/call %s: rpc error %v", name, e)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s: missing result: %v", name, resp)
	}
	if result["isError"] == true {
		t.Fatalf("tools/call %s: tool error: %v", name, result["content"])
	}
	sc, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s: missing structuredContent: %v", name, result)
	}
	return sc
}

func getBlob(t *testing.T, srv *httptest.Server, key string) map[string]any {
	t.Helper()
	resp, body := do(t, http.MethodGet, srv.URL+"/api/kv/"+key, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get %s: status %d", key, resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode blob: %v", err)
	}
	return out
}

func TestMCPInitialize(t *testing.T) {
	srv := newTestServer(t)
	code, resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18"},
	})
	if code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", code)
	}
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want the requested one echoed back", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "tracker" {
		t.Fatalf("serverInfo.name = %v", info["name"])
	}
	caps := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatal("initialize must advertise the tools capability")
	}
}

func TestMCPInitializeFallsBackForUnknownVersion(t *testing.T) {
	srv := newTestServer(t)
	_, resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "1999-01-01"},
	})
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v, want fallback %s", result["protocolVersion"], mcpProtocolVersion)
	}
}

func TestMCPNotificationAcceptedWithoutBody(t *testing.T) {
	srv := newTestServer(t)
	code, resp := mcpPost(t, srv, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if code != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", code)
	}
	if len(resp) != 0 {
		t.Fatalf("notification must have no response, got %v", resp)
	}
}

func TestMCPToolsList(t *testing.T) {
	srv := newTestServer(t)
	code, resp := mcpPost(t, srv, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if code != http.StatusOK {
		t.Fatalf("tools/list status = %d", code)
	}
	tools := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		tool := tl.(map[string]any)
		names[tool["name"].(string)] = true
		if _, ok := tool["inputSchema"]; !ok {
			t.Fatalf("tool %v has no inputSchema", tool["name"])
		}
	}
	for _, want := range []string{"tracker_summary", "list_foods", "add_food", "list_workouts", "add_workout"} {
		if !names[want] {
			t.Fatalf("tools/list is missing %q (got %v)", want, names)
		}
	}
}

func TestMCPGetRejected(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := do(t, http.MethodGet, srv.URL+"/mcp", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d, want 405", resp.StatusCode)
	}
}

func TestMCPAddFoodPreservesOtherCollections(t *testing.T) {
	srv := newTestServer(t)
	original := `{"entries":[{"id":"e1","date":"2026-01-10","weight":72.5}],` +
		`"diary":{"2026-01-10":{"type":"training","items":[]}},` +
		`"foods":[],"workouts":[],"done":{"2026-01-10":{"x":true}},` +
		`"app":"ricomposizione-tracker","version":4}`
	if resp, _ := do(t, http.MethodPut, srv.URL+"/api/kv/default", original); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("seed put status = %d", resp.StatusCode)
	}

	created := mcpCall(t, srv, "add_food", map[string]any{
		"name": "Skyr", "p": 11, "g": 0.2, "c": 4, "min": 150, "cat": "proteina,latticino",
	})
	if created["created"] != true {
		t.Fatalf("created = %v, want true", created["created"])
	}
	food := created["food"].(map[string]any)
	if got := food["kcal"].(float64); got != 62 { // 11*4 + 4*4 + 0.2*9 = 61.8 -> 62
		t.Fatalf("kcal = %v, want 62", got)
	}
	if got := food["id"].(string); got == "" {
		t.Fatal("food has no id")
	}

	blob := getBlob(t, srv, "default")
	// Untouched collections must survive byte-for-byte semantically.
	if len(blob["entries"].([]any)) != 1 {
		t.Fatalf("entries were lost: %v", blob["entries"])
	}
	if blob["app"] != "ricomposizione-tracker" || blob["version"].(float64) != 4 {
		t.Fatalf("top-level fields were lost: %v", blob)
	}
	if _, ok := blob["diary"].(map[string]any)["2026-01-10"]; !ok {
		t.Fatal("diary was lost")
	}
	foods := blob["foods"].([]any)
	if len(foods) != 1 {
		t.Fatalf("foods = %v", foods)
	}
	// meta.updated.foods must carry the id so the app's per-record merge pulls it.
	meta := blob["meta"].(map[string]any)
	updated := meta["updated"].(map[string]any)["foods"].(map[string]any)
	if _, ok := updated[food["id"].(string)]; !ok {
		t.Fatalf("meta.updated.foods missing record: %v", updated)
	}
}

func TestMCPAddFoodComputesKcalAndUpserts(t *testing.T) {
	srv := newTestServer(t)
	first := mcpCall(t, srv, "add_food", map[string]any{"name": "Riso", "p": 7, "g": 1, "c": 78})
	food := first["food"].(map[string]any)
	if got := food["kcal"].(float64); got != 7*4+78*4+1*9 {
		t.Fatalf("kcal = %v, want %v", got, 7*4+78*4+1*9)
	}
	id := food["id"].(string)

	second := mcpCall(t, srv, "add_food", map[string]any{"name": "riso", "p": 6.5, "g": 0.8, "c": 79})
	if second["created"] != false {
		t.Fatalf("second add should update, got created=%v", second["created"])
	}
	if got := second["food"].(map[string]any)["id"].(string); got != id {
		t.Fatalf("upsert changed the id: %s -> %s", id, got)
	}
	blob := getBlob(t, srv, "default")
	if len(blob["foods"].([]any)) != 1 {
		t.Fatalf("upsert duplicated the food: %v", blob["foods"])
	}
}

func TestMCPListFoodsFilterAndPage(t *testing.T) {
	srv := newTestServer(t)
	for _, f := range []map[string]any{
		{"name": "Pollo", "p": 23, "g": 2, "c": 0, "cat": "proteina"},
		{"name": "Riso", "p": 7, "g": 1, "c": 78, "cat": "carboidrato"},
		{"name": "Pollo al forno", "p": 25, "g": 4, "c": 1, "cat": "proteina"},
	} {
		mcpCall(t, srv, "add_food", f)
	}

	all := mcpCall(t, srv, "list_foods", map[string]any{})
	if all["total"].(float64) != 3 {
		t.Fatalf("total = %v, want 3", all["total"])
	}
	filtered := mcpCall(t, srv, "list_foods", map[string]any{"query": "pollo"})
	if filtered["count"].(float64) != 2 {
		t.Fatalf("query count = %v, want 2", filtered["count"])
	}
	byCat := mcpCall(t, srv, "list_foods", map[string]any{"category": "proteina"})
	if byCat["count"].(float64) != 2 {
		t.Fatalf("category count = %v, want 2", byCat["count"])
	}
	page := mcpCall(t, srv, "list_foods", map[string]any{"limit": 1, "offset": 1})
	if page["count"].(float64) != 1 || page["offset"].(float64) != 1 {
		t.Fatalf("page = %v", page)
	}
}

func TestMCPAddWorkoutAndSessions(t *testing.T) {
	srv := newTestServer(t)
	first := mcpCall(t, srv, "add_workout", map[string]any{
		"session": "Giorno 1 — Upper A", "name": "Panca piana", "sets": "3", "reps": "6-10", "rest": "2-3'",
	})
	if first["created"] != true {
		t.Fatalf("created = %v", first["created"])
	}
	mcpCall(t, srv, "add_workout", map[string]any{"session": "Giorno 1 — Upper A", "name": "Lat machine", "sets": "3", "reps": "8-12"})
	mcpCall(t, srv, "add_workout", map[string]any{"session": "Giorno 2", "name": "Leg press", "sets": "3", "reps": "8-12"})

	updated := mcpCall(t, srv, "add_workout", map[string]any{
		"session": "Giorno 1 — Upper A", "name": "panca piana", "sets": "4", "reps": "5",
	})
	if updated["created"] != false {
		t.Fatalf("same session+name should update, got created=%v", updated["created"])
	}

	list := mcpCall(t, srv, "list_workouts", map[string]any{})
	if list["total"].(float64) != 3 {
		t.Fatalf("total = %v, want 3", list["total"])
	}
	sessions := list["sessions"].([]any)
	if len(sessions) != 2 || sessions[0] != "Giorno 1 — Upper A" {
		t.Fatalf("sessions = %v", sessions)
	}
	one := mcpCall(t, srv, "list_workouts", map[string]any{"session": "giorno 2"})
	if one["count"].(float64) != 1 {
		t.Fatalf("session filter count = %v, want 1", one["count"])
	}
}

func TestMCPSummary(t *testing.T) {
	srv := newTestServer(t)
	mcpCall(t, srv, "add_food", map[string]any{"name": "Uova", "p": 13, "g": 11, "c": 1, "cat": "proteina,uovo"})
	mcpCall(t, srv, "add_workout", map[string]any{"session": "Giorno 1", "name": "Squat", "sets": "3"})

	summary := mcpCall(t, srv, "tracker_summary", map[string]any{})
	counts := summary["counts"].(map[string]any)
	if counts["foods"].(float64) != 1 || counts["workouts"].(float64) != 1 {
		t.Fatalf("counts = %v", counts)
	}
	cats := summary["categories"].([]any)
	if len(cats) != 2 {
		t.Fatalf("categories = %v", cats)
	}
	targets := summary["targets"].(map[string]any)
	if targets["training"].(map[string]any)["kcal"].(float64) != 1800 {
		t.Fatalf("training target = %v", targets["training"])
	}
}

func TestMCPUnknownTool(t *testing.T) {
	srv := newTestServer(t)
	code, resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "nope", "arguments": map[string]any{}},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if resp["error"] == nil {
		t.Fatalf("unknown tool must be a JSON-RPC error, got %v", resp)
	}
}

func TestMCPBadKeyIsToolError(t *testing.T) {
	srv := newTestServer(t)
	_, resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list_foods", "arguments": map[string]any{"key": "a/b"}},
	})
	if resp["error"] != nil {
		t.Fatalf("bad key should be a tool error, not an rpc error: %v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("bad key must set isError, got %v", result)
	}
}

func TestMCPRejectsMalformedJSON(t *testing.T) {
	srv := newTestServer(t)
	resp, body := do(t, http.MethodPost, srv.URL+"/mcp", "{not json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a JSON-RPC error", resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["error"] == nil {
		t.Fatalf("parse error not reported: %v", out)
	}
}
