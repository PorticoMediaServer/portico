import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeRecordingsController} from './recordings-controller';
import type {NativeLiveController} from './live-controller';
import type {NativeLiveOwnerController} from './live-owner-controller';
import type {NativeScreenLifetime} from './lifetime';

/** Borrow the existing viewer services; a Watch source never constructs a DVR client. */
export type NativeLiveWatchPort = Readonly<{
  recordings: NativeRecordingsController;
  live: NativeLiveController;
  owner: NativeLiveOwnerController;
  requestId: () => Promise<string>;
  current: () => boolean;
}>;
type Slot = {port?: NativeLiveWatchPort; listeners: Set<() => void>};
const slots = new WeakMap<NativeShellBootstrapContext, Slot>();
function slot(context: NativeShellBootstrapContext): Slot {
  let value = slots.get(context);
  if (!value) {value = {listeners: new Set()};slots.set(context, value);}
  return value;
}
export const nativeLiveWatchPort = (context: NativeShellBootstrapContext) => slot(context).port;
export function subscribeNativeLiveWatchPort(context: NativeShellBootstrapContext, listener: () => void) {
  const value = slot(context);value.listeners.add(listener);return () => {value.listeners.delete(listener);};
}
export function publishNativeLiveWatchPort(context: NativeShellBootstrapContext, lifetime: NativeScreenLifetime, services: Omit<NativeLiveWatchPort, 'current'>) {
  const value = slot(context);
  // A second renderer cannot replace this viewer's receipt-owning service.
  if (value.port?.current()) return () => {};
  let alive = true;
  const port: NativeLiveWatchPort = Object.freeze({...services, current: () => alive && value.port === port && lifetime.current()});
  value.port = port;for (const listener of [...value.listeners]) listener();
  return () => {
    alive = false;if (value.port !== port) return;
    value.port = undefined;for (const listener of [...value.listeners]) listener();
  };
}
