import {Linking, Platform} from 'react-native';
import {parseJoinLink} from '../../../../packages/client-core/src/join-link';

/** Input only: a pending link grants no server, identity or invitation authority. */
export type NativeInvitationLink = Readonly<{id: number; raw: string; current: () => boolean;
  onRetired: (listener: () => void) => () => void; dismiss: () => void}>;
let pending: NativeInvitationLink | undefined, retirePending: (() => void) | undefined;
let serial = 0, generation = 0, eventRevision = 0, initialRevision = 0, initialConsumed = false;
let initial: Promise<string | null> | undefined, offNative: (() => void) | undefined;
const owners = new Set<object>(), listeners = new Set<() => void>();
const publish = () => {for (const listener of listeners) {try {listener();} catch { /* Input publication grants no authority. */ }}};
function receive(raw: unknown, warm = false) {
  // Use the exact existing codec and raw input. No normalization, URL opening,
  // network, persistence, logging or short-code/capability conversion.
  if (typeof raw !== 'string' || !parseJoinLink(raw)) return;
  if (warm) ++eventRevision;
  if (pending?.current() && pending.raw === raw) return;
  const oldRetire = retirePending, retired = new Set<() => void>();
  let active = true;
  const value: NativeInvitationLink = Object.freeze({id: ++serial, raw,
    current: () => active && pending === value,
    onRetired: listener => {if (!active) return () => {}; retired.add(listener); return () => {retired.delete(listener);};},
    dismiss: () => {if (pending !== value || !active) return; pending = undefined; retirePending = undefined; retire(); publish();},
  });
  const retire = () => {if (!active) return; active = false; for (const listener of retired) {try {listener();} catch { /* Keep retiring original listeners. */ }} retired.clear();};
  pending = value; retirePending = retire;
  // Retire the original consumer synchronously before publishing its successor.
  oldRetire?.(); publish();
}
export const nativeInvitationLinks = Object.freeze({
  getSnapshot: () => pending,
  subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  acquire: () => {
    if (Platform.isTV) return () => {};
    const owner = {}; owners.add(owner);
    if (!offNative) {
      const ownGeneration = ++generation;
      try {const subscription = Linking.addEventListener('url', event => {if (generation !== ownGeneration || !owners.size) return; receive(event.url, true);});
        offNative = () => subscription.remove();
      } catch (error) {owners.delete(owner); throw error;}
    }
    const ownGeneration = generation;
    if (!initial) {initialRevision = eventRevision;
      try {initial = Linking.getInitialURL();} catch {initial = Promise.resolve(null);}
    }
    void initial.then(raw => {
      if (!owners.has(owner) || generation !== ownGeneration || initialConsumed) return;
      initialConsumed = true;
      // A real warm URL takes priority over the cold-launch read it overtook.
      if (eventRevision === initialRevision) receive(raw);
    }, () => {});
    return () => {owners.delete(owner); if (!owners.size) {++generation; offNative?.(); offNative = undefined;}};
  },
});
