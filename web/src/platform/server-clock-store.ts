import {restoreServerClock} from '@core/server-clock.ts';

/** The last offset this browser learned between its clock and the servers', kept so a page that
 * loads before any server answers (a refresh, a provider return) still reads server-issued
 * instants the way the server does. Importing this module restores it and keeps it saved. */
const KEY = 'portico.server-clock.v1';
function read(): number | undefined {
  try {
    const raw = globalThis.localStorage?.getItem(KEY);
    const value = raw === null || raw === undefined ? NaN : Number(raw);
    return Number.isFinite(value) ? value : undefined;
  } catch { return undefined; }
}
let saved = read();
restoreServerClock(saved, offset => {
  if (saved !== undefined && Math.abs(offset - saved) < 2_000) return;
  saved = offset;
  try { globalThis.localStorage?.setItem(KEY, String(offset)); } catch { /* A refused store only costs the first read after a reload. */ }
});
