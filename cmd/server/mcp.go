package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file adds a small, dependency-free Model Context Protocol server on top
// of the blob store. It speaks "Streamable HTTP": JSON-RPC 2.0 over POST /mcp,
// answering with plain application/json (the spec allows this, and every client
// that asks for text/event-stream also accepts JSON).
//
// The tools know two of the collections inside the otherwise opaque blob:
// `foods` (the food database) and `workouts` (the training program). Writes are
// read-merged-write: the new record is stamped in `meta.updated`, exactly like
// the browser does, so the next app sync pulls it in per record instead of
// fighting over the whole blob. Unknown top-level keys (entries, diary, done,
// …) are passed through untouched.

// mcpProtocolVersion is the newest revision of the protocol we implement. The
// client names a version in initialize; we echo it back when we know it and
// fall back to this otherwise.
const mcpProtocolVersion = "2025-11-25"

var supportedMCPVersions = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
	"2024-10-07": true,
}

// defaultMCPKey matches the app's default sync key.
const defaultMCPKey = "default"

const mcpInstructions = `Tools to inspect and extend the Ricomposizione tracker.

The tracker stores everything (weight log, food diary, food database, workout
program) in one synced blob. This server exposes the collections that are
useful to edit from outside the app:

  - foods    — the food database. Macros are per 100 g.
  - workouts — the training program, split into sessions (days).
  - diary    — what was eaten, day by day: one record per date holding the day
               type (training/rest) and the list of foods with their grams.

Read tools return the current state. Write tools add or update a single record
and return it. Writes are merged per record with the app (a newer timestamp
wins), so a new food, exercise or diary day shows up on the device at its next
sync — and the device's own edits are not lost. Foods are upserted by name,
workouts by (session, exercise name); the diary is keyed by date, and the items
inside a day are addressed by their id or food name.

Use "key" to target a different sync key/blob; it defaults to "default".`

// --- MCP wire types ----------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// --- Routing and transport ---------------------------------------------------

func (s *server) mcpRoute(w http.ResponseWriter, r *http.Request) {
	// GET would open a server->client SSE stream and DELETE would end a session.
	// This server is stateless and never pushes, so both are "method not
	// allowed" — which is exactly how a client is told there is no stream.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlobBytes+(1<<20)))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: codeParseError, Message: "parse error"},
		})
		return
	}

	result, rerr, notification := s.handleRPC(&req)
	if notification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleRPC(req *rpcRequest) (result any, rerr *rpcError, notification bool) {
	// Notifications carry no id and expect no reply.
	if len(req.ID) == 0 || strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil, true
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpProtocolVersion
		if supportedMCPVersions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{
				"name":    "tracker",
				"title":   "Ricomposizione tracker",
				"version": "1.0.0",
			},
			"instructions": mcpInstructions,
		}, nil, false

	case "ping":
		return map[string]any{}, nil, false

	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil, false

	case "tools/call":
		var p callParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params"}, false
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		v, err := s.runTool(p.Name, p.Arguments)
		if err != nil {
			if errors.Is(err, errUnknownTool) {
				return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}, false
			}
			return toolErrorResult(err.Error()), nil, false
		}
		return toolResult(v), nil, false

	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}, false
	}
}

// --- Tool catalogue ----------------------------------------------------------

var errUnknownTool = errors.New("unknown tool")

