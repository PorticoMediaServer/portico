export type SecureRecordsRecoveryState = Readonly<{phase: 'idle' | 'confirming' | 'working' | 'reopen'; failed: boolean}>;
export const secureRecordsRecoveryIdle: SecureRecordsRecoveryState = Object.freeze({phase: 'idle', failed: false});

/** A closed reset/restart operation belonging to one original root. After a
 * confirmed reset the installation key may change, so only the same root's
 * local ownership may finish restarting; it grants no session or transport. */
export function createSecureRecordsRecovery(ports: Readonly<{
  current: () => boolean; ownsRoot: () => boolean; retry: () => void;
  reset: () => Promise<boolean>; restart: () => Promise<boolean>;
}>) {
  let alive = true, state = secureRecordsRecoveryIdle, pending: object | undefined;
  const listeners = new Set<() => void>();
  const owned = () => alive && ports.ownsRoot();
  const admitted = () => owned() && ports.current() && owned();
  const publish = (next: SecureRecordsRecoveryState) => {if (owned()) {state = Object.freeze(next); for (const listener of [...listeners]) listener();}};
  return Object.freeze({
    getSnapshot: () => state,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    open: () => {if (pending || state.phase !== 'idle' || !admitted()) return; publish({phase: 'confirming', failed: false});},
    cancel: () => {if (!pending && state.phase === 'confirming' && owned()) publish(secureRecordsRecoveryIdle);},
    retry: () => {if (!pending && state.phase === 'idle' && admitted()) ports.retry();},
    confirm: async () => {
      if (!alive || pending || state.phase !== 'confirming') return;
      const ticket = pending = {};
      const owns = () => pending === ticket && owned();
      try {
        if (!admitted() || !owns()) return;
        publish({phase: 'working', failed: false});
        if (!admitted() || !owns()) return;
        await ports.reset(); // false means the key recovered; restart is still required.
        if (!owns()) return;
        const restarting = await ports.restart();
        if (owns() && !restarting) publish({phase: 'reopen', failed: false});
      } catch {
        if (owns()) publish({phase: 'confirming', failed: true});
      } finally {if (pending === ticket) pending = undefined;}
    },
    dispose: () => {alive = false; listeners.clear();},
  });
}
