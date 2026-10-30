import React, {useEffect, type ReactNode} from 'react';
import type {StyleProp, ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';

export type SelectionHoldFeedbackProps = {active: boolean; progress: number | null; children?: ReactNode; style?: StyleProp<ViewStyle>};

/** Q6/tablet9, motion2.16: feedback belongs to the item; the selection hint is retired. */
export function SelectionHoldFeedback({active, progress, children, style}: SelectionHoldFeedbackProps) {
  const {isTV} = useTheme(), motion = useMotion(), scale = useSharedValue(1);
  useEffect(() => {
    const next = isTV || !motion.transforms || !active ? 1 : progress === 1 ? 1.05 : .98;
    const spring = motion.spring('snap');
    scale.value = next === 1.05 && spring ? withSpring(next, spring) : withTiming(next, motion.timing('quick'));
  }, [active, progress, isTV, motion, scale]);
  const animated = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  if (isTV) return <>{children}</>;
  return <Animated.View style={[style, animated]}>{children}</Animated.View>;
}