// mcpTools returns the tool list. Descriptions are what the model sees, so they
// say what each tool does and what it returns.
func mcpTools() []map[string]any {
	keyProp := map[string]any{
		"type":        "string",
		"description": `Sync key/blob to operate on (default "default").`,
	}
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	num := func(desc string) map[string]any {
		return map[string]any{"type": "number", "description": desc}
	}
	intProp := func(desc string, min, max float64) map[string]any {
		return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
	}

	return []map[string]any{
		{
			"name":        "tracker_summary",
			"title":       "Tracker summary",
			"description": "Overview of the tracker state: record counts, workout sessions, food categories, the latest weight/calorie entries and the daily macro targets. Start here to understand what is in the database.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": keyProp},
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name":        "list_foods",
			"title":       "List foods",
			"description": `List the food database. Each food has name, calories and protein/fat/carbs per 100 g, optional recommended min/max portion in grams, a comma-separated category tag ("cat") and grams per piece. Supports a name substring and a category filter.`,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":    str("Case-insensitive substring matched against the food name."),
					"category": str("Only foods whose comma-separated cat contains this tag (case-insensitive)."),
					"limit":    intProp("Maximum foods to return (default 100).", 1, 1000),
					"offset":   intProp("Skip this many foods (default 0).", 0, 100000),
					"key":      keyProp,
				},
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name":        "add_food",
			"title":       "Add or update a food",
			"description": "Add a food to the database, or update it if a food with the same name already exists (matched case-insensitively). Macros are per 100 g; kcal is computed from P*4 + C*4 + G*9 when omitted. Returns the stored record and whether it was created or updated.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  str("Food name. Required; also the upsert key."),
					"p":     num("Protein in grams per 100 g."),
					"g":     num("Fat in grams per 100 g."),
					"c":     num("Carbohydrates in grams per 100 g."),
					"kcal":  num("Calories per 100 g. Computed from the macros when omitted."),
					"min":   num("Recommended minimum portion in grams."),
					"max":   num("Recommended maximum portion in grams."),
					"cat":   str(`Comma-separated category tags, e.g. "proteina,latticino".`),
					"piece": num("Grams per piece, when the food is eaten by the piece."),
					"key":   keyProp,
				},
				"required": []string{"name"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			"name":        "list_workouts",
			"title":       "List workouts",
			"description": "List the training program. Every exercise has a session (training day), name, sets, reps, rest and free-text notes. Supports a session filter and a name substring. The response also lists the available sessions.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session": str("Only exercises in this session (exact, case-insensitive)."),
					"query":   str("Case-insensitive substring matched against the exercise name."),
					"key":     keyProp,
				},
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name":        "add_workout",
			"title":       "Add or update a workout exercise",
			"description": "Add an exercise to the training program, or update it when the same (session, exercise name) already exists. Sets, reps and rest are free text so both \"3 x 6-10\" and \"2-3'\" work. Returns the stored record and whether it was created or updated.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    str("Exercise name. Required; upserted together with session."),
					"session": str(`Training day, e.g. "Giorno 1 — Upper A".`),
					"sets":    str("Number of sets, e.g. \"3\"."),
					"reps":    str(`Repetitions, e.g. "6-10".`),
					"rest":    str(`Rest between sets, e.g. "2-3'" or "60-90\"".`),
					"notes":   str("Free-text note."),
					"key":     keyProp,
				},
				"required": []string{"name"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			"name":        "list_diary",
			"title":       "Read the food diary",
			"description": "Read the diary day by day. With `date` (YYYY-MM-DD) returns that day's type, items (id, food, grams, macros) and totals. Without it, lists recent days as summaries (date, type, item count, totals), newest first, optionally filtered by `from`/`to`.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date":  str("Exact day to read, YYYY-MM-DD."),
					"from":  str("Only days on or after this date (YYYY-MM-DD)."),
					"to":    str("Only days on or before this date (YYYY-MM-DD)."),
					"limit": intProp("Maximum days to list when `date` is omitted (default 30).", 1, 1000),
					"key":   keyProp,
				},
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name":        "add_diary_item",
			"title":       "Add a food to a diary day",
			"description": "Add a food to a day's diary, creating the day if needed. `food` is a food id, an exact name or an unambiguous substring from the food database; the item's macros are computed from the food and `grams`. Returns the new item (keep its id to update or remove it later) and the day totals.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date":  str("Day to update, YYYY-MM-DD. Required."),
					"food":  str("Food id, exact name or substring. Required."),
					"grams": num("Portion in grams. Required."),
					"key":   keyProp,
				},
				"required": []string{"date", "food", "grams"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
		{
			"name":        "update_diary_item",
			"title":       "Update a diary item",
			"description": "Change the grams of an item already in a day's diary and recompute its macros. Identify the item by its id or by food name (first match). Returns the updated item and the day totals.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date":  str("Day of the item, YYYY-MM-DD. Required."),
					"item":  str("Item id or food name. Required."),
					"grams": num("New portion in grams. Required."),
					"key":   keyProp,
				},
				"required": []string{"date", "item", "grams"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			"name":        "remove_diary_item",
			"title":       "Remove a diary item",
			"description": "Remove one item from a day's diary, identified by item id or food name (first match). Returns the removed item and the new day totals.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date": str("Day of the item, YYYY-MM-DD. Required."),
					"item": str("Item id or food name. Required."),
					"key":  keyProp,
				},
				"required": []string{"date", "item"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			"name":        "set_diary_type",
			"title":       "Set a diary day type",
			"description": "Mark a diary day as a training or rest day, creating the day if needed. The day type drives the calorie/macro targets.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date": str("Day to mark, YYYY-MM-DD. Required."),
					"type": map[string]any{"type": "string", "enum": []string{"training", "rest"}, "description": "Day type."},
					"key":  keyProp,
				},
				"required": []string{"date", "type"},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
	}
}

