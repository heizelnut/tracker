// Test end-to-end dell'MCP: avvia il vero server Go, fa scrivere un alimento e
// un esercizio via /mcp, poi esegue lo stesso ciclo di sync dell'app
// (GET -> syncMerge -> PUT) e verifica che il dispositivo li riceva senza
// perdere le proprie giornate.
import test from 'node:test';
import assert from 'node:assert';
import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync, spawn} from 'node:child_process';
import net from 'node:net';
import {loadCore} from './extract-core.mjs';

const repo = fileURLToPath(new URL('..', import.meta.url));
const core = loadCore();

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.on('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const p = s.address().port;
      s.close(() => resolve(p));
    });
  });
}

async function waitHealth(base) {
  for (let i = 0; i < 200; i++) {
    try {
      if ((await fetch(base + '/healthz')).ok) return;
    } catch {
      // non ancora pronto
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error('server Go non pronto');
}

async function withServer(t) {
  const tmp = mkdtempSync(join(tmpdir(), 'tracker-mcp-e2e-'));
  const bin = join(tmp, 'tracker');
  execFileSync('go', ['build', '-o', bin, './cmd/server'], {cwd: repo, stdio: 'inherit'});
  const port = await freePort();
  const child = spawn(bin, [], {
    env: {...process.env, PORT: String(port), DATA_DIR: join(tmp, 'data'), STATIC_DIR: repo},
    stdio: 'ignore',
  });
  t.after(() => {
    child.kill('SIGKILL');
    rmSync(tmp, {recursive: true, force: true});
  });
  const base = 'http://127.0.0.1:' + port;
  await waitHealth(base);
  return base;
}

// Stato locale di un dispositivo fittizio, come in sync-e2e.
function device(entries) {
  const meta = core.emptyMeta();
  for (const e of entries) meta.updated.entries[e.date] = Date.parse(e.date);
  return {entries, foods: [], diary: {}, workouts: [], done: {}, meta};
}

function remoteState(p) {
  return {
    entries: Array.isArray(p && p.entries) ? p.entries : [],
    foods: Array.isArray(p && p.foods) ? p.foods : [],
    diary: p && p.diary && typeof p.diary === 'object' ? p.diary : {},
    workouts: Array.isArray(p && p.workouts) ? p.workouts : [],
    done: p && p.done && typeof p.done === 'object' ? p.done : {},
    meta: core.normalizeMeta(p && p.meta),
  };
}

async function put(url, payload) {
  const res = await fetch(url, {
    method: 'PUT',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(payload),
  });
  assert.ok(res.ok, 'PUT ha risposto ' + res.status);
}

async function syncDevice(base, key, local) {
  const url = base + '/api/kv/' + key;
  const res = await fetch(url, {cache: 'no-store'});
  if (res.status === 404) {
    await put(url, local);
    return {state: local, pushed: true};
  }
  assert.equal(res.status, 200);
  const merged = core.syncMerge(local, remoteState(await res.json()));
  if (merged.remoteChanged) await put(url, merged.state);
  return {state: merged.state, localChanged: merged.localChanged, remoteChanged: merged.remoteChanged};
}

let rpcID = 0;
async function mcpCall(base, name, args) {
  const res = await fetch(base + '/mcp', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', Accept: 'application/json, text/event-stream'},
    body: JSON.stringify({jsonrpc: '2.0', id: ++rpcID, method: 'tools/call', params: {name, arguments: args}}),
  });
  assert.equal(res.status, 200);
  const json = await res.json();
  assert.ok(!json.error, 'errore JSON-RPC: ' + JSON.stringify(json.error));
  assert.ok(!json.result.isError, 'errore tool: ' + JSON.stringify(json.result));
  return json.result.structuredContent;
}

test('un alimento e un esercizio aggiunti via MCP arrivano al dispositivo', async (t) => {
  const base = await withServer(t);

  // Il dispositivo ha già una giornata registrata, poi sincronizza.
  let phone = device([{date: '2026-03-01', weight: 70}]);
  phone = (await syncDevice(base, 'default', phone)).state;

  // L'agente scrive nel tracker via MCP.
  const food = await mcpCall(base, 'add_food', {
    name: 'Skyr MCP', p: 11, g: 0.2, c: 4, min: 150, cat: 'proteina,latticino',
  });
  assert.equal(food.created, true);
  const workout = await mcpCall(base, 'add_workout', {
    session: 'Giorno 1 — Upper A', name: 'Panca MCP', sets: '3', type: 'compound', rest: "2-3'",
  });
  assert.equal(workout.created, true);
  assert.equal(workout.workout.type, 'compound');

  // Il dispositivo risincronizza: deve assorbire entrambi, non riscriverli.
  const synced = await syncDevice(base, 'default', phone);
  assert.equal(synced.localChanged, true, 'il dispositivo deve accorgersi delle novità');
  assert.ok(synced.state.foods.some((f) => f.name === 'Skyr MCP'), 'manca il cibo');
  assert.ok(synced.state.workouts.some((w) => w.name === 'Panca MCP'), 'manca l\'esercizio');
  assert.equal(synced.state.entries.length, 1, 'la giornata locale non deve sparire');

  // E il server conserva tutto.
  const final = await (await fetch(base + '/api/kv/default')).json();
  assert.equal(final.entries.length, 1);
  assert.ok(final.foods.some((f) => f.name === 'Skyr MCP'));
  assert.ok(final.workouts.some((w) => w.name === 'Panca MCP'));
});

test('l\'upsert MCP non duplica e il blob resta valido', async (t) => {
  const base = await withServer(t);

  const first = await mcpCall(base, 'add_food', {name: 'Riso MCP', p: 7, g: 1, c: 78});
  const second = await mcpCall(base, 'add_food', {name: 'riso mcp', p: 6.5, g: 0.8, c: 79});
  assert.equal(second.created, false);
  assert.equal(second.food.id, first.food.id);

  const list = await mcpCall(base, 'list_foods', {query: 'riso'});
  assert.equal(list.count, 1, 'l\'upsert non deve duplicare');
});

test('le voci di diario aggiunte via MCP arrivano al dispositivo', async (t) => {
  const base = await withServer(t);

  // Il dispositivo ha una giornata registrata e un diario locale.
  let phone = device([{date: '2026-04-10', weight: 71}]);
  phone.diary['2026-04-10'] = {type: 'training', items: []};
  phone = (await syncDevice(base, 'default', phone)).state;

  // L'agente aggiunge un alimento al database e lo mette nel diario.
  await mcpCall(base, 'add_food', {name: 'Pollo MCP', p: 23, g: 2, c: 0});
  const added = await mcpCall(base, 'add_diary_item', {date: '2026-04-10', food: 'Pollo MCP', grams: 200});
  assert.equal(added.item.p, 46);
  assert.equal(added.totals.p, 46);

  // Il dispositivo risincronizza e deve vedere la voce.
  const synced = await syncDevice(base, 'default', phone);
  assert.equal(synced.localChanged, true, 'il dispositivo deve accorgersi della modifica');
  const day = synced.state.diary['2026-04-10'];
  assert.ok(day, 'manca la giornata di diario');
  assert.equal(day.items.length, 1, 'manca la voce di diario');
  assert.equal(day.items[0].p, 46);

  // Un aggiornamento dei grammi deve propagarsi al giro successivo.
  const updated = await mcpCall(base, 'update_diary_item', {date: '2026-04-10', item: added.item.id, grams: 100});
  assert.equal(updated.item.p, 23);
  const again = await syncDevice(base, 'default', synced.state);
  assert.equal(again.state.diary['2026-04-10'].items[0].p, 23);

  // E anche la rimozione.
  await mcpCall(base, 'remove_diary_item', {date: '2026-04-10', item: 'Pollo MCP'});
  const afterRemove = await syncDevice(base, 'default', again.state);
  assert.equal(afterRemove.state.diary['2026-04-10'].items.length, 0);
});
