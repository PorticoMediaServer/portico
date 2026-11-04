import React, {createContext, useEffect, useLayoutEffect, useRef} from 'react';
import type {View} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useMotion, useReducedMotion} from '../useMotion';
import {useTheme} from '../theme';
import {alpha} from './visual';

/** Settings search's row targeting, read by every `SettingsRow` under it (rows are matched by `testID`, the settings row id).
 * - `open`: TV search results (tv-more 34). OK on a match opens that row's page instead of acting on the row.
 * - `highlight`: TV, the page a match opened. That row is drawn highlighted.
 * - `flash`: phone and tablet (settings.html). The row a result opened scrolls to the middle (`reveal`) and flashes once. */
export type SettingsRowTargeting = Readonly<{
  open?: (rowId: string) => void;
  highlight?: string;
  flash?: Readonly<{rowId: string; serial: number; reveal: (row: View) => void}>;
}>;
export const SettingsRowTargetContext = createContext<SettingsRowTargeting>({});

/** settings.html `.r.pulse`: the row's background goes from the accent at 34% to clear over 1.6 s, once. With reduced motion
 * there is no flash. Its box is the row's box, so it is also what `reveal` measures, once per opening (`serial`). */
export function SettingsRowFlash({serial, reveal}: Readonly<{serial: number; reveal: (row: View) => void}>) {
  const theme = useTheme(), motion = useMotion(), reduce = useReducedMotion();
  const box = useRef<View>(null), revealed = useRef<number | undefined>(undefined);
  const opacity = useSharedValue(reduce ? 0 : 1);
  useEffect(() => {
    if (reduce) {opacity.value = 0; return;}
    opacity.value = 1;
    opacity.value = withTiming(0, {...motion.timing('standard', 'out'), duration: 1600});
  }, [serial, reduce, motion, opacity]);
  const style = useAnimatedStyle(() => ({opacity: opacity.value}));
  const place = () => {
    if (revealed.current === serial || !box.current) return;
    revealed.current = serial; reveal(box.current);
  };
  // Each opening (serial) reveals once, as soon as its row is committed: a row that is already laid out sends no new
  // layout event when it is opened again. A later layout of the same opening measures nothing more.
  useLayoutEffect(place, [serial]); // eslint-disable-line react-hooks/exhaustive-deps
  return <Animated.View ref={box} collapsable={false} pointerEvents="none" onLayout={place}
    style={[{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, backgroundColor: alpha(theme.colors.accent, .34)}, style]}/>;
}
