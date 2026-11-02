import {releaseQuietly} from '../../../../packages/client-core/src/cleanup';

/** PR-1: a remembered main profile past its management hour. Not a sign-out, a role change or a re-entry: the
 *  profile keeps watching and the app asks for its PIN (ManageUnlockHost). */
export const nativeManageRefused = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === 'profile_pin_manage';

/** PR-1: management refusals heard on the mounted shell's own sign-in. Each one asks for the PIN (ManageUnlockHost,
 *  which also locks management in the session lifecycle), whichever screen made the request. */
let manageRefusals = 0;
const manageListeners = new Set<() => void>();
export const nativeManageGate = Object.freeze({
  getSnapshot: () => manageRefusals,
  subscribe: (listener: () => void) => {manageListeners.add(listener); return () => {manageListeners.delete(listener);};},
  refused: () => {
    manageRefusals++;
    for (const listener of [...manageListeners]) releaseQuietly('manage gate listener', listener);
  },
});
