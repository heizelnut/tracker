// Test unitari della logica di merge per-record.
//
// Il caso che ha motivato tutto: un dispositivo rimasto indietro non deve
// poter cancellare le giornate registrate su un altro dispositivo. Il vecchio
// sync sostituiva l'intero blob (last-write-wins sul blob), quindi il telefono
// fermo a 5 giorni poteva sovrascrivere il PC arrivato a 11.
import test from 'node:test';
import assert from 'node:assert';
import {loadCore} from './extract-core.mjs';

const core = loadCore();
const {syncMerge, emptyMeta, syncStampChanges, syncChangedKeys, syncDeltaState, syncInferTs, SYNC_COLLECTIONS} = core;

// Costruisce uno "stato dispositivo" come lo vede il client.
function state(init = {}) {
  const meta = emptyMeta();
  const s = {entries: [], foods: [], diary: {}, workouts: [], lifts: [], done: {}, meta};
  for (const k of ['entries', 'foods', 'diary', 'workouts', 'lifts', 'done']) {
    if (init[k] !== undefined) s[k] = init[k];
  }
  for (const c of SYNC_COLLECTIONS) {
    Object.assign(meta.updated[c.name], (init.updated && init.updated[c.name]) || {});
    Object.assign(meta.tombstones[c.name], (init.tombstones && init.tombstones[c.name]) || {});
  }
  return s;
}

test('un dispositivo indietro non cancella le giornate più recenti', () => {
  const local = state({
    entries: [
      {date: '2026-01-01', weight: 70},
      {date: '2026-01-02', weight: 70.2},
    ],
    updated: {entries: {'2026-01-01': 10, '2026-01-02': 20}},
  });
  const remote = state({
    entries: [
      {date: '2026-01-01', weight: 70},
      {date: '2026-01-02', weight: 70.2},
      {date: '2026-01-03', weight: 70.4},
      {date: '2026-01-04', weight: 70.6},
      {date: '2026-01-05', weight: 70.8},
    ],
    updated: {entries: {
      '2026-01-01': 10, '2026-01-02': 20, '2026-01-03': 30,
      '2026-01-04': 40, '2026-01-05': 50,
    }},
  });

  const {state: merged, localChanged, remoteChanged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 5, 'nessuna giornata deve sparire');
  assert.deepEqual(
    merged.entries.map((e) => e.date),
    ['2026-01-01', '2026-01-02', '2026-01-03', '2026-01-04', '2026-01-05'],
  );
  assert.equal(localChanged, true, 'il dispositivo locale riceve i giorni mancanti');
  assert.equal(remoteChanged, false, 'se il server è già completo non va riscritto');
});

test('la modifica locale più recente vince sul record remoto più vecchio', () => {
  const local = state({entries: [{date: '2026-01-03', weight: 99}], updated: {entries: {'2026-01-03': 100}}});
  const remote = state({entries: [{date: '2026-01-03', weight: 70}], updated: {entries: {'2026-01-03': 50}}});

  const {state: merged, remoteChanged} = syncMerge(local, remote);

  assert.equal(merged.entries[0].weight, 99);
  assert.equal(remoteChanged, true, 'il server deve ricevere la modifica più recente');
});

test('una eliminazione locale cancella il record sul server', () => {
  const local = state({tombstones: {entries: {'2026-01-03': 100}}});
  const remote = state({entries: [{date: '2026-01-03', weight: 70}], updated: {entries: {'2026-01-03': 50}}});

  const {state: merged, remoteChanged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 0);
  assert.equal(merged.meta.tombstones.entries['2026-01-03'], 100);
  assert.equal(remoteChanged, true);
});

test('un record ricreato più di recente batte la tombstone', () => {
  const local = state({tombstones: {entries: {'2026-01-03': 50}}});
  const remote = state({entries: [{date: '2026-01-03', weight: 70}], updated: {entries: {'2026-01-03': 100}}});

  const {state: merged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 1);
  assert.equal(merged.meta.tombstones.entries['2026-01-03'], undefined);
});

test('a parità di timestamp la tombstone batte il record', () => {
  const local = state({tombstones: {entries: {'2026-01-03': 100}}});
  const remote = state({entries: [{date: '2026-01-03', weight: 70}], updated: {entries: {'2026-01-03': 100}}});

  const {state: merged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 0);
});

test('gli alimenti presenti su un solo dispositivo vengono uniti', () => {
  const local = state({foods: [{id: 'a', name: 'Pollo'}, {id: 'b', name: 'Riso'}], updated: {foods: {a: 10, b: 10}}});
  const remote = state({foods: [{id: 'b', name: 'Riso'}, {id: 'c', name: 'Uova'}], updated: {foods: {b: 10, c: 10}}});

  const {state: merged} = syncMerge(local, remote);

  assert.deepEqual(merged.foods.map((f) => f.id).sort(), ['a', 'b', 'c']);
});

test('le spunte degli allenamenti si uniscono per esercizio, non per giornata', () => {
  const local = state({done: {'2026-01-10': {ex1: true}}, updated: {done: {'2026-01-10\u0000ex1': 100}}});
  const remote = state({done: {'2026-01-10': {ex2: true}}, updated: {done: {'2026-01-10\u0000ex2': 50}}});

  const {state: merged} = syncMerge(local, remote);

  assert.deepEqual(merged.done, {'2026-01-10': {ex1: true, ex2: true}});
});