// --- Tool dispatch -----------------------------------------------------------

func (s *server) runTool(name string, args map[string]any) (any, error) {
	switch name {
	case "tracker_summary":
		return s.toolSummary(args)
	case "list_foods":
		return s.toolListFoods(args)
	case "add_food":
		return s.toolAddFood(args)
	case "list_workouts":
		return s.toolListWorkouts(args)
	case "add_workout":
		return s.toolAddWorkout(args)
	case "list_diary":
		return s.toolListDiary(args)
	case "add_diary_item":
		return s.toolAddDiaryItem(args)
	case "update_diary_item":
		return s.toolUpdateDiaryItem(args)
	case "remove_diary_item":
		return s.toolRemoveDiaryItem(args)
	case "set_diary_type":
		return s.toolSetDiaryType(args)
	default:
		return nil, fmt.Errorf("%w: %s", errUnknownTool, name)
	}
}

func (s *server) toolSummary(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	state, err := s.loadState(key)
	if err != nil {
		return nil, err
	}

	foods, err := decodeObjects(state["foods"])
	if err != nil {
		return nil, err
	}
	workouts, err := decodeObjects(state["workouts"])
	if err != nil {
		return nil, err
	}
	entries, err := decodeObjects(state["entries"])
	if err != nil {
		return nil, err
	}

	categories := map[string]bool{}
	for _, f := range foods {
		for _, tag := range strings.Split(getString(f, "cat"), ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				categories[tag] = true
			}
		}
	}
	catList := make([]string, 0, len(categories))
	for tag := range categories {
		catList = append(catList, tag)
	}
	sort.Strings(catList)

	sort.SliceStable(entries, func(i, j int) bool {
		return getString(entries[i], "date") < getString(entries[j], "date")
	})
	latest := entries
	if len(latest) > 7 {
		latest = latest[len(latest)-7:]
	}
	recent := make([]map[string]any, 0, len(latest))
	for _, e := range latest {
		recent = append(recent, map[string]any{
			"date":     getString(e, "date"),
			"weight":   getValue(e, "weight"),
			"calories": getValue(e, "calories"),
			"type":     getString(e, "type"),
		})
	}

	return map[string]any{
		"key": key,
		"counts": map[string]any{
			"foods":     len(foods),
			"workouts":  len(workouts),
			"entries":   len(entries),
			"diaryDays": len(decodeMapKeys(state["diary"])),
		},
		"sessions":     sessionCounts(workouts),
		"categories":   catList,
		"latestWeight": recent,
		// Mirrors TARGETS in index.html; keep the two in sync.
		"targets": map[string]any{
			"training": map[string]any{"kcal": 1800, "p": 145, "g": 55, "c": 180},
			"rest":     map[string]any{"kcal": 1650, "p": 145, "g": 55, "c": 145},
		},
	}, nil
}

func (s *server) toolListFoods(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	state, err := s.loadState(key)
	if err != nil {
		return nil, err
	}
	foods, err := decodeObjects(state["foods"])
	if err != nil {
		return nil, err
	}

	query := strings.ToLower(argString(args, "query"))
	category := strings.ToLower(argString(args, "category"))
	filtered := make([]map[string]any, 0, len(foods))
	for _, f := range foods {
		if query != "" && !strings.Contains(strings.ToLower(getString(f, "name")), query) {
			continue
		}
		if category != "" && !hasCategory(f, category) {
			continue
		}
		filtered = append(filtered, f)
	}

	total := len(filtered)
	offset := argInt(args, "offset", 0)
	limit := argInt(args, "limit", 100)
	if offset > total {
		offset = total
	}
	page := filtered[offset:]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}

	return map[string]any{
		"total":  len(foods),
		"count":  len(page),
		"offset": offset,
		"foods":  page,
	}, nil
}

