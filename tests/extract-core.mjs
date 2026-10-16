// Estrae le funzioni pure di merge/sync da index.html e le valuta in un
// contesto Node isolato. Così l'app resta un singolo file, ma la logica
// critica (quella che decide se una giornata viene persa) è testabile.
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import vm from 'node:vm';

const START = '/* SYNC-CORE-START */';
const END = '/* SYNC-CORE-END */';

export function loadCore() {
  const file = fileURLToPath(new URL('../index.html', import.meta.url));
  const html = readFileSync(file, 'utf8');
  const start = html.indexOf(START);
  const end = html.indexOf(END);
  if (start < 0 || end < 0 || end < start) {
    throw new Error('marcatori SYNC-CORE non trovati in index.html');
  }
  const code = html.slice(start + START.length, end);
  const ctx = vm.createContext({});
  vm.runInContext(
    code +
      '\n;globalThis.__syncCore={' +
      'SYNC_COLLECTIONS,emptyMeta,normalizeMeta,syncItems,syncBuildValue,' +
      'syncStable,syncRecordEqual,syncInferTs,syncStampChanges,syncBetter,' +
      'syncMerge,syncGc};',
    ctx,
  );
  return ctx.__syncCore;
}
