import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

export type OverlayKey = 'escape' | 'tab' | 'menu';
export type OverlayKeyHandler = (key: OverlayKey, shift: boolean) => boolean;
type Registration = {handler: OverlayKeyHandler};
type NativeKey = {id: string; key: 'escape' | 'tab'; shift: boolean};
type KeyBridge = {
  setEnabled: (enabled: boolean) => void;
  claimKeyEvent: (id: string) => boolean;
  resolveKeyEvent: (id: string, handled: boolean) => void;
  addListener: (event: string) => void;
  removeListeners: (count: number) => void;
};

const registrations: Registration[] = [];
let disconnect: (() => void) | undefined;
let dispatching = 0;

export const hasRegisteredOverlayKeys = () => registrations.length > 0;

/** Only the current registration answers; false never falls through to a lower overlay. */
export function dispatchRegisteredOverlayKey(key: OverlayKey, shift = false): boolean {
  return registrations[registrations.length - 1]?.handler(key, shift) === true;
}

function stopWhenUnused() {
  if (registrations.length || dispatching) return;
  const stop = disconnect;
  disconnect = undefined;
  stop?.();
}

function connect() {
  if (disconnect || Platform.OS !== 'android') return;
  const bridge = NativeModules.PorticoOverlayKeys as KeyBridge | undefined;
  if (!bridge?.setEnabled || !bridge.claimKeyEvent || !bridge.resolveKeyEvent) return;
  const subscription = new NativeEventEmitter(bridge).addListener('PorticoOverlayKey', (event: NativeKey) => {
    // Native may already have replayed an event after timeout or subscription teardown.
    if (!bridge.claimKeyEvent(event.id)) return;
    let handled = false;
    dispatching++;
    try {
      if (event.key === 'escape' || event.key === 'tab') handled = dispatchRegisteredOverlayKey(event.key, event.shift === true);
    } finally {
      try {bridge.resolveKeyEvent(event.id, handled);}
      finally {dispatching--; stopWhenUnused();}
    }
  });
  disconnect = () => {
    // Native replays unhandled strokes before discarding its capture state.
    bridge.setEnabled(false);
    subscription.remove();
  };
  bridge.setEnabled(true);
}

/**
 * The renderer supplies its real top-overlay dispatcher. Retain one subscription while its
 * stack is present, and release it after the final surface exits. No renderer is imported here.
 */
export function registerOverlayKeys(handler: OverlayKeyHandler): () => void {
  const registration = {handler};
  registrations.push(registration);
  connect();
  return () => {
    const index = registrations.indexOf(registration);
    if (index < 0) return;
    registrations.splice(index, 1);
    stopWhenUnused();
  };
}
