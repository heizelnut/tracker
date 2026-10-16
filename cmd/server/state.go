package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file is the bridge between the app's blob-shaped state (the collections
// the browser and the MCP tools speak) and the flat record rows in SQLite.
// Keeping one canonical mapping in Go means migration, the compatibility blob
// view and the incremental API all agree on what a record is.

// collectionNames mirrors SYNC_COLLECTIONS in index.html.
var collectionNames = []string{"entries", "foods", "diary", "workouts", "done"}

// stateFromRecords rebuilds the blob-style state (entries/foods/diary/workouts/
// done + meta) from stored records. Non-deleted records become the collections;
// deleted ones become tombstones in meta.
func stateFromRecords(recs []record) (map[string]json.RawMessage, error) {
	entries := []map[string]any{}
	foods := []map[string]any{}
	workouts := []map[string]any{}
	diary := map[string]any{}
	done := map[string]map[string]bool{}
	updated := map[string]map[string]int64{}
	tombstones := map[string]map[string]int64{}
	for _, name := range collectionNames {
		updated[name] = map[string]int64{}
		tombstones[name] = map[string]int64{}
	}
	ensure := func(coll string) {
		if updated[coll] == nil {
			updated[coll] = map[string]int64{}
		}
		if tombstones[coll] == nil {
			tombstones[coll] = map[string]int64{}
		}
	}

	for _, rec := range recs {
		ensure(rec.Collection)
		if rec.Deleted {
			tombstones[rec.Collection][rec.ID] = rec.Updated
			continue
		}
		updated[rec.Collection][rec.ID] = rec.Updated
		data, err := unmarshalObject(rec.Data)
		if err != nil {
			return nil, err
		}
		switch rec.Collection {
		case "entries":
			entries = append(entries, data)
		case "foods":
			foods = append(foods, data)
		case "workouts":
			workouts = append(workouts, data)
		case "diary":
			diary[rec.ID] = data
		case "done":
			date, id := splitDoneKey(rec.ID)
			if done[date] == nil {
				done[date] = map[string]bool{}
			}
			done[date][id] = true
		}
	}

	sortByName := func(list []map[string]any) {
		sort.SliceStable(list, func(i, j int) bool {
			return strings.ToLower(getString(list[i], "name")) < strings.ToLower(getString(list[j], "name"))
		})
	}
	sortByDate := func(list []map[string]any) {
		sort.SliceStable(list, func(i, j int) bool {
			return getString(list[i], "date") < getString(list[j], "date")
		})
	}
	sortByDate(entries)
	sortByName(foods)
	sort.SliceStable(workouts, func(i, j int) bool {
		si, sj := getString(workouts[i], "session"), getString(workouts[j], "session")
		if si != sj {
			return si < sj
		}
		return strings.ToLower(getString(workouts[i], "name")) < strings.ToLower(getString(workouts[j], "name"))
	})

	meta, err := json.Marshal(map[string]any{"updated": updated, "tombstones": tombstones})
	if err != nil {
		return nil, err
	}
	return map[string]json.RawMessage{
		"entries":  mustRaw(entries),
		"foods":    mustRaw(foods),
		"diary":    mustRaw(diary),
		"workouts": mustRaw(workouts),
		"done":     mustRaw(done),
		"meta":     meta,
	}, nil
}