func (s *server) toolAddFood(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	name := argString(args, "name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	p, _ := argNumber(args, "p")
	g, _ := argNumber(args, "g")
	c, _ := argNumber(args, "c")
	kcal, hasKcal := argNumber(args, "kcal")
	if !hasKcal {
		kcal = math.Round(p*4 + c*4 + g*9)
	}
	for label, v := range map[string]float64{"p": p, "g": g, "c": c, "kcal": kcal} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s must be a finite number", label)
		}
	}

	var food map[string]any
	created := false
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		foods, err := decodeObjects(state["foods"])
		if err != nil {
			return err
		}
		idx, id := -1, ""
		for i, f := range foods {
			if strings.EqualFold(getString(f, "name"), name) {
				idx, id = i, getString(f, "id")
				break
			}
		}
		created = idx < 0
		if id == "" {
			id = newRecordID()
		}
		food = map[string]any{
			"id":    id,
			"name":  name,
			"p":     p,
			"g":     g,
			"c":     c,
			"kcal":  kcal,
			"min":   optionalNumber(args, "min"),
			"max":   optionalNumber(args, "max"),
			"cat":   argString(args, "cat"),
			"piece": optionalNumber(args, "piece"),
		}
		if idx >= 0 {
			foods[idx] = food
		} else {
			foods = append(foods, food)
		}
		sort.SliceStable(foods, func(i, j int) bool {
			return strings.ToLower(getString(foods[i], "name")) < strings.ToLower(getString(foods[j], "name"))
		})
		if err := setRaw(state, "foods", foods); err != nil {
			return err
		}
		return stampMeta(state, "foods", id)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "food": food}, nil
}

func (s *server) toolListWorkouts(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	state, err := s.loadState(key)
	if err != nil {
		return nil, err
	}
	workouts, err := decodeObjects(state["workouts"])
	if err != nil {
		return nil, err
	}

	session := strings.ToLower(argString(args, "session"))
	query := strings.ToLower(argString(args, "query"))
	filtered := make([]map[string]any, 0, len(workouts))
	for _, w := range workouts {
		if session != "" && strings.ToLower(getString(w, "session")) != session {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(getString(w, "name")), query) {
			continue
		}
		filtered = append(filtered, w)
	}

	return map[string]any{
		"total":    len(workouts),
		"count":    len(filtered),
		"sessions": sessionNames(workouts),
		"workouts": filtered,
	}, nil
}

func (s *server) toolAddWorkout(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	name := argString(args, "name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	session := argString(args, "session")

	var workout map[string]any
	created := false
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		workouts, err := decodeObjects(state["workouts"])
		if err != nil {
			return err
		}
		idx, id := -1, ""
		for i, w := range workouts {
			if strings.EqualFold(getString(w, "name"), name) && strings.EqualFold(getString(w, "session"), session) {
				idx, id = i, getString(w, "id")
				break
			}
		}
		created = idx < 0
		if id == "" {
			id = newRecordID()
		}
		workout = map[string]any{
			"id":      id,
			"session": session,
			"name":    name,
			"sets":    argString(args, "sets"),
			"reps":    argString(args, "reps"),
			"rest":    argString(args, "rest"),
			"notes":   argString(args, "notes"),
		}
		if idx >= 0 {
			workouts[idx] = workout
		} else {
			workouts = append(workouts, workout)
		}
		if err := setRaw(state, "workouts", workouts); err != nil {
			return err
		}
		return stampMeta(state, "workouts", id)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "workout": workout}, nil
}

// --- Diary -------------------------------------------------------------------

