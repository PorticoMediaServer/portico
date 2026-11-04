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
 *   present that never reached the window, D15), `onGone` runs after `wait`.
 * - Android and Vega: the dialog window goes with the element, so `onGone` runs at once.
 */
export function useNativeDismissal(onGone: () => void, wait = LAYER_FOCUS_WAIT_MS): Readonly<{visible: boolean; close: () => void; onDismiss: () => void}> {
  const [closing, setClosing] = useState(false);
  const gone = useRef(onGone), state = useRef({closing: false, finished: false});
  gone.current = onGone;
  const finish = useCallback(() => {if (state.current.finished) return; state.current.finished = true; gone.current();}, []);
  const close = useCallback(() => {
    if (state.current.closing || state.current.finished) return;
    if (Platform.OS !== 'ios') {finish(); return;}
    state.current.closing = true;
    setClosing(true);
  }, [finish]);
  const onDismiss = useCallback(() => {if (state.current.closing) finish();}, [finish]);
  useEffect(() => {
    if (!closing) return;
    const timer = setTimeout(finish, wait);
    return () => clearTimeout(timer);
  }, [closing, finish, wait]);
  return {visible: !closing, close, onDismiss};
}
