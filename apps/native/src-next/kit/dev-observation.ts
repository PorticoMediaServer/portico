import {useCallback, useLayoutEffect, useRef, type ForwardedRef, type ReactNode} from 'react';
import {findNodeHandle, type Image, type Text, type TextProps, type View} from 'react-native';
import type {HostFacts, HostRegistration} from '../dev/layout-dump/index';
import {useHostRef} from './focus/useHostRef';

type Facts = HostFacts & Readonly<{kind: 'text' | 'view' | 'image'}>;
const dev = () => typeof __DEV__ !== 'undefined' && __DEV__;

/** Curated, DEV-only observations of existing hosts; no new native view or text renderer. */
export function useNativeObservation<T extends View | Text | Image>(facts: Facts, forwarded?: ForwardedRef<T>, textRevision?: ReactNode) {
  const lease = useRef<HostRegistration | undefined>(undefined), input = useRef(facts);
  const renderedDeclaration = useRef<Facts['flattenedStyle']>(undefined);
  renderedDeclaration.current = undefined;
  const onHost = useCallback((host: T | null) => {
    lease.current?.unregister(); lease.current = undefined;
    if (typeof __DEV__ !== 'undefined' && __DEV__ && host) {
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
  const recordFocus = useCallback((event: Readonly<{nativeEvent: Readonly<{target?: number}>}>, focused: boolean) => {
    if (native.current && findNodeHandle(native.current) === event.nativeEvent.target) lease.current?.recordNativeFocus(focused);
  }, [native]);
  // Called only by the real Pressable style resolver, after its actual current
  // state has evaluated the caller's style. No callback replay or React state.
  const recordDeclaredStyle = useCallback((flattenedStyle: Facts['flattenedStyle']) => {
    if (!dev() || flattenedStyle === undefined) return;
    renderedDeclaration.current = flattenedStyle;
    const current = {...input.current, flattenedStyle, declaredStyleComplete: true};
    input.current = current; lease.current?.update(current);
  }, []);
  return {ref: dev() ? ref : forwarded, textProps: dev() ? {ref, onTextLayout} : {}, recordFocus, recordDeclaredStyle};
}
