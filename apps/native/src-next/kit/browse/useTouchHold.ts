import {useEffect, useRef, useState} from 'react';
import {AppState, Platform, type GestureResponderEvent} from 'react-native';
import {createTouchHold, type TouchHoldPhase} from './touch-hold';

/** The native timer owns the threshold; Pressability's wider retention area cannot fire it. */
export function useTouchHold(onFire: () => void, enabled: boolean) {
  const callback = useRef(onFire), allowed = useRef(enabled), foreground = useRef(AppState.currentState === 'active');
  callback.current = onFire; allowed.current = enabled;
  const [phase, setPhase] = useState<TouchHoldPhase>('idle');
  const [hold] = useState(() => createTouchHold(() => {if (allowed.current && foreground.current) callback.current();}, setPhase, {
    schedule: (run, delay) => setTimeout(run, delay), cancel: ticket => clearTimeout(ticket as ReturnType<typeof setTimeout>),
  }));
  useEffect(() => () => hold.dispose(), [hold]);
  useEffect(() => {
    const state = AppState.addEventListener('change', next => {
      foreground.current = next === 'active';
      if (!foreground.current) hold.cancel();
    });
    // 'blur' is an Android-only AppState event: iOS/tvOS route it to appStateFocusChange,
    // which RCTAppState does not implement, so registering it there is fatal in Debug.
    const focus = Platform.OS === 'android' ? AppState.addEventListener('blur', () => hold.cancel()) : undefined;
    return () => {state.remove(); focus?.remove();};
  }, [hold]);
  useEffect(() => {if (!enabled) hold.cancel();}, [enabled, hold]);
  return {phase, consumePress: hold.consumePress, cancel: hold.cancel, end: hold.end, handlers: {
    onTouchStart(event: GestureResponderEvent) {
      if (!enabled || !foreground.current) return;
      if (event.nativeEvent.touches.length !== 1) {hold.cancel(); return;}
      hold.start({x: event.nativeEvent.pageX, y: event.nativeEvent.pageY});
    },
    onTouchMove: (event: GestureResponderEvent) => hold.move({x: event.nativeEvent.pageX, y: event.nativeEvent.pageY}),
    onTouchEnd: () => hold.end(), onTouchCancel: () => hold.cancel(),
  }};
}
