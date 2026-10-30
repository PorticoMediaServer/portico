import React, {createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {StyleSheet, TVFocusGuideView, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withTiming, type SharedValue} from 'react-native-reanimated';
import {useMotion} from '../useMotion';

/** Retain presentation only. Controllers retire their actions as soon as they close. */
export function useTVPanelPresence<T>(value: T | undefined, scope: unknown) {
  const last = useRef<{value: T; scope: unknown} | undefined>(undefined);
  const latest = useRef(value); latest.current = value;
  const [present, setPresent] = useState(value !== undefined);
  useLayoutEffect(() => {
    if (value !== undefined) {last.current = {value, scope}; setPresent(true);}
  }, [value, scope]);
  const exited = useCallback(() => {
    // A reversed exit must not discard the new opening.
    if (latest.current !== undefined) return;
    last.current = undefined; setPresent(false);
  }, []);
  const retained = last.current;
  return {value: value ?? (present && retained && retained.scope === scope ? retained.value : undefined), open: value !== undefined, exited};
}

/** Completion ownership survives queued JS callbacks without extending any animation. */
export function useTVPresentationCompletion() {
  const generation = useRef(0);
  return useMemo(() => ({
    begin: () => ++generation.current,
    current: (ticket: number) => generation.current === ticket,
    cancel: (ticket: number) => {if (generation.current === ticket) generation.current++;},
  }), []);
}

type PanelMotion = {progress: SharedValue<number>; reduced: boolean; interactive: boolean};
const Context = createContext<PanelMotion | undefined>(undefined);
export function useTVPanelInteractive() {return useContext(Context)?.interactive ?? true;}

/** motion.html 4.7: one interruptible UI-thread path for every custom TV panel. */
export function TVPanelTransition({open, onExited, children}: {open: boolean; onExited: () => void; children: ReactNode}) {
  const motion = useMotion(), progress = useSharedValue(0), [landed, setLanded] = useState(false);
  const exitRef = useRef(onExited); exitRef.current = onExited;
  const completion = useTVPresentationCompletion();
  const finished = useCallback((ticket: number, arrived: boolean) => {
    if (!completion.current(ticket)) return;
    setLanded(arrived);
    if (!arrived) exitRef.current();
  }, [completion]);
  useEffect(() => {
    const ticket = completion.begin();
    setLanded(false);
    progress.value = withTiming(open ? 1 : 0, {...motion.timing(open ? 'slow' : 'quick', open ? 'out' : 'in'),
      duration: motion.reduce ? 160 : open ? 320 : 240}, done => {if (done) runOnJS(finished)(ticket, open);});
    return () => {completion.cancel(ticket); cancelAnimation(progress);};
  }, [open, motion, progress, finished, completion]);
  return <Context.Provider value={{progress, reduced: motion.reduce, interactive: open && landed}}>{children}</Context.Provider>;
}

export function TVPanelScrim({color = '#0710189e'}: {color?: string}) {
  const context = useContext(Context);
  const fade = useAnimatedStyle(() => ({opacity: context?.progress.value ?? 1}));
  return <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: color}, fade]}/>;
}

/** An ancestor guide gates native admission until landing; it chooses no targets. */
export function TVPanelSlide({width, children, style}: {width: number; children: ReactNode; style?: StyleProp<ViewStyle>}) {
  const context = useContext(Context), interactive = context?.interactive ?? true;
  const slide = useAnimatedStyle(() => ({opacity: context?.reduced ? context.progress.value : 1,
    transform: [{translateX: context && !context.reduced ? (1 - context.progress.value) * width : 0}]}));
  return <Animated.View pointerEvents={interactive ? 'box-none' : 'none'} accessibilityElementsHidden={!interactive}
    importantForAccessibility={interactive ? 'auto' : 'no-hide-descendants'} style={[{flex: 1}, style, slide]}>
    <TVFocusGuideView autoFocus focusable={interactive} style={{flex: 1}}>{children}</TVFocusGuideView>
  </Animated.View>;
}
