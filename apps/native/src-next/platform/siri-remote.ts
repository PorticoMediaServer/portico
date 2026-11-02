import {useEffect, useRef} from 'react';
import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

/** Native/PorticoSiriRemote.m: the Siri Remote touch surface as an absolute finger position
 * (-1…1 across), reported only while JavaScript enables it. */
export type SiriRemoteTouch = Readonly<{phase: 'began' | 'changed' | 'ended'; x: number; y: number}>;

/** A slide across the whole surface (x from -1 to 1) covers a third of the title, at least a
 * minute and at most twenty, so a long film is crossed in a few slides and a short one stays
 * precise. A rest or a tiny wobble moves nothing. */
export function touchScrub(span: number) {
  const whole = Math.max(60, Math.min(20 * 60, span / 3)), perUnit = whole / 2, dead = 0.04;
  let anchor: number | undefined, startX = 0, moved = false;
  return {
    /** The landing time for this touch, or undefined while the finger hasn't really moved. */
    read(touch: SiriRemoteTouch, from: () => number): number | undefined {
      if (touch.phase === 'began') {anchor = from(); startX = touch.x; moved = false; return;}
      if (anchor === undefined) return;
      const delta = touch.x - startX;
      if (!moved && Math.abs(delta) < dead) {if (touch.phase === 'ended') anchor = undefined; return;}
      moved = true;
      const seconds = anchor + delta * perUnit;
      if (touch.phase === 'ended') anchor = undefined;
      return seconds;
    },
  };
}

type Module = {setEnabled?: (enabled: boolean) => void};
const module = (): Module | undefined => Platform.OS === 'ios' && Platform.isTV ? NativeModules.PorticoSiriRemote as Module | undefined : undefined;

/** N3-3: listen to the touch surface only while `enabled` (the player's seek bar has focus);
 * the module restores the remote's normal reporting when disabled, so focus navigation
 * everywhere else is untouched. Apple TV only. */
export function useSiriRemoteTouch(enabled: boolean, handler: (touch: SiriRemoteTouch) => void): void {
  const latest = useRef(handler); latest.current = handler;
  useEffect(() => {
    const native = module();
    if (!enabled || typeof native?.setEnabled !== 'function') return;
    const emitter = new NativeEventEmitter(native as never);
    const subscription = emitter.addListener('PorticoSiriRemoteTouch', (event: unknown) => {
      const touch = event as Partial<SiriRemoteTouch> | undefined;
      if (!touch || (touch.phase !== 'began' && touch.phase !== 'changed' && touch.phase !== 'ended') || typeof touch.x !== 'number' || !Number.isFinite(touch.x)) return;
      latest.current({phase: touch.phase, x: Math.max(-1, Math.min(1, touch.x)), y: typeof touch.y === 'number' ? touch.y : 0});
    });
    native.setEnabled(true);
    return () => {subscription.remove(); native.setEnabled?.(false);};
  }, [enabled]);
}
