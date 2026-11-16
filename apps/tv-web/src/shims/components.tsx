import React, {forwardRef, useCallback, useLayoutEffect, useRef} from 'react';
import * as Web from 'react-native-web';
import {focus, setNextFocus, setPreferred} from './focus';
import {setPressHandlers} from './press-registry';

/**
 * React Native's `View` and `Pressable` with the TV properties react-native-web drops:
 * `nextFocusUp/Down/Left/Right` and `hasTVPreferredFocus` (both read by `focus.tsx`), and on a
 * `Pressable` the press handlers OK runs (`keys.ts`), so a held OK is the control's long press as
 * on every other TV. `focusable={false}` keeps a control out of reach, as it does natively.
 */
type TVProps = {
  nextFocusUp?: unknown; nextFocusDown?: unknown; nextFocusLeft?: unknown; nextFocusRight?: unknown; nextFocusForward?: unknown;
  hasTVPreferredFocus?: boolean; tvParallaxProperties?: unknown; isTVSelectable?: boolean;
};

function useTVHost<T>(ref: React.ForwardedRef<T>, {nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, hasTVPreferredFocus}: TVProps) {
  const host = useRef<Element | null>(null);
  const attach = useCallback((element: T | null) => {
    host.current = element instanceof Element ? element : null;
    if (typeof ref === 'function') ref(element);
    else if (ref) (ref as React.MutableRefObject<T | null>).current = element;
  }, [ref]);
  useLayoutEffect(() => {
    const element = host.current;
    if (!element) return;
    setNextFocus(element, {up: nextFocusUp, down: nextFocusDown, left: nextFocusLeft, right: nextFocusRight});
  });
  // Preferred focus is read once, when the control appears, as on Apple TV (and Fire TV).
  const wanted = useRef(hasTVPreferredFocus === true);
  useLayoutEffect(() => {
    const element = host.current;
    if (!element || !wanted.current) return;
    setPreferred(element, true);
    // As on Apple TV: a control that appears asking for focus takes it.
    if (element instanceof HTMLElement) requestAnimationFrame(() => {if (element.isConnected) focus(element);});
    return () => setPreferred(element, false);
  }, []);
  return attach;
}

export const View = forwardRef<unknown, Web.ViewProps & TVProps>(function View({nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, nextFocusForward: _forward, hasTVPreferredFocus, tvParallaxProperties: _parallax, isTVSelectable: _selectable, ...props}, ref) {
  const attach = useTVHost(ref, {nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, hasTVPreferredFocus});
  return <Web.View {...props} ref={attach as never}/>;
});

type PressableProps = Web.PressableProps & TVProps & {onLongPress?: Web.PressableProps['onLongPress']};
const synthetic = {nativeEvent: {}} as never;

export const Pressable = forwardRef<unknown, PressableProps>(function Pressable({nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, nextFocusForward: _forward, hasTVPreferredFocus, tvParallaxProperties: _parallax, isTVSelectable: _selectable, ...props}, ref) {
  const host = useRef<Element | null>(null);
  const tv = useTVHost(ref, {nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, hasTVPreferredFocus});
  const attach = useCallback((element: unknown) => {host.current = element instanceof Element ? element : null; tv(element as never);}, [tv]);
  const latest = useRef(props); latest.current = props;
  useLayoutEffect(() => {
    const element = host.current;
    if (!element) return;
    setPressHandlers(element, {
      press: () => {const p = latest.current; if (!p.disabled) p.onPress?.(synthetic);},
      pressIn: () => {const p = latest.current; if (!p.disabled) p.onPressIn?.(synthetic);},
      pressOut: () => {const p = latest.current; if (!p.disabled) p.onPressOut?.(synthetic);},
      longPress: () => {const p = latest.current; if (p.disabled || !p.onLongPress) return false; p.onLongPress(synthetic); return true;},
    });
    return () => setPressHandlers(element, undefined);
  }, []);
  return <Web.Pressable {...props} ref={attach as never}/>;
});

/**
 * A text field, as a TV has it: the remote moves over it like any control, and OK starts typing
 * (the set's on-screen keyboard opens for an editable field that has focus). Until then the
 * field is read-only, so passing over it never opens the keyboard. OK again (the keyboard's Done)
 * submits and stops typing; Up or Down stop typing and move on.
 */
export const TextInput = forwardRef<unknown, Web.TextInputProps & TVProps>(function TextInput({nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, nextFocusForward: _forward, hasTVPreferredFocus, tvParallaxProperties: _parallax, isTVSelectable: _selectable, editable, onBlur, ...props}, ref) {
  const host = useRef<HTMLInputElement | null>(null);
  const tv = useTVHost(ref, {nextFocusUp, nextFocusDown, nextFocusLeft, nextFocusRight, hasTVPreferredFocus});
  const [typing, setTyping] = React.useState(false);
  const refocusing = useRef(false);
  const attach = useCallback((element: unknown) => {host.current = element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement ? element as HTMLInputElement : null; tv(element as never);}, [tv]);
  useLayoutEffect(() => {
    const element = host.current;
    if (!element || editable === false) return;
    const keydown = (event: KeyboardEvent) => {
      if (event.keyCode === 13 && !typing) {
        event.preventDefault();
        event.stopImmediatePropagation();
        setTyping(true);
        // Focusing it again once editable is what opens the set's keyboard.
        requestAnimationFrame(() => {
          refocusing.current = true;
          element.blur();
          element.focus({preventScroll: true});
          refocusing.current = false;
        });
      } else if (typing && (event.keyCode === 38 || event.keyCode === 40)) {
        setTyping(false);
      }
    };
    element.addEventListener('keydown', keydown);
    return () => element.removeEventListener('keydown', keydown);
  }, [editable, typing]);
  return <Web.TextInput {...props} editable={editable !== false && typing} ref={attach as never}
    onBlur={event => {if (refocusing.current) return; setTyping(false); onBlur?.(event);}}
    onSubmitEditing={event => {setTyping(false); props.onSubmitEditing?.(event);}}/>;
});
(TextInput as unknown as {State: unknown}).State = (Web.TextInput as unknown as {State: unknown}).State;
