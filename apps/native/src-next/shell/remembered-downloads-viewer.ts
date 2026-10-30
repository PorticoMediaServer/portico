import {selectedViewerRecord} from '../../../../packages/client-core/src/viewer-selection';
import type {LocalPlaybackScope} from '../../../../packages/player-core/src/local-playback-identity';
import {nativeAppSessions, subscribeNativeAppSessions} from '../platform/app-session-storage';
import {nativeAccounts} from '../platform/accounts';
import {nativeShellDirectViewer} from './bootstrap-services';

type InstallationOwner = ReturnType<typeof import('../platform/installation').beginNativeInstallation>;
const rememberedProof = Symbol('remembered_downloads_identity');
const cancelled = () => Object.assign(new Error('remembered_downloads_identity_closed'), {name: 'AbortError', code: 'cancelled'});

/** Identity only, not a Home/API/playback context or file permission. A future
 * offline-only consumer must still verify a real receipt and its own purpose.
 * Only a tokenless state-binding owner leaves this module; no session or role does.
 * Borrow the root's installation owner; never create/dispose a competing one. */
export async function captureRememberedDownloadsViewer(installation: InstallationOwner,
  options: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
  let closed = false, established = false, changes = 0;
  const listeners = new Set<() => void>();
  let offStore = () => {}, offFence = () => {};
  const active = () => {try {return !closed && !options.signal.aborted && options.isCurrent() && installation.current();} catch {return false;}};
  const dispose = () => {
    if (closed) return; closed = true; options.signal.removeEventListener('abort', dispose); offStore(); offFence();
    for (const listener of listeners) {try {listener();} catch { /* Identity is already retired; observers cannot reopen it. */ }}
    listeners.clear();
  };
  const check = () => {if (!active()) throw cancelled();};
  try {
    check(); options.signal.addEventListener('abort', dispose, {once: true});
    offStore = subscribeNativeAppSessions(() => {++changes; if (established) dispose();});
    offFence = nativeAccounts.onFence(dispose);
    const prepared = await installation.prepare(); check();
    const original = prepared.saved.viewer;
    if (prepared.saved.recovered || !original) {dispose(); return undefined;}
    const stamp = changes, selected = await nativeAppSessions.selected(); check();
    if (changes !== stamp || !selected) throw cancelled();
    if (!prepared.connection || JSON.stringify(selected.connection) !== JSON.stringify(prepared.connection)) throw cancelled();
    const projected = selectedViewerRecord(selected.connection.logicalOrigin, selected.connection.session, true);
    if (JSON.stringify(projected) !== JSON.stringify(original)) throw cancelled();
    const viewer = original.session.viewer;
    nativeShellDirectViewer(viewer); if (viewer.authority !== 'local') throw cancelled();
    const owner: LocalPlaybackScope = Object.freeze({authority: viewer.authority, serverId: viewer.serverId,
      accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: original.session.sessionFamilyId});
    established = true; check();
    return Object.freeze({[rememberedProof]: rememberedProof, owner,
      current: () => established && active(),
      subscribe: (listener: () => void) => {if (closed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
      dispose,
    });
  } catch (error) {dispose(); throw error;}
}
export type RememberedDownloadsViewer = NonNullable<Awaited<ReturnType<typeof captureRememberedDownloadsViewer>>>;