// toolListDiary reads a single day in full, or lists recent days as summaries.
func (s *server) toolListDiary(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	state, err := s.loadState(key)
	if err != nil {
		return nil, err
	}
	diary, err := decodeDiary(state["diary"])
	if err != nil {
		return nil, err
	}

	if date := argString(args, "date"); date != "" {
		day := diaryDay(diary, date)
		if day == nil {
			return map[string]any{
				"date":   date,
				"exists": false,
				"type":   entryType(state, date),
				"items":  []map[string]any{},
				"totals": diaryTotals(nil),
			}, nil
		}
		items := diaryItems(day)
		return map[string]any{
			"date":   date,
			"exists": true,
			"type":   getString(day, "type"),
			"items":  items,
			"totals": diaryTotals(items),
		}, nil
	}

	from, to := argString(args, "from"), argString(args, "to")
	limit := argInt(args, "limit", 30)
	dates := make([]string, 0, len(diary))
	for date := range diary {
		if (from != "" && date < from) || (to != "" && date > to) {
			continue
		}
		dates = append(dates, date)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	if limit > 0 && len(dates) > limit {
		dates = dates[:limit]
	}
	days := make([]map[string]any, 0, len(dates))
	for _, date := range dates {
		day := diaryDay(diary, date)
		items := diaryItems(day)
		days = append(days, map[string]any{
			"date":      date,
			"type":      getString(day, "type"),
			"itemCount": len(items),
			"totals":    diaryTotals(items),
		})
	}
	return map[string]any{"count": len(days), "days": days}, nil
}

// toolAddDiaryItem appends a food (with its computed macros) to a day.
func (s *server) toolAddDiaryItem(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	date, err := requireDate(args)
	if err != nil {
		return nil, err
	}
	ref := argString(args, "food")
	if ref == "" {
		return nil, errors.New("food is required (name or id)")
	}
	grams, err := requireGrams(args)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		foods, err := decodeObjects(state["foods"])
		if err != nil {
			return err
		}
		food := findFood(foods, ref)
		if food == nil {
			return fmt.Errorf("food %q not found: add it with add_food first", ref)
		}
		diary, err := decodeDiary(state["diary"])
		if err != nil {
			return err
		}
		day := ensureDiaryDay(diary, state, date)
		items := append(diaryItems(day), makeDiaryItem(food, grams))
		day["items"] = items
		if err := setRaw(state, "diary", diary); err != nil {
			return err
		}
		if err := stampMeta(state, "diary", date); err != nil {
			return err
		}
		result = map[string]any{
			"date":   date,
			"type":   getString(day, "type"),
			"item":   items[len(items)-1],
			"totals": diaryTotals(items),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toolUpdateDiaryItem changes an item's grams and recomputes its macros.
func (s *server) toolUpdateDiaryItem(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	date, err := requireDate(args)
	if err != nil {
		return nil, err
	}
	ref := argString(args, "item")
	if ref == "" {
		return nil, errors.New("item is required (item id or food name)")
	}
	grams, err := requireGrams(args)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		diary, err := decodeDiary(state["diary"])
		if err != nil {
			return err
		}
		day := diaryDay(diary, date)
		if day == nil {
			return fmt.Errorf("no diary for %s", date)
		}
		items := diaryItems(day)
		idx := findDiaryItem(items, ref)
		if idx < 0 {
			return fmt.Errorf("item %q not found on %s", ref, date)
		}
		foods, err := decodeObjects(state["foods"])
		if err != nil {
			return err
		}
		recalcDiaryItem(items[idx], findFood(foods, getString(items[idx], "foodId")), grams)
		day["items"] = items
		if err := setRaw(state, "diary", diary); err != nil {
			return err
		}
		if err := stampMeta(state, "diary", date); err != nil {
			return err
		}
		result = map[string]any{
			"date":   date,
			"type":   getString(day, "type"),
			"item":   items[idx],
			"totals": diaryTotals(items),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toolRemoveDiaryItem drops one item from a day.
func (s *server) toolRemoveDiaryItem(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	date, err := requireDate(args)
	if err != nil {
		return nil, err
	}
	ref := argString(args, "item")
	if ref == "" {
		return nil, errors.New("item is required (item id or food name)")
	}

	var result map[string]any
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		diary, err := decodeDiary(state["diary"])
		if err != nil {
			return err
		}
		day := diaryDay(diary, date)
		if day == nil {
			return fmt.Errorf("no diary for %s", date)
		}
		items := diaryItems(day)
		idx := findDiaryItem(items, ref)
		if idx < 0 {
			return fmt.Errorf("item %q not found on %s", ref, date)
		}
		removed := items[idx]
		items = append(items[:idx], items[idx+1:]...)
		day["items"] = items
		if err := setRaw(state, "diary", diary); err != nil {
			return err
		}
		if err := stampMeta(state, "diary", date); err != nil {
			return err
		}
		result = map[string]any{
			"date":    date,
			"removed": removed,
			"totals":  diaryTotals(items),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toolSetDiaryType marks a day as training or rest, creating it when missing.
func (s *server) toolSetDiaryType(args map[string]any) (any, error) {
	key, err := mcpKey(args)
	if err != nil {
		return nil, err
	}
	date, err := requireDate(args)
	if err != nil {
		return nil, err
	}
	dayType := argString(args, "type")
	if dayType != "training" && dayType != "rest" {
		return nil, errors.New(`type must be "training" or "rest"`)
	}

	var result map[string]any
	err = s.mutateState(key, func(state map[string]json.RawMessage) error {
		diary, err := decodeDiary(state["diary"])
		if err != nil {
			return err
		}
		day := ensureDiaryDay(diary, state, date)
		day["type"] = dayType
		items := diaryItems(day)
		if err := setRaw(state, "diary", diary); err != nil {
			return err
		}
		if err := stampMeta(state, "diary", date); err != nil {
			return err
		}
		result = map[string]any{
			"date":      date,
			"type":      dayType,
			"itemCount": len(items),
			"totals":    diaryTotals(items),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// --- Blob access -------------------------------------------------------------

// readPlain returns the decoded JSON bytes for key, decompressing a gzip blob.
// A missing key is (nil, nil): the app treats an absent blob as empty state.
func (s *server) readPlain(key string) ([]byte, error) {
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if isGzip(data) {
		return gunzipLimit(data, maxBlobBytes)
	}
	return data, nil
}

// loadState materialises the blob-shaped state from the record store, so the
// MCP tools keep speaking the same collections as the browser.
func (s *server) loadState(key string) (map[string]json.RawMessage, error) {
	if err := s.ensureImported(key); err != nil {
		return nil, err
	}
	recs, err := s.store.all(key)
	if err != nil {
		return nil, err
	}
	return stateFromRecords(recs)
}

// mutateState applies fn to the materialised state and persists only the records
// it changed, with the same last-write-wins rule as a client push. The changes
// are broadcast to SSE subscribers so other clients update in real time.
func (s *server) mutateState(key string, fn func(map[string]json.RawMessage) error) error {
	if err := s.ensureImported(key); err != nil {
		return err
	}
	before, err := s.store.all(key)
	if err != nil {
		return err
	}
	state, err := stateFromRecords(before)
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	after, err := recordsFromState(state)
	if err != nil {
		return err
	}
	return s.persistDiff(key, before, after)
}

// persistDiff upserts the records that differ from what is stored (and tombstones
// any record that disappeared), then publishes them.
func (s *server) persistDiff(key string, before, after []record) error {
	prev := recordsByKey(before)
	next := recordsByKey(after)
	var changed []record
	for k, rec := range next {
		if old, ok := prev[k]; ok && recordsEqual(old, rec) {
			continue
		}
		changed = append(changed, rec)
	}
	now := time.Now().UnixMilli()
	for k, old := range prev {
		if old.Deleted {
			continue
		}
		if _, ok := next[k]; !ok {
			changed = append(changed, record{Collection: old.Collection, ID: old.ID, Updated: now, Deleted: true})
		}
	}
	if len(changed) == 0 {
		return nil
	}
	applied, err := s.store.upsert(key, changed)
	if err != nil {
		return err
	}
	s.broadcast(key, applied)
	return nil
}

func recordsByKey(recs []record) map[string]record {
	m := make(map[string]record, len(recs))
	for _, r := range recs {
		m[r.Collection+"\x00"+r.ID] = r
	}
	return m
}

// recordsEqual ignores the payload of tombstones: a deleted row's data is not
// meaningful, only its timestamp and deleted flag are.
func recordsEqual(a, b record) bool {
	if a.Updated != b.Updated || a.Deleted != b.Deleted {
		return false
	}
	if a.Deleted {
		return true
	}
	return string(a.Data) == string(b.Data)
}

// stampMeta marks a record as just-modified in the app's per-record metadata,
// the same way the browser's syncStampChanges does. That is what makes the app
// pick the record up (and keeps a stale tombstone from deleting it).
func stampMeta(state map[string]json.RawMessage, collection, id string) error {
	meta := map[string]any{}
	if raw := state["meta"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &meta); err != nil || meta == nil {
			meta = map[string]any{}
		}
	}
	updated := ensureObject(meta, "updated")
	tombstones := ensureObject(meta, "tombstones")
	collectionUpdated := ensureObject(updated, collection)
	collectionTombstones := ensureObject(tombstones, collection)
	collectionUpdated[id] = time.Now().UnixMilli()
	delete(collectionTombstones, id)
	return setRaw(state, "meta", meta)
}

// --- Small helpers -----------------------------------------------------------

func mcpKey(args map[string]any) (string, error) {
	key := argString(args, "key")
	if key == "" {
		return defaultMCPKey, nil
	}
	if !keyRE.MatchString(key) {
		return "", fmt.Errorf("invalid key %q: use letters, digits, _ or - (max 64)", key)
	}
	return key, nil
}

func decodeObjects(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("invalid record list: %w", err)
	}
	return out, nil
}

func decodeMapKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func requireDate(args map[string]any) (string, error) {
	date := argString(args, "date")
	if date == "" {
		return "", errors.New("date is required (YYYY-MM-DD)")
	}
	return date, nil
}

func requireGrams(args map[string]any) (float64, error) {
	grams, ok := argNumber(args, "grams")
	if !ok || grams <= 0 || math.IsNaN(grams) || math.IsInf(grams, 0) {
		return 0, errors.New("grams must be a positive number")
	}
	return grams, nil
}

// decodeDiary reads the diary map. Days are kept as generic maps so fields the
// MCP does not touch survive a round-trip.
func decodeDiary(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid diary: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func diaryDay(diary map[string]any, date string) map[string]any {
	if d, ok := diary[date].(map[string]any); ok {
		return d
	}
	return nil
}

func diaryItems(day map[string]any) []map[string]any {
	if day == nil {
		return nil
	}
	switch v := day["items"].(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// ensureDiaryDay returns the day, creating it with the type implied by the
// matching weight entry (training by default) when it does not exist yet.
func ensureDiaryDay(diary map[string]any, state map[string]json.RawMessage, date string) map[string]any {
	if day := diaryDay(diary, date); day != nil {
		return day
	}
	day := map[string]any{"type": entryType(state, date), "items": []map[string]any{}}
	diary[date] = day
	return day
}

// entryType mirrors the app: a diary day defaults to the type of the weight
// entry for that date, or "training".
func entryType(state map[string]json.RawMessage, date string) string {
	entries, _ := decodeObjects(state["entries"])
	for _, e := range entries {
		if getString(e, "date") == date {
			if t := getString(e, "type"); t != "" {
				return t
			}
		}
	}
	return "training"
}

// findFood resolves a food reference: id, exact name, then name substring.
func findFood(foods []map[string]any, ref string) map[string]any {
	if ref == "" {
		return nil
	}
	for _, f := range foods {
		if getString(f, "id") == ref {
			return f
		}
	}
	for _, f := range foods {
		if strings.EqualFold(getString(f, "name"), ref) {
			return f
		}
	}
	lower := strings.ToLower(ref)
	for _, f := range foods {
		if strings.Contains(strings.ToLower(getString(f, "name")), lower) {
			return f
		}
	}
	return nil
}

// findDiaryItem resolves an item inside a day: id, exact name, then substring.
func findDiaryItem(items []map[string]any, ref string) int {
	if ref == "" {
		return -1
	}
	for i, it := range items {
		if getString(it, "id") == ref {
			return i
		}
	}
	for i, it := range items {
		if strings.EqualFold(getString(it, "name"), ref) {
			return i
		}
	}
	lower := strings.ToLower(ref)
	for i, it := range items {
		if strings.Contains(strings.ToLower(getString(it, "name")), lower) {
			return i
		}
	}
	return -1
}

// makeDiaryItem denormalises a food into a diary item, exactly like the app
// (protein/fat/carbs to one decimal, calories rounded to an integer).
func makeDiaryItem(food map[string]any, grams float64) map[string]any {
	return map[string]any{
		"id":     newRecordID(),
		"foodId": getString(food, "id"),
		"name":   getString(food, "name"),
		"grams":  grams,
		"p":      round1(getFloat(food, "p") * grams / 100),
		"g":      round1(getFloat(food, "g") * grams / 100),
		"c":      round1(getFloat(food, "c") * grams / 100),
		"kcal":   math.Round(getFloat(food, "kcal") * grams / 100),
	}
}

// recalcDiaryItem rewrites an item's macros for a new weight. It prefers the
// current food's per-100 g values and, when the food is gone, back-computes them
// from the item's own numbers (as the app's recalcItem does).
func recalcDiaryItem(item, food map[string]any, grams float64) {
	per100 := func(k string) float64 {
		if food != nil {
			return getFloat(food, k)
		}
		if old := getFloat(item, "grams"); old > 0 {
			return getFloat(item, k) / old * 100
		}
		return 0
	}
	item["grams"] = grams
	item["p"] = round1(per100("p") * grams / 100)
	item["g"] = round1(per100("g") * grams / 100)
	item["c"] = round1(per100("c") * grams / 100)
	item["kcal"] = math.Round(per100("kcal") * grams / 100)
	if food != nil {
		item["foodId"] = getString(food, "id")
		item["name"] = getString(food, "name")
	}
}

func diaryTotals(items []map[string]any) map[string]any {
	var kcal, p, g, c float64
	for _, it := range items {
		kcal += getFloat(it, "kcal")
		p += getFloat(it, "p")
		g += getFloat(it, "g")
		c += getFloat(it, "c")
	}
	return map[string]any{"kcal": math.Round(kcal), "p": round1(p), "g": round1(g), "c": round1(c)}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func getFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(t), ",", "."), 64)
		return f
	}
	return 0
}

func setRaw(state map[string]json.RawMessage, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	state[key] = b
	return nil
}

func ensureObject(parent map[string]any, key string) map[string]any {
	if v, ok := parent[key].(map[string]any); ok && v != nil {
		return v
	}
	v := map[string]any{}
	parent[key] = v
	return v
}

func sessionNames(workouts []map[string]any) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, w := range workouts {
		s := getString(w, "session")
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		names = append(names, s)
	}
	return names
}

func sessionCounts(workouts []map[string]any) []map[string]any {
	counts := map[string]int{}
	order := []string{}
	for _, w := range workouts {
		s := getString(w, "session")
		if s == "" {
			continue
		}
		if _, ok := counts[s]; !ok {
			order = append(order, s)
		}
		counts[s]++
	}
	out := make([]map[string]any, 0, len(order))
	for _, s := range order {
		out = append(out, map[string]any{"session": s, "exercises": counts[s]})
	}
	return out
}

func hasCategory(food map[string]any, want string) bool {
	for _, tag := range strings.Split(getString(food, "cat"), ",") {
		if strings.EqualFold(strings.TrimSpace(tag), want) {
			return true
		}
	}
	return false
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func getValue(m map[string]any, key string) any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	return v
}

// --- Argument coercion -------------------------------------------------------

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func argNumber(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(t), ",", "."), 64)
		return f, err == nil
	}
	return 0, false
}

func optionalNumber(args map[string]any, key string) any {
	if v, ok := argNumber(args, key); ok {
		return v
	}
	return nil
}

func argInt(args map[string]any, key string, def int) int {
	if v, ok := argNumber(args, key); ok {
		return int(v)
	}
	return def
}

func newRecordID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- MCP results -------------------------------------------------------------

func toolResult(v any) map[string]any {
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		text = []byte(fmt.Sprintf("%v", v))
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": v,
		"isError":           false,
	}
}

func toolErrorResult(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}
