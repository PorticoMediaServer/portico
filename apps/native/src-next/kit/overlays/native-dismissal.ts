import {useCallback, useEffect, useRef, useState} from 'react';
import {Platform} from 'react-native';
import {LAYER_FOCUS_WAIT_MS} from '../focus/layer-stack';

/**
 * A native `Modal` that is gone only once the platform says so (DS-427, 4 Nov amendment: focus returns after the
 * overlay has fully left the screen, native dismissal included).
 *
 * Keep the `Modal` mounted with `visible` from here and pass `onDismiss`; call `close` to close it. `onGone` is
 * when the caller unmounts it (and so when the overlay lifecycle gives focus back):
 * - iOS and tvOS: `close` starts the dismissal, and `onGone` runs from `onDismiss`, once UIKit has finished the
 *   transition and given the presenting hierarchy its focus back. If dismissal is never reported (a cold-start
 *   present that never reached the window, D15), `onGone` runs after `wait`. Menu on tvOS is UIKit's own dismissal: it
 *   reports `onDismiss` first and the `onRequestClose` that calls `close` after it, possibly in a later commit (D16). A
 *   dismissal already reported is kept, so that `close` is gone at once rather than on the fallback.
 * - Android and Vega: the dialog window goes with the element, so `onGone` runs at once.
 */
export function useNativeDismissal(onGone: () => void, wait = LAYER_FOCUS_WAIT_MS): Readonly<{visible: boolean; close: () => void; onDismiss: () => void}> {
  const [closing, setClosing] = useState(false);
  const gone = useRef(onGone), state = useRef({closing: false, finished: false, dismissed: false});
  gone.current = onGone;
  const finish = useCallback(() => {if (state.current.finished) return; state.current.finished = true; gone.current();}, []);
  const close = useCallback(() => {
    if (state.current.closing || state.current.finished) return;
    if (Platform.OS !== 'ios' || state.current.dismissed) {finish(); return;}
    state.current.closing = true;
    setClosing(true);
  }, [finish]);
  const onDismiss = useCallback(() => {state.current.dismissed = true; if (state.current.closing) finish();}, [finish]);
  useEffect(() => {
    if (!closing) return;
    const timer = setTimeout(finish, wait);
    return () => clearTimeout(timer);
  }, [closing, finish, wait]);
  return {visible: !closing, close, onDismiss};
}

/**
 * `useNativeDismissal` for a kit overlay that comes and goes: a dialog, sheet, popover menu, the mini guide or a player
 * panel, which runs its own exit and then removes its `Modal`. Same rule (DS-427, 4 Nov amendment): on iOS and tvOS the
 * `Modal` stays mounted, `visible={false}`, until the platform reports it dismissed (or `wait` has passed), so the
 * overlay lifecycle gives focus back only once it has fully gone. On Android and Vega the dialog window goes with the
 * element, so `mounted` follows `present`.
 *
 * - `present`: the overlay's own presence (true through its exit). Render the `Modal` while `mounted`, with `visible`
 *   and `onDismiss` from here, and keep the overlay lifecycle open while `mounted`.
 * - `shown`: when the `Modal` asks to be visible while present (a menu that hides at once when it closes); it defaults
 *   to `present`. A dismissal it starts and that is reported before `present` ends is kept, as is UIKit's own Menu
 *   dismissal (D16), so the overlay goes as soon as its exit ends.
 * - Opened again while the old one is still dismissing, it is shown again once that dismissal ends (UIKit can't present
 *   a view controller that is still being dismissed).
 */
export function useNativeModalPresence(present: boolean, shown = present, wait = LAYER_FOCUS_WAIT_MS): Readonly<{mounted: boolean; visible: boolean; onDismiss: () => void}> {
  const ios = Platform.OS === 'ios';
  const [waiting, setWaiting] = useState(false), [was, setWas] = useState(present);
  // Whether the platform has reported the current showing dismissed (reset when it is shown again).
  const dismissed = useRef(false);
  let pending = waiting;
  if (was !== present) {
    setWas(present);
    // Leaving: wait for the dismissal unless it has already been reported. Back again: a dismissal still running ends first.
    if (!present) {pending = ios && !dismissed.current; if (pending !== waiting) setWaiting(pending);}
  }
  const visible = shown && present && !pending;
  useEffect(() => {if (visible) dismissed.current = false;}, [visible]);
  const onDismiss = useCallback(() => {dismissed.current = true; setWaiting(false);}, []);
  useEffect(() => {
    if (!waiting) return;
    const timer = setTimeout(() => setWaiting(false), wait);
    return () => clearTimeout(timer);
  }, [waiting, wait]);
  return {mounted: present || pending, visible, onDismiss};
}
