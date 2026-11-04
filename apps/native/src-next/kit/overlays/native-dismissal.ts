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
 *   to `present`.
 *
 * Each report belongs to its own presentation. Every time the `Modal` is hidden (whether `shown` or `present` ended it)
 * one dismissal starts, and reports are matched to those dismissals oldest first, so an old report never ends a later
 * close. The `Modal` is shown again only once no dismissal is outstanding: UIKit can't present a view controller that
 * is still being dismissed, and flipping `visible` back does not cancel the dismissal. A report while the `Modal` is
 * shown, with none outstanding, is UIKit's own Menu dismissal of that presentation (D16): its close then has nothing
 * left to wait for. Dismissals the fallback gave up on are remembered, so their late reports are ignored too.
 */
type Presentation = Readonly<{
  /** What the `Modal` was last asked for. */
  shown: boolean;
  /** Dismissals started (iOS) and not yet reported, oldest first. */
  outstanding: number;
  /** Dismissals the fallback gave up on; their reports, if they ever come, are ignored. */
  lost: number;
  /** UIKit dismissed the presentation shown now by itself (D16). */
  gone: boolean;
}>;
export function useNativeModalPresence(present: boolean, shown = present, wait = LAYER_FOCUS_WAIT_MS): Readonly<{mounted: boolean; visible: boolean; onDismiss: () => void}> {
  const ios = Platform.OS === 'ios';
  const [state, setState] = useState<Presentation>({shown: false, outstanding: 0, lost: 0, gone: false});
  let now = state;
  // The Modal's own edge, decided in render so that `mounted` holds on the same commit that hides it.
  const ask = shown && present && state.outstanding === 0;
  if (ask !== state.shown) {
    now = ask ? {...state, shown: true, gone: false} // a new presentation
      : {...state, shown: false, gone: false, outstanding: state.outstanding + (ios && !state.gone ? 1 : 0)}; // its dismissal starts
    setState(now);
  }
  const onDismiss = useCallback(() => setState(current =>
    current.outstanding > 0 ? {...current, outstanding: current.outstanding - 1}
      : current.lost > 0 ? {...current, lost: current.lost - 1}
      : current.shown && !current.gone ? {...current, gone: true} : current), []);
  useEffect(() => {
    if (state.outstanding === 0) return;
    const timer = setTimeout(() => setState(current => ({...current, lost: current.lost + current.outstanding, outstanding: 0})), wait);
    return () => clearTimeout(timer);
  }, [state.outstanding, wait]);
  return {mounted: present || now.outstanding > 0, visible: now.shown, onDismiss};
}