// recordsFromState is the inverse: it flattens a mutated state into records,
// taking each record's timestamp from meta.updated and falling back to a
// date-derived timestamp for legacy blobs that predate the metadata.
func recordsFromState(state map[string]json.RawMessage) ([]record, error) {
	meta := parseMeta(state["meta"])
	out := []record{}

	stamp := func(coll, id string, data map[string]any) int64 {
		if ts := meta.updatedFor(coll, id); ts > 0 {
			return ts
		}
		return inferTS(coll, id, data)
	}

	for _, coll := range []string{"entries", "foods", "workouts"} {
		objs, err := decodeObjects(state[coll])
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			id := recordKey(coll, o)
			if id == "" {
				continue
			}
			out = append(out, record{Collection: coll, ID: id, Data: mustRaw(o), Updated: stamp(coll, id, o)})
		}
	}

	for date, day := range decodeDiaryMap(state["diary"]) {
		out = append(out, record{Collection: "diary", ID: date, Data: mustRaw(day), Updated: stamp("diary", date, day)})
	}

	for date, ids := range decodeDone(state["done"]) {
		for id := range ids {
			recID := doneKey(date, id)
			data := map[string]any{"date": date, "id": id}
			out = append(out, record{Collection: "done", ID: recID, Data: mustRaw(data), Updated: stamp("done", recID, data)})
		}
	}

	for coll, ids := range meta.tombstones {
		for id, ts := range ids {
			out = append(out, record{Collection: coll, ID: id, Updated: ts, Deleted: true})
		}
	}
	return out, nil
}

// recordKey mirrors syncItems() in the browser: the field that identifies a
// record inside its collection.
func recordKey(collection string, data map[string]any) string {
	switch collection {
	case "entries":
		return getString(data, "date")
	case "foods", "workouts":
		return getString(data, "id")
	}
	return ""
}

func doneKey(date, id string) string { return date + "\x00" + id }
func splitDoneKey(k string) (string, string) {
	date, id, _ := strings.Cut(k, "\x00")
	return date, id
}

// inferTS gives legacy blobs (no meta.updated) a stable timestamp: midday of
// the record's date, or 1 when there is no date. Mirrors syncInferTs().
func inferTS(collection, id string, data map[string]any) int64 {
	var date string
	switch collection {
	case "entries":
		date = getString(data, "date")
	case "diary":
		date = id
	case "done":
		date, _ = splitDoneKey(id)
	default:
		return 1
	}
	if t, err := time.Parse("2006-01-02", date); err == nil {
		return t.Add(12 * time.Hour).UnixMilli()
	}
	return 1
}

// --- meta --------------------------------------------------------------------

type syncMetaView struct {
	updated    map[string]map[string]int64
	tombstones map[string]map[string]int64
}

func (m syncMetaView) updatedFor(coll, id string) int64 {
	if ids, ok := m.updated[coll]; ok {
		return ids[id]
	}
	return 0
}

func parseMeta(raw json.RawMessage) syncMetaView {
	m := syncMetaView{updated: map[string]map[string]int64{}, tombstones: map[string]map[string]int64{}}
	if len(raw) == 0 {
		return m
	}
	var tmp struct {
		Updated    map[string]map[string]int64 `json:"updated"`
		Tombstones map[string]map[string]int64 `json:"tombstones"`
	}
	if json.Unmarshal(raw, &tmp) != nil {
		return m
	}
	if tmp.Updated != nil {
		m.updated = tmp.Updated
	}
	if tmp.Tombstones != nil {
		m.tombstones = tmp.Tombstones
	}
	return m
}

// --- small decoders ----------------------------------------------------------

func decodeDiaryMap(raw json.RawMessage) map[string]map[string]any {
	out := map[string]map[string]any{}
	if len(raw) == 0 {
		return out
	}
	var m map[string]map[string]any
	if json.Unmarshal(raw, &m) == nil && m != nil {
		return m
	}
	return out
}

func decodeDone(raw json.RawMessage) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if len(raw) == 0 {
		return out
	}
	var m map[string]map[string]bool
	if json.Unmarshal(raw, &m) == nil && m != nil {
		return m
	}
	return out
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// importBlob folds a legacy blob into the record store. It is the bridge that
// lets a browser still running the old client migrate simply by syncing.
func (s *server) importBlob(key string, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	state := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("invalid blob: %w", err)
	}
	recs, err := recordsFromState(state)
	if err != nil {
		return err
	}
	_, err = s.store.upsert(key, recs)
	return err
}
