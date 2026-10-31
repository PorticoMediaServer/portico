import {releaseQuietly} from '../../../../packages/client-core/src/cleanup';
import {SessionLifecycle} from '../../../../packages/client-core/src/session-lifecycle-runtime';
import type {LifecycleState} from '../../../../packages/client-core/src/session-lifecycle';
import type {UserAction} from '../../../../packages/client-core/src/user-action';
import {createNativeLifecyclePorts, type NativeLifecyclePorts} from './session-lifecycle-native';

/**
 * The app's one session lifecycle (C-DESIGN §3): which server, profile and sign-in the device uses,
 * from launch to sign-out. Created once per process and started by the first root. Screens read
 * its snapshot and start its actions; the shell listens for its fence.
 */
type NativeSessionLifecycle = Readonly<{lifecycle: SessionLifecycle; ports: NativeLifecyclePorts}>;
let instance: NativeSessionLifecycle | undefined;
const fences = new Set<() => void>();

export function nativeSessionLifecycle(): NativeSessionLifecycle {
  if (instance) return instance;
  const ports = createNativeLifecyclePorts({fenceShell: () => {for (const listener of [...fences]) releaseQuietly('lifecycle shell fence', listener);}});
  instance = Object.freeze({lifecycle: new SessionLifecycle(ports), ports});
  return instance;
}

/** The mounted shell retires its private state when the sign-in it was built on changes. */
export function onNativeShellFence(listener: () => void): () => void {
  fences.add(listener);
  return () => {fences.delete(listener);};
}

/** The states in which the device asks who is watching (the launch picker, a switch, or a PIN). */
export const choosingProfile = (state: LifecycleState): state is Extract<LifecycleState, {kind: 'choosingProfile' | 'enteringPin'}> =>
  state.kind === 'choosingProfile' || state.kind === 'enteringPin';

const profilesChanged = () => Object.assign(new Error('native_profiles_changed'), {code: 'stale'});

/** Starts `action` and settles with it: its value when done, its cause when it failed. */
function settle<A extends readonly unknown[]>(action: UserAction<void, A>, ...args: A): Promise<void> {
  return new Promise((resolve, reject) => {
    let started = false;
    const check = () => {
      const now = action.getSnapshot();
      if (!started || now.phase === 'working') return;
      off();
      if (now.phase === 'done') resolve();
      else reject(now.phase === 'failed' ? now.failure.cause : profilesChanged());
    };
    const off = action.subscribe(check);
    action.start(...args); started = true; check();
  });
}

/**
 * Switching profile from inside the app (C-DESIGN §3, rows 23, 16–19): the lifecycle makes the
 * choice, so a PIN-protected profile always asks for its PIN. Resolves once the new sign-in is
 * published (the root then reopens on it). Anything else puts the open profile back and rethrows.
 */
export async function switchNativeProfile(familyId: string, profileId: string, pin?: string): Promise<void> {
  const {lifecycle} = nativeSessionLifecycle();
  const open = lifecycle.getSnapshot().state;
  if (open.kind !== 'ready' || open.viewer.familyId !== familyId) throw profilesChanged();
  lifecycle.switchProfile();
  try {
    await settle(lifecycle.actions.pickProfile, profileId);
    if (lifecycle.getSnapshot().state.kind === 'enteringPin') {
      if (pin === undefined) throw Object.assign(new Error('profile_pin'), {code: 'profile_pin'});
      await settle(lifecycle.actions.submitPin, pin, false);
    }
    if (lifecycle.getSnapshot().state.kind !== 'ready') throw profilesChanged();
  } catch (error) {
    lifecycle.cancelSwitch();
    throw error;
  }
}

/** Resolves once launch has decided (anything but starting or connecting), or rejects when `signal` aborts. */
export function lifecycleSettled(lifecycle: SessionLifecycle, signal: AbortSignal): Promise<LifecycleState> {
  return new Promise((resolve, reject) => {
    const check = () => {
      const state = lifecycle.getSnapshot().state;
      if (state.kind === 'starting' || state.kind === 'connecting') return false;
      off(); signal.removeEventListener('abort', abort); resolve(state); return true;
    };
    const abort = () => {off(); reject(Object.assign(new Error('lifecycle_wait_cancelled'), {name: 'AbortError'}));};
    const off = lifecycle.subscribe(() => {check();});
    if (signal.aborted) {abort(); return;}
    signal.addEventListener('abort', abort, {once: true});
    check();
  });
}
