// Test end-to-end del protocollo a record: avvia il vero server Go, simula
// dispositivi che fanno push incrementale e pull con `since`, e verifica che il
// merge del client (syncMerge + syncDeltaState) li faccia convergere. Copre
// anche il push in tempo reale via SSE.
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
  const tmp = mkdtempSync(join(tmpdir(), 'tracker-records-'));
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

const rec = (collection, id, data, updated) => ({collection, id, data, updated});
const tomb = (collection, id, updated) => ({collection, id, deleted: true, updated});
const device = () => ({entries: [], foods: [], diary: {}, workouts: [], lifts: [], done: {}, meta: core.emptyMeta()});

async function push(base, key, records) {
  const res = await fetch(`${base}/api/sync/${key}`, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({records}),
  });
  assert.equal(res.status, 204, 'push status');
}

async function pull(base, key, since) {
  const res = await fetch(`${base}/api/sync/${key}?since=${since}`);
  assert.equal(res.status, 200);
  return res.json();
}

// Come il client: costruisce lo stato remoto dal delta e lo unisce al locale.
function applyDelta(local, delta) {
  return core.syncMerge(local, core.syncDeltaState(delta)).state;
}

test('un dispositivo nuovo riceve i record dal server', async (t) => {
  const base = await withServer(t);
  await push(base, 'default', [
    rec('entries', '2026-01-01', {date: '2026-01-01', weight: 70}, 100),
    rec('foods', 'f1', {id: 'f1', name: 'Pollo', p: 23}, 100),
  ]);

  let b = device();
  const delta = await pull(base, 'default', 0);
  assert.equal(delta.records.length, 2);
  b = applyDelta(b, delta);
  assert.equal(b.entries.length, 1);
  assert.ok(b.foods.some((f) => f.id === 'f1'));

  // Nessuna novità oltre il watermark.
  assert.equal((await pull(base, 'default', delta.serverTime)).records.length, 0);
});

test('le eliminazioni arrivano come tombstone', async (t) => {
  const base = await withServer(t);
  await push(base, 'del', [rec('foods', 'f1', {id: 'f1', name: 'Pollo'}, 100)]);
  let b = applyDelta(device(), await pull(base, 'del', 0));
  assert.equal(b.foods.length, 1);

  await push(base, 'del', [tomb('foods', 'f1', 200)]);
  b = applyDelta(b, await pull(base, 'del', 100));
  assert.equal(b.foods.length, 0, 'il cibo eliminato sparisce');
  assert.equal(b.meta.tombstones.foods.f1, 200);
});

test('due dispositivi convergono con delta separati', async (t) => {
  const base = await withServer(t);

  // A crea una giornata; B ne crea un'altra senza aver visto A.
  await push(base, 'shared', [rec('entries', '2026-01-01', {date: '2026-01-01', weight: 70}, 100)]);
  let b = applyDelta(device(), await pull(base, 'shared', 0));
  await push(base, 'shared', [rec('entries', '2026-01-02', {date: '2026-01-02', weight: 71}, 200)]);

  // A torna e prende solo il delta nuovo; B riprende dal suo watermark.
  const deltaB = await pull(base, 'shared', 100);
  assert.deepEqual(deltaB.records.map((r) => r.id), ['2026-01-02']);
  b = applyDelta(b, deltaB);

  assert.deepEqual(b.entries.map((e) => e.date).sort(), ['2026-01-01', '2026-01-02']);
});

test('SSE spinge le modifiche in tempo reale', async (t) => {
  const base = await withServer(t);
  const controller = new AbortController();
  const res = await fetch(`${base}/api/events/live`, {
    headers: {Accept: 'text/event-stream'},
    signal: controller.signal,
  });
  assert.equal(res.headers.get('content-type'), 'text/event-stream');

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  async function nextEvent(timeoutMs = 5000) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const idx = buffer.indexOf('\n\n');
      if (idx >= 0) {
        const block = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        for (const line of block.split('\n')) {
          if (line.startsWith('data: ')) return JSON.parse(line.slice(6));
        }
        continue;
      }
      const {value, done} = await Promise.race([
        reader.read(),
        new Promise((_, reject) => setTimeout(() => reject(new Error('timeout SSE')), deadline - Date.now())),
      ]);
      if (done) throw new Error('stream chiuso');
      buffer += decoder.decode(value, {stream: true});
    }
    throw new Error('timeout SSE');
  }

  setTimeout(() => {
    push(base, 'live', [rec('entries', '2026-02-02', {date: '2026-02-02', weight: 69}, 300)]).catch(() => {});
  }, 100);

  const event = await nextEvent();
  assert.ok(event.records.some((r) => r.id === '2026-02-02'), 'evento SSE mancante: ' + JSON.stringify(event));
  controller.abort();
});
