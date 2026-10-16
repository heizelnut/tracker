// Test end-to-end: avvia il vero server Go e simula due dispositivi che
// sincronizzano. Riproduce il bug segnalato ("perdo giornate intere") e
// verifica che con il merge per-record non si perda più nulla.
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
      const r = await fetch(base + '/healthz');
      if (r.ok) return;
    } catch {
      // ancora su
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error('server Go non pronto');
}

// Stato locale di un dispositivo fittizio.
function device(entries) {
  const meta = core.emptyMeta();
  for (const e of entries) meta.updated.entries[e.date] = Date.parse(e.date);
  return {entries, foods: [], diary: {}, workouts: [], done: {}, meta};
}

// Come il client: normalizza il payload remoto (retro-compatibile).
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

// Un ciclo di sync identico a quello dell'app: GET -> merge -> PUT se serve.
async function syncDevice(base, key, local) {
  const url = base + '/api/kv/' + key;
  const res = await fetch(url, {cache: 'no-store'});
  if (res.status === 404) {
    await put(url, local);
    return {state: local, pushed: true};
  }
  assert.equal(res.status, 200);
  const remote = await res.json();
  const merged = core.syncMerge(local, remoteState(remote));
  if (merged.remoteChanged) await put(url, merged.state);
  return {state: merged.state, localChanged: merged.localChanged, remoteChanged: merged.remoteChanged};
}

async function withServer(t) {
  const tmp = mkdtempSync(join(tmpdir(), 'tracker-e2e-'));
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

test('un dispositivo rimasto indietro non cancella le giornate sul server', async (t) => {
  const base = await withServer(t);

  const days = (from, to) =>
    Array.from({length: to - from + 1}, (_, i) => ({
      date: '2026-01-' + String(from + i).padStart(2, '0'),
      weight: 70 + (from + i) / 10,
    }));

  // Il telefono è indietro: conosce solo i primi 3 giorni.
  let phone = device(days(1, 3));
  const firstPhone = await syncDevice(base, 'default', phone);
  phone = firstPhone.state;

  // Il PC è avanti: ha 5 giorni. Sincronizza dopo il telefono.
  let pc = device(days(1, 5));
  const firstPc = await syncDevice(base, 'default', pc);
  pc = firstPc.state;
  assert.equal(pc.entries.length, 5);

  // Il telefono torna online: deve assorbire i giorni 4-5, non cancellarli.
  const secondPhone = await syncDevice(base, 'default', phone);
  assert.equal(secondPhone.state.entries.length, 5, 'il telefono deve avere tutte le giornate');
  assert.equal(secondPhone.localChanged, true, 'il telefono deve ricevere i giorni mancanti');

  // Il server deve conservare tutte le giornate.
  const final = await (await fetch(base + '/api/kv/default')).json();
  assert.equal(final.entries.length, 5, 'il server non deve perdere giornate');
  assert.ok(final.meta, 'il blob sincronizzato deve contenere i metadati di merge');
});

test('modifiche concorrenti su giorni diversi sopravvivono a entrambi i dispositivi', async (t) => {
  const base = await withServer(t);

  let a = device([{date: '2026-02-01', weight: 71}]);
  let b = device([{date: '2026-02-02', weight: 72}]);

  const ra = await syncDevice(base, 'shared', a);
  a = ra.state;
  const rb = await syncDevice(base, 'shared', b);
  b = rb.state;

  // Un altro giro per far convergere entrambi.
  a = (await syncDevice(base, 'shared', a)).state;
  b = (await syncDevice(base, 'shared', b)).state;

  assert.deepEqual(a.entries.map((e) => e.date).sort(), ['2026-02-01', '2026-02-02']);
  assert.deepEqual(b.entries.map((e) => e.date).sort(), ['2026-02-01', '2026-02-02']);
});
