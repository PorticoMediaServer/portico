import * as Vega from '@amazon-devices/react-native-kepler';
import React, {forwardRef, useCallback, useContext, useEffect, useRef} from 'react';
import {Unreachable, useGuides, type Member} from './focus';
import {useSnap} from './snap';
import {isTyping, preferAfterTyping} from './typing';

type SdkProps = React.ComponentProps<typeof Vega.Pressable>;
// Shared Apple TV callers can supply this prop; Vega has no parallax API.
// Accept it at the shim boundary and consume it before forwarding SDK props.
type Props = SdkProps & {tvParallaxProperties?: unknown};
type FocusNode = {requestTVFocus?: () => void};

/**
 * `Pressable` with the Apple TV's meaning of `hasTVPreferredFocus`: a preference the focus
 * engine reads when the control appears, or when the preference is first expressed.
 *
 * On Vega the native prop is a standing order instead. While it is set on any mounted view, every
 * update of the screen asks for focus again, and taking the prop away does not withdraw it. The
 * shared `Focusable` re-renders whenever its focus changes, so a preferred control took focus
 * back the moment it lost it; and once anything on the screen animated continuously (a loading
 * ring), the app stopped responding. The native view is therefore never given the prop: the
 * preference is one request for focus when the control appears, and one more each time the
 * preference turns on.
 *
 * Taking focus also brings the row and page around the control to rest (see `snap.tsx`), and is
 * told to the focus guides around it, which decide where focus returns to (see `focus.tsx`).
 */
export const Pressable = forwardRef<React.ComponentRef<typeof Vega.Pressable>, Props>(function Pressable(
  {hasTVPreferredFocus, tvParallaxProperties: _parallax, onFocus, onBlur, ...props},
  forwarded,
) {
  const node = useRef<React.ComponentRef<typeof Vega.Pressable> | null>(null);
  const attach = useCallback((value: React.ComponentRef<typeof Vega.Pressable> | null) => {
    node.current = value;
    if (typeof forwarded === 'function') forwarded(value);
    else if (forwarded) forwarded.current = value;
  }, [forwarded]);
  const snap = useSnap();
  // Inside a guide that cannot be reached, neither can this (see `focus.tsx`).
  const unreachable = useContext(Unreachable);
  const available = !unreachable && props.focusable !== false && props.disabled !== true;
  const guides = useGuides();
  const member = useRef<Member>({node: () => node.current, open: false, live: false, prefers: false}).current;
  const wants = hasTVPreferredFocus === true;
  member.prefers = wants;
  member.open = available;
  member.label = props.accessibilityLabel;
  useEffect(() => {
    if (!wants) return;
    // Under the keyboard the request waits (see `typing.ts`).
    if (isTyping()) { preferAfterTyping(member); return; }
    // The next frame lets a control that has just appeared finish layout first.
    const frame = requestAnimationFrame(() => (node.current as FocusNode | null)?.requestTVFocus?.());
    return () => cancelAnimationFrame(frame);
  }, [wants, member]);
  useEffect(() => (available ? guides.join(member) : undefined), [available, guides, member]);
  const focused = useCallback<NonNullable<SdkProps['onFocus']>>(event => {
    guides.focused(member);
    snap?.();
    onFocus?.(event);
  }, [guides, member, snap, onFocus]);
  const blurred = useCallback<NonNullable<SdkProps['onBlur']>>(event => {
    guides.blurred(member);
    onBlur?.(event);
  }, [guides, member, onBlur]);
  return <Vega.Pressable ref={attach} onFocus={focused} onBlur={blurred} {...props} focusable={unreachable ? false : props.focusable} />;
});
