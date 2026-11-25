import React, {useCallback, useEffect, useRef, type ReactNode} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {useTheme} from '../../kit/theme';
import {shellType} from '../../kit/shell/visual';
import {useFocusRegistry} from '../../kit/focus';
import type {FocusAddress} from '../../kit/focus/registry';
import {registerBack} from '../navigation/back';
import type {ShellDestination} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';

/** A page's own paddings and title. It stands on its layer's backdrop and draws none of its own. */
export function Screen({children, title, style}: {children: ReactNode; title?: string; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme();
  return <View style={[{flex: 1, paddingLeft: theme.isTV ? 200 : 0, paddingRight: theme.isTV ? 96 : 0, paddingTop: theme.isTV ? 60 : 0}, style]}>
    {title && <Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : 32, 800, 'normal', -.035), {color: theme.colors.text, marginBottom: theme.isTV ? 38 : 16, marginHorizontal: theme.isTV ? 0 : 20}]}>{title}</Text>}
    {children}
  </View>;
}

/** First entry is requested only after the real target's native layout exists. */
export function useScreenEntry(target: FocusAddress | undefined, current: () => boolean) {
  const registry = useFocusRegistry(), entered = useRef(false), laidOut = useRef(false), frame = useRef<number | undefined>(undefined);
  const regionId = target?.regionId, childId = target?.childId;
  const request = useCallback(() => {
    if (!laidOut.current || !regionId || !childId || entered.current || frame.current !== undefined) return;
    frame.current = requestAnimationFrame(() => {frame.current = undefined; if (current() && !entered.current) entered.current = registry.focus({regionId, childId});});
  }, [current, registry, regionId, childId]);
  // Guide data and native child refs may arrive after the region's first layout. Retry only
  // for that real registration, and stop requesting as soon as a native host accepted entry.
  useEffect(() => {request(); return registry.subscribe(request);}, [registry, request]);
  useEffect(() => () => {if (frame.current !== undefined) cancelAnimationFrame(frame.current);}, []);
  return useCallback(() => {laidOut.current = true; request();}, [request]);
}

export function useScreenBack(runtime: NativeShellRuntime, handler: () => boolean, isTV: boolean, destination?: ShellDestination) {
  const key = JSON.stringify(destination ?? runtime.getSnapshot().destination);
  useEffect(() => {
    if (!isTV) return;
    const original = runtime.retirementSignal;
    if (original?.aborted) return;
    const off = registerBack(handler, {
      scope: 'page',
      active: () => {
        if (original?.aborted) return false;
        const now = runtime.getSnapshot();
        return now.contentVisible !== false && !now.mediaExpanded && !now.homeObscured && !now.selectionActive
          && JSON.stringify(now.destination) === key;
      },
    });
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off); off();};
  }, [runtime, handler, isTV, key]);
}

export function ScreenText({children, secondary = false}: {children: ReactNode; secondary?: boolean}) {
  const theme = useTheme();
  return <Text style={[shellType(theme.isTV ? 24 : 15, secondary ? 400 : 600, 'normal'), {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{children}</Text>;
}