test('il diario si unisce per data', () => {
  const local = state({diary: {'2026-01-10': {type: 'training', items: [{id: 'i1'}]}}, updated: {diary: {'2026-01-10': 100}}});
  const remote = state({diary: {'2026-01-11': {type: 'rest', items: [{id: 'i2'}]}}, updated: {diary: {'2026-01-11': 100}}});

  const {state: merged} = syncMerge(local, remote);

  assert.deepEqual(Object.keys(merged.diary).sort(), ['2026-01-10', '2026-01-11']);
});

test('i dati legacy senza meta vengono uniti senza perdere nulla', () => {
  const local = state({entries: [{date: '2026-01-01', weight: 70}]});
  const remote = state({entries: [{date: '2026-01-01', weight: 71}, {date: '2026-01-02', weight: 72}]});

  const {state: merged, localChanged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 2);
  assert.equal(localChanged, true);
});

test('syncStampChanges marca nuovi/modificati e crea tombstone per i rimossi', () => {
  const col = SYNC_COLLECTIONS.find((c) => c.name === 'entries');
  const meta = emptyMeta();
  const prev = [{date: '2026-01-01', weight: 70}, {date: '2026-01-02', weight: 71}];
  const next = [{date: '2026-01-01', weight: 70}, {date: '2026-01-03', weight: 72}];

  const changed = syncStampChanges(col, prev, next, meta, 500);

  assert.equal(changed, true);
  assert.equal(meta.updated.entries['2026-01-01'], undefined, 'record invariato: nessun nuovo timestamp');
  assert.equal(meta.updated.entries['2026-01-03'], 500);
  assert.equal(meta.tombstones.entries['2026-01-02'], 500);
  assert.equal(meta.updated.entries['2026-01-02'], undefined);
});

test('il merge converge: riapplicarlo non cambia più lo stato locale', () => {
  const local = state({entries: [{date: '2026-01-01', weight: 70}], updated: {entries: {'2026-01-01': 100}}});
  const remote = state({entries: [{date: '2026-01-02', weight: 71}], updated: {entries: {'2026-01-02': 200}}});

  const first = syncMerge(local, remote).state;
  const again = syncMerge(first, remote);

  assert.deepEqual(again.state.entries, first.entries);
  assert.equal(again.localChanged, false);
});

test('syncInferTs usa mezzanotte UTC, non mezzogiorno', () => {
  const ts = syncInferTs('diary', '2026-01-10', null);
  assert.equal(ts, Date.parse('2026-01-10T00:00:00Z'));
  assert.ok(ts < Date.parse('2026-01-10T12:00:00Z'), 'un record inferito non deve sembrare più recente di una modifica reale');
});

test('syncChangedKeys riporta nuovi, modificati e rimossi', () => {
  const col = SYNC_COLLECTIONS.find((c) => c.name === 'entries');
  const prev = [{date: '2026-01-01', weight: 70}, {date: '2026-01-02', weight: 71}];
  const next = [{date: '2026-01-01', weight: 70}, {date: '2026-01-03', weight: 72}];

  assert.deepEqual(syncChangedKeys(col, prev, next).sort(), ['2026-01-02', '2026-01-03']);
});

test('un delta server si applica con lo stesso merge', () => {
  const local = state({entries: [{date: '2026-01-01', weight: 70}], updated: {entries: {'2026-01-01': 100}}});
  const delta = {records: [
    {collection: 'entries', id: '2026-01-01', data: {date: '2026-01-01', weight: 71}, updated: 200},
    {collection: 'foods', id: 'f1', data: {id: 'f1', name: 'Pollo'}, updated: 200},
    {collection: 'foods', id: 'gone', deleted: true, updated: 200},
  ]};

  const {state: merged, localChanged} = syncMerge(local, syncDeltaState(delta));

  assert.equal(localChanged, true);
  assert.equal(merged.entries[0].weight, 71, 'il record più recente del server vince');
  assert.ok(merged.foods.some((f) => f.id === 'f1'), 'il nuovo record arriva');
  assert.equal(merged.meta.tombstones.foods.gone, 200, 'la tombstone arriva');
});

test('un delta server non tocca i record locali che non contiene', () => {
  const local = state({entries: [{date: '2026-01-01', weight: 70}], updated: {entries: {'2026-01-01': 100}}});
  const remote = syncDeltaState({records: [{collection: 'foods', id: 'f1', data: {id: 'f1', name: 'Pollo'}, updated: 200}]});

  const {state: merged} = syncMerge(local, remote);

  assert.equal(merged.entries.length, 1, 'il record locale resta');
});

test('i set registrati (rep max) si uniscono per id', () => {
  const local = state({lifts: [{id: 'l1', workoutId: 'w1', name: 'Panca', weight: 80, reps: 5}], updated: {lifts: {l1: 100}}});
  const remote = state({lifts: [{id: 'l2', workoutId: 'w1', name: 'Panca', weight: 85, reps: 3}], updated: {lifts: {l2: 200}}});

  const {state: merged} = syncMerge(local, remote);

  assert.deepEqual(merged.lifts.map((l) => l.id).sort(), ['l1', 'l2']);
});

test('un set (rep max) eliminato viaggia come tombstone', () => {
  const local = state({lifts: [{id: 'l1', weight: 80, reps: 5}], updated: {lifts: {l1: 100}}});
  const remote = state({tombstones: {lifts: {l1: 200}}});

  const {state: merged} = syncMerge(local, remote);

  assert.equal(merged.lifts.length, 0);
  assert.equal(merged.meta.tombstones.lifts.l1, 200);
});
