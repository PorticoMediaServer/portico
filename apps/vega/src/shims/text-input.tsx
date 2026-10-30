import * as Vega from '@amazon-devices/react-native-kepler';
import React, {forwardRef, useCallback, useContext, useEffect, useImperativeHandle, useRef, useState} from 'react';
import {Unreachable, focusHolder, seekFocus, type Member} from './focus';
import {takePreferred, typingEnded, typingStarted} from './typing';

type SdkProps = React.ComponentProps<typeof Vega.TextInput> & {auxOptions?: string};
type SdkInput = React.ComponentRef<typeof Vega.TextInput>;

/**
 * The SDK's `TextInput` (0.24) adds a minimum length to its own `auxOptions` prop when
 * `enablesReturnKeyAutomatically` is set, and reads the length of `auxOptions` without checking
 * that there is one: a field with the standard prop and without the Amazon one fails to render.
 */
const Input = forwardRef<SdkInput, SdkProps>(function Input(props, ref) {
  return props.enablesReturnKeyAutomatically && props.auxOptions === undefined
    ? <Vega.TextInput ref={ref} {...({auxOptions: ''} as object)} {...props} />
    : <Vega.TextInput ref={ref} {...props} />;
});

let editingNow = 0;
let lastOrigin: Member | null = null;

function summon(input: SdkInput | null): void {
  const node = input as (SdkInput & {requestTVFocus?: () => void}) | null;
  if (node?.requestTVFocus) node.requestTVFocus();
  else node?.focus();
}

/**
 * A text input the remote must not reach (the shared `Field` on television: the field is a
 * button, and its real input sits in an unreachable guide, to be focused only by the button).
 *
 * On Vega an input cannot be mounted and out of reach: focus moving across the screen lands on
 * it, invisible as it is. So it is mounted only between `focus()` and the end of typing. Taking
 * focus brings the system keyboard up; when the keyboard is done (submitted, or dismissed with
 * Back, which is kept from the screen), focus goes to the control that asked for it meanwhile, or
 * back to the control that opened the keyboard, and the input goes away.
 */
const Summoned = forwardRef<SdkInput, SdkProps>(function Summoned({onBlur, onSubmitEditing, onEndEditing, ...props}, ref) {
  const [editing, setEditing] = useState(false);
  const input = useRef<SdkInput | null>(null);
  const origin = useRef<Member | null>(null);
  const turn = useRef(0);
  const live = useRef(false);
  const seeking = useRef<(() => void) | undefined>(undefined);

  const end = useCallback(() => {
    if (!live.current) return;
    live.current = false;
    typingEnded();
    // Another field took over (a "next" chain): it has the keyboard now.
    if (editingNow !== turn.current) { setEditing(false); return; }
    // Focus goes to the control that asked for it while the keyboard was up (the first result of
    // a search), or back where it came from.
    const to = takePreferred<Member>() ?? origin.current;
    if (!to?.live) { setEditing(false); return; }
    // Focus moves first, and the input goes away after: an input removed while it holds focus
    // leaves focus to wherever the system then puts it.
    seeking.current = seekFocus(() => to, () => focusHolder() === to, [0, 100, 250, 500, 800], () => {
      seeking.current = undefined;
      setEditing(false);
    });
  }, []);

  useImperativeHandle(ref, () => ({
    focus: () => {
      if (live.current) { summon(input.current); return; }
      seeking.current?.();
      seeking.current = undefined;
      // Asked for by the field's button, which has focus; or by the field before it (a "next"
      // chain), whose button is then the nearest thing to where focus was.
      origin.current = lastOrigin = focusHolder() ?? lastOrigin;
      turn.current = ++editingNow;
      live.current = true;
      typingStarted();
      setEditing(true);
      // Still mounted from the last time: nothing will render, so ask here.
      if (input.current) summon(input.current);
    },
    blur: () => end(),
    clear: () => input.current?.clear(),
    isFocused: () => live.current,
  }) as unknown as SdkInput, [end]);

  useEffect(() => {
    if (!editing) return;
    // The SDK asks for `requestTVFocus` in place of `focus` (which it warns about on every call).
    const frame = requestAnimationFrame(() => summon(input.current));
    const back = Vega.BackHandler.addEventListener('hardwareBackPress', () => { end(); return true; });
    const hidden = Vega.Keyboard.addListener('keyboardDidHide', end);
    return () => { cancelAnimationFrame(frame); back.remove(); hidden.remove(); };
  }, [editing, end]);
  useEffect(() => () => { seeking.current?.(); if (live.current) { live.current = false; typingEnded(); } }, []);

  if (!editing) return null;
  return (
    <Input
      ref={input}
      {...props}
      onBlur={event => { onBlur?.(event); end(); }}
      onSubmitEditing={event => { onSubmitEditing?.(event); end(); }}
      onEndEditing={event => { onEndEditing?.(event); end(); }}
    />
  );
});

const TextInputShim = forwardRef<SdkInput, SdkProps>(function TextInput(props, ref) {
  return useContext(Unreachable) ? <Summoned ref={ref} {...props} /> : <Input ref={ref} {...props} />;
});
export const TextInput = TextInputShim as unknown as typeof Vega.TextInput;
export type TextInput = Vega.TextInput;
