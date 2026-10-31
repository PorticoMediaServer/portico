import {releaseQuietly} from '../../../../packages/client-core/src/cleanup';
import {SessionLifecycle} from '../../../../packages/client-core/src/session-lifecycle-runtime';
import type {LifecycleState} from '../../../../packages/client-core/src/session-lifecycle';
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
