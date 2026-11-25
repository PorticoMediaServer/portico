import {useCallback, useLayoutEffect, useRef, type ForwardedRef, type ReactNode} from 'react';
import {findNodeHandle, type Image, type Text, type TextProps, type View} from 'react-native';
import type {HostFacts, HostRegistration} from '../dev/layout-dump/index';
import {useHostRef} from './focus/useHostRef';

type Facts = HostFacts & Readonly<{kind: 'text' | 'view' | 'image'}>;
type FocusEvent = Readonly<{nativeEvent: Readonly<{target?: number}>}>;
type TextObservationProps<T extends View | Text | Image> =
  | Readonly<{ref: (host: T | null) => void; onTextLayout: NonNullable<TextProps['onTextLayout']>}>
  | Readonly<{ref?: undefined; onTextLayout?: undefined}>;
type NativeObservation<T extends View | Text | Image> = Readonly<{
  ref: ForwardedRef<T> | undefined;
  textProps: TextObservationProps<T>;
  recordFocus: (event: FocusEvent, focused: boolean) => void;
  recordDeclaredStyle: (flattenedStyle: Facts['flattenedStyle']) => void;
}>;

const isDev = typeof __DEV__ !== 'undefined' && __DEV__;
const EMPTY_TEXT_PROPS: Readonly<{ref?: undefined; onTextLayout?: undefined}> = Object.freeze({});
const NOOP = () => {};

function useDevObservation<T extends View | Text | Image>(
  facts: Facts,
  forwarded?: ForwardedRef<T>,
  textRevision?: ReactNode,
): NativeObservation<T> {
  const lease = useRef<HostRegistration | undefined>(undefined), input = useRef(facts);
  const renderedDeclaration = useRef<Facts['flattenedStyle']>(undefined);
  renderedDeclaration.current = undefined;
  const onHost = useCallback((host: T | null) => {
    lease.current?.unregister(); lease.current = undefined;
    if (isDev && host) {
      const {registerNativeHost} = require('../dev/layout-dump/index') as typeof import('../dev/layout-dump/index');
      lease.current = registerNativeHost(host, input.current);
    }
  }, []);
  const {native, ref} = useHostRef<T>(forwarded ?? null, onHost);
  useLayoutEffect(() => {
    const current = renderedDeclaration.current === undefined ? facts : {...facts, flattenedStyle: renderedDeclaration.current, declaredStyleComplete: true};
    input.current = current; lease.current?.update(current);
  });
  useLayoutEffect(() => {if (facts.kind === 'text') lease.current?.invalidateTextLayout();}, [facts.kind, textRevision]);
  const onTextLayout = useCallback<NonNullable<TextProps['onTextLayout']>>(event => {
    if (input.current.kind === 'text' && native.current && findNodeHandle(native.current) === event.nativeEvent.target)
      lease.current?.recordTextLayout(event.nativeEvent.lines);
  }, [native]);
  const recordFocus = useCallback((event: FocusEvent, focused: boolean) => {
    if (native.current && findNodeHandle(native.current) === event.nativeEvent.target) lease.current?.recordNativeFocus(focused);
  }, [native]);
  // Called only by the real Pressable style resolver, after its actual current
  // state has evaluated the caller's style. No callback replay or React state.
  const recordDeclaredStyle = useCallback((flattenedStyle: Facts['flattenedStyle']) => {
    if (!isDev || flattenedStyle === undefined) return;
    renderedDeclaration.current = flattenedStyle;
    const current = {...input.current, flattenedStyle, declaredStyleComplete: true};
    input.current = current; lease.current?.update(current);
  }, []);
  return {ref, textProps: {ref, onTextLayout}, recordFocus, recordDeclaredStyle};
}

function useProdObservation<T extends View | Text | Image>(
  _facts: Facts,
  forwarded?: ForwardedRef<T>,
  _textRevision?: ReactNode,
): NativeObservation<T> {
  return {ref: forwarded, textProps: EMPTY_TEXT_PROPS, recordFocus: NOOP, recordDeclaredStyle: NOOP};
}

/** Curated, DEV-only observations of existing hosts; no new native view or text renderer. */
export const useNativeObservation: <T extends View | Text | Image>(
  facts: Facts,
  forwarded?: ForwardedRef<T>,
  textRevision?: ReactNode,
) => NativeObservation<T> = isDev ? useDevObservation : useProdObservation;
