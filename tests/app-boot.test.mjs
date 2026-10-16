// Smoke test: esegue l'intero script di index.html in un DOM finto per
// verificare che l'app si avvii senza errori con il nuovo motore di sync e che
// un salvataggio venga marcato come "da inviare".
import test from 'node:test';
import assert from 'node:assert';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import vm from 'node:vm';

function makeClassList() {
  const set = new Set();
  return {
    add: (...c) => c.forEach((x) => set.add(x)),
    remove: (...c) => c.forEach((x) => set.delete(x)),
    toggle: (c, force) => {
      const on = force === undefined ? !set.has(c) : !!force;
      if (on) set.add(c);
      else set.delete(c);
      return on;
    },
    contains: (c) => set.has(c),
  };
}

function makeCtx2d() {
  return {
    scale() {}, clearRect() {}, beginPath() {}, moveTo() {}, lineTo() {},
    stroke() {}, arc() {}, fill() {}, fillText() {},
    strokeStyle: '', lineWidth: 0, lineJoin: '', fillStyle: '', font: '',
  };
}

function makeEl() {
  const target = {
    value: '', textContent: '', innerHTML: '', hidden: false, checked: false,
    disabled: false, placeholder: '', download: '', href: '', width: 0, height: 0,
    className: '', id: '', style: {}, dataset: {}, classList: makeClassList(),
    addEventListener() {}, removeEventListener() {}, appendChild() {}, remove() {},
    focus() {}, blur() {}, click() {}, scrollIntoView() {}, contains() { return false; },
    querySelectorAll() { return []; }, querySelector() { return makeEl(); },
    closest() { return makeEl(); }, setAttribute() {}, getAttribute() { return null; },
    insertBefore() {}, prepend() {}, after() {},
    getContext() { return makeCtx2d(); },
    getBoundingClientRect() { return {width: 800, height: 270, top: 0, left: 0}; },
    requestFullscreen() { return Promise.resolve(); },
  };
  return new Proxy(target, {
    get(obj, prop) {
      if (prop in obj) return obj[prop];
      if (typeof prop === 'symbol') return undefined;
      return () => undefined; // qualunque metodo non previsto: no-op
    },
    set(obj, prop, val) { obj[prop] = val; return true; },
  });
}

function makeDom() {
  const registry = new Map();
  const document = {
    getElementById(id) {
      if (!registry.has(id)) registry.set(id, makeEl());
      return registry.get(id);
    },
    querySelectorAll() { return []; },
    querySelector() { return makeEl(); },
    createElement() { return makeEl(); },
    addEventListener() {},
    documentElement: makeEl(),
    body: makeEl(),
    fullscreenElement: null,
    exitFullscreen() {},
  };
  return document;
}

test('index.html si avvia e marca le modifiche come da sincronizzare', () => {
  const html = readFileSync(fileURLToPath(new URL('../index.html', import.meta.url)), 'utf8');
  const script = html.match(/<script>([\s\S]*)<\/script>/)[1];

  const store = new Map();
  const localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  };

  const sandbox = {
    document: makeDom(),
    window: {addEventListener() {}, devicePixelRatio: 1},
    localStorage,
    navigator: {onLine: true, vibrate() {}},
    fetch: () => Promise.reject(new Error('rete non disponibile nel test')),
    setTimeout: () => 0,
    clearTimeout() {},
    setInterval: () => 0,
    clearInterval() {},
    alert() {},
    confirm: () => true,
    getComputedStyle: () => ({getPropertyValue: () => '#000'}),
    crypto: globalThis.crypto,
    URL: globalThis.URL,
    Blob: globalThis.Blob,
    Response: globalThis.Response,
    AbortController: globalThis.AbortController,
    CompressionStream: globalThis.CompressionStream,
    console,
  };
  const ctx = vm.createContext(sandbox);

  // Espone gli interni per poter simulare un salvataggio dopo l'avvio.
  vm.runInContext(
    script +
      '\n;globalThis.__app={save,backupData,pendingCount,applyDelta,outboxRecords,KEY,' +
      'getEntries:()=>entries,getFoods:()=>foods,getMeta:()=>meta,' +
      'addEntry:(rec)=>{entries=[...entries,rec];save(KEY,entries)}};',
    ctx,
  );

  const app = ctx.__app;
  assert.equal(app.getEntries().length, 0);

  app.addEntry({id: 'x', date: '2026-01-01', weight: 70, calories: 1800, type: 'training'});

  const meta = app.getMeta();
  assert.ok(meta.updated.entries['2026-01-01'], 'il record salvato deve avere un timestamp');
  assert.ok(app.pendingCount() >= 1, 'il record salvato deve risultare in attesa di invio');

  // L'outbox contiene il record da inviare, con i suoi dati.
  const out = app.outboxRecords();
  assert.equal(out.recs.length, 1);
  assert.equal(out.recs[0].collection, 'entries');
  assert.equal(out.recs[0].data.date, '2026-01-01');
  assert.equal(out.recs[0].updated, meta.updated.entries['2026-01-01']);

  // Un delta del server viene applicato al dispositivo.
  app.applyDelta({records: [
    {collection: 'foods', id: 'f1', data: {id: 'f1', name: 'Pollo', p: 23}, updated: 9_000_000_000_000},
  ]});
  assert.ok(app.getFoods().some((f) => f.id === 'f1'), 'il client deve applicare il delta del server');

  const backup = app.backupData();
  assert.equal(backup.version, 4);
  assert.ok(backup.meta, 'il backup deve includere i metadati di merge');
  assert.equal(backup.entries.length, 1);
});
