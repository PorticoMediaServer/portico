import type {PorticoAccountScope} from '@core/app-sessions.ts';
import {browserAppSessionActions} from './app-sessions';
import {porticoAccountHolder} from './portico-account-holder';
import {browserAppSessions, restoreBrowserConnection} from './server-connections';

/** The servers this browser opened with its Portico Account last only as long as that sign-in.
 * However it ends (Sign out here or on the account pages, "Not you?", this device signed out in
 * Devices, a password reset, another tab, a renewal Services refuses), they are signed out and
 * forgotten here too; a sign-in over another account ends the other account's. Servers added by
 * a local sign-in stay. */
async function endServers(keep?: string) {
  const record = await browserAppSessions().read(), ended = new Map<string, PorticoAccountScope>();
  for (const entry of record.servers) {
    const hosted = entry.connection.hosted;
    if (entry.signIn === 'portico' && hosted && hosted.origin === location.origin && hosted.accountId !== keep)
      ended.set(hosted.accountId, {origin: hosted.origin, accountId: hosted.accountId});
  }
  for (const scope of ended.values()) await browserAppSessionActions().endPorticoAccount(scope);
}
let settled: Promise<void> = Promise.resolve();
let signIns = porticoAccountHolder.signIns();
function check() {
  settled = settled.then(async () => {
    if (!porticoAccountHolder.signedIn()) {await endServers(); return;}
    if (porticoAccountHolder.signIns() === signIns) return;
    signIns = porticoAccountHolder.signIns();
    const who = await porticoAccountHolder.who().catch(() => undefined);
    if (who) await endServers(who.id);
  }).catch(() => {/* Read again at the next change or launch. */});
  return settled;
}
porticoAccountHolder.subscribe(() => void check());

/** Reopens a saved server only after the servers of an ended sign-in are gone, so one signed out on
 * the account pages while no app tab was open never opens again. */
export async function restoreAfterAccountEnd(...args: Parameters<typeof restoreBrowserConnection>) {
  await check();
  return restoreBrowserConnection(...args);
}
