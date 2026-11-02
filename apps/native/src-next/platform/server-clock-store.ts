import {restoreServerClock} from '../../../../packages/client-core/src/server-clock';
import {NativeQueueStore} from './queue-storage';

/** The last offset this device learned between its clock and the servers', kept so work that
 * judges a stored server-issued instant before any server has answered (a remembered sign-in,
 * a queued revocation, a download) still reads it the way the server does. Importing this
 * module keeps the offset saved as it changes and restores it once the store has been read. */
const KEY = 'server-clock.v1';
let saved: number | undefined;
function save(offset: number): void {
  if (saved !== undefined && Math.abs(offset - saved) < 2_000) return;
  saved = offset;
  void new NativeQueueStore().change(KEY, () => ({record: {offset}, value: undefined})).catch(() => { /* A refused store only costs the first read after a restart. */ });
}
restoreServerClock(undefined, save);
void new NativeQueueStore().change(KEY, raw => ({record: raw, value: raw as {offset?: unknown} | null})).then(raw => {
  const offset = raw?.offset;
  if (typeof offset === 'number' && Number.isFinite(offset)) { saved = offset; restoreServerClock(offset, save); }
}).catch(() => { /* Unreadable means no restored offset; the first answer teaches it. */ });
