import React, {createContext, useCallback, useContext, useEffect, useRef, useState} from 'react';
import {type StyleProp, type ViewStyle} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withDelay, withTiming} from 'react-native-reanimated';
import {useMotion} from '../../kit/useMotion';
import {useTVPresentationCompletion} from '../../kit/overlays/TVPanelMotion';

const Page = createContext({open: true, ready: true});
export function useTVPageMotion() {return useContext(Page);}

/** motion4.4: the transport is group1; native entry follows its landing by120ms. */
export function TVPageTransition({open, onExited, children}: {open: boolean; onExited: () => void; children: React.ReactNode}) {
  const motion = useMotion(), completion = useSharedValue(0), [ready, setReady] = useState(false);
  const latest = useRef({open, onExited}); latest.current = {open, onExited};
  const guard = useTVPresentationCompletion();
  const finished = useCallback((entered: boolean, run: number) => {
    if (!guard.current(run) || latest.current.open !== entered) return;
    setReady(entered);
    if (!entered) latest.current.onExited();
  }, [guard]);
  useEffect(() => {
    const run = guard.begin();
    setReady(false);
    completion.value = 0;
    completion.value = withDelay(open ? motion.reduce ? 160 : 60 + 180 + 120 : 0,
      withTiming(1, {...motion.timing('quick', 'in'), duration: open ? 0 : motion.reduce ? 160 : 120}, done => {if (done) runOnJS(finished)(open, run);}));
    return () => {guard.cancel(run); cancelAnimation(completion);};
  }, [open, motion, completion, finished, guard]);
  return <Page.Provider value={{open, ready: open && ready}}>{children}</Page.Provider>;
}

/** Logical point movement inside the TV canvas; the ambient backdrop stays outside. */
export function TVPageGroup({index, style, children}: {index: number; style?: StyleProp<ViewStyle>; children: React.ReactNode}) {
  const {open} = useTVPageMotion(), motion = useMotion(), progress = useSharedValue(0);
  useEffect(() => {
    progress.value = withDelay(open && !motion.reduce ? index * 60 : 0,
      withTiming(open ? 1 : 0, {...motion.timing(open ? 'standard' : 'quick', open ? 'out' : 'in'), duration: motion.reduce ? 160 : open ? 180 : 120}));
    return () => cancelAnimation(progress);
  }, [open, motion, index, progress]);
  const animated = useAnimatedStyle(() => ({opacity: progress.value, transform: motion.transforms ? [{translateY: (1 - progress.value) * 24}] : []}));
  return <Animated.View pointerEvents="box-none" style={[style, animated]}>{children}</Animated.View>;
}
