import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
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
 * - `present`: the overlay's own presence (true through its exit). Render the `Modal` while `mounted`, with `key`,
 *   `visible` and `onDismiss` from here, and keep the overlay lifecycle open while `mounted`.
 * - `shown`: when the `Modal` asks to be visible while present (a menu that hides at once when it closes); it defaults
 *   to `present`.
 *
 * Each presentation has its own identity (a token, taken when it is shown). Its `Modal` is its own native host (`key`),
 * and its `onDismiss` carries that token, so a report retires only its own presentation's close: a report from an
 * earlier presentation, however late, never ends a later close. The `Modal` is shown again only once the presentation
 * before it has finished leaving: UIKit can't present a view controller that is still being dismissed, and flipping
 * `visible` back does not cancel the dismissal. A report while its presentation is shown is UIKit's own Menu dismissal
 * of it (D16): its close then has nothing left to wait for. A presentation whose dismissal is never reported is
 * retired by the fallback; it then blocks nothing, and its report, if it ever comes, is ignored. On iOS a new
 * presentation is asked one commit after it is wanted, so a leaving one registered in the same commit is seen first.
 */
type Presentation = Readonly<{
  /** The current presentation (0 before the first). */
  token: number;
  /** Whether it is shown. */
  shown: boolean;
  /** Hidden, and its dismissal not yet reported or given up on (iOS). */
  leaving: boolean;
  /** UIKit dismissed it by itself while shown (D16). */
  gone: boolean;
}>;
/** Every presentation still leaving on iOS, across overlays: from the start of its exit (`closing`: it runs its own
 * exit while its `Modal` is still shown) until UIKit reports it gone. UIKit drops a `Modal` presented over one that is
 * then dismissed, or while another is still being dismissed, so a new one waits for all of them: a menu's item that opens a confirmation sheet would otherwise
 * open nothing (iPhone, Switch server › ⋯ › Sign out). Each leaving presentation is retired by its own report or fallback. */
const leavingModals = new Set<object>(), leavingListeners = new Set<() => void>();
const leavingChanged = () => {for (const listener of [...leavingListeners]) listener();};
const subscribeLeaving = (listener: () => void) => {leavingListeners.add(listener); return () => {leavingListeners.delete(listener);};};
const leavingCount = () => leavingModals.size;
/** How long a Modal unmounted while shown is treated as still leaving: React Native reports no dismissal after unmount,
 * and a Menu's non-animated dismissal is done well within this. */
export const UNMOUNTED_DISMISS_MS = 500;

export function useNativeModalPresence(present: boolean, shown = present, wait = LAYER_FOCUS_WAIT_MS, closing = false): Readonly<{mounted: boolean; visible: boolean; onDismiss: () => void; key: number}> {
  const ios = Platform.OS === 'ios';
  const [state, setState] = useState<Presentation>({token: 0, shown: false, leaving: false, gone: false});
  let now = state;
  // The Modal's own edge, decided in render so that `mounted` holds on the same commit that hides it.
  // A new presentation also waits while another overlay's Modal is still leaving (iOS); its own leaving is not counted.
  const own = useRef<object | null>(null);
  const others = useSyncExternalStore(subscribeLeaving, leavingCount, leavingCount) - (own.current ? 1 : 0);
  // A new presentation on iOS asks only after one commit has passed with it wanted: a menu item that closes its menu
  // and opens a picker does both in one commit, and the menu's leaving is registered in that commit's effects, after
  // this render. Asking at once presented the picker under the menu's dismissal: UIKit dropped it and its empty Modal
  // took every tap (iPhone, Search › ⋯ › Add to playlist…, a film's Add to collection…).
  const wanted = shown && present && !state.leaving, [armed, setArmed] = useState(false);
  useEffect(() => {if (wanted !== armed) setArmed(wanted);}, [wanted, armed]);
  const ask = wanted && (state.shown || !ios || armed && others === 0);
  if (ask !== state.shown) {
    now = ask ? {token: state.token + 1, shown: true, leaving: false, gone: false} // a new presentation
      : {...state, shown: false, leaving: ios && !state.gone, gone: false}; // its dismissal starts
    setState(now);
  }
  const report = useCallback((token: number) => setState(current => current.token !== token ? current // an earlier presentation's
    : current.leaving ? {...current, leaving: false}
    : current.shown && !current.gone ? {...current, gone: true} : current), []);
  const token = now.token, onDismiss = useMemo(() => () => report(token), [report, token]);
  const holding = ios && (state.leaving || closing && state.shown);
  // A Modal whose owner unmounts it while it is shown (a menu drawn only while its entry exists, closed by its own item)
  // never reports a dismissal, yet UIKit is still taking it down: it counts as leaving for a short while, so a picker the
  // item opens waits (iPhone, Search › ⋯ › Add to playlist…: #5012's wait alone was not enough).
  const showing = useRef(false);
  showing.current = ios && now.shown && present;
  useEffect(() => () => {
    if (!showing.current) return;
    const entry = {};
    leavingModals.add(entry); leavingChanged();
    setTimeout(() => {if (leavingModals.delete(entry)) leavingChanged();}, UNMOUNTED_DISMISS_MS);
  }, []);
  useEffect(() => {
    if (!holding) return;
    const entry = {}; own.current = entry; leavingModals.add(entry); leavingChanged();
    return () => {if (own.current === entry) own.current = null; if (leavingModals.delete(entry)) leavingChanged();};
  }, [holding, state.token]);
  useEffect(() => {
    if (!state.leaving) return;
    const leaving = state.token;
    const timer = setTimeout(() => setState(current => current.token === leaving && current.leaving ? {...current, leaving: false} : current), wait);
    return () => clearTimeout(timer);
  }, [state.leaving, state.token, wait]);
  return {mounted: present || now.leaving, visible: now.shown, onDismiss, key: token};
}
