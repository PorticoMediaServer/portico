import React, {useLayoutEffect, useMemo} from 'react';
import type {ViewProps} from 'react-native';
import Animated, {cancelAnimation, useAnimatedStyle, useSharedValue, withTiming, type EntryExitAnimationFunction, type LayoutAnimationFunction} from 'react-native-reanimated';
import {useMotion} from '../useMotion';

/** Yoga adopts the final geometry; only the retained visual translation animates. */
/** `enter` fades a genuinely inserted item; cached or windowed mounts appear at once (motion.html 2.14). */
export function MotionItem({mode = 'row', enter: animateEnter = true, style, ...props}: ViewProps & {mode?: 'row' | 'section'; enter?: boolean}) {
  const motion = useMotion(), x = useSharedValue(0), y = useSharedValue(0);
  const transforms = motion.transforms;
  const shift = useMemo(() => motion.timing(mode === 'row' ? 'slow' : 'standard', 'inOut'), [mode, motion]);
  const enter = useMemo(() => motion.timing('quick'), [motion]);
  const leave = useMemo(() => motion.timing(mode === 'row' ? 'standard' : 'quick'), [mode, motion]);
  const layout = useMemo<LayoutAnimationFunction>(() => values => {
    'worklet';
    // Add the currently animated translation before retargeting an interrupted move.
    const dx = transforms ? values.currentOriginX + x.value - values.targetOriginX : 0;
    const dy = transforms ? values.currentOriginY + y.value - values.targetOriginY : 0;
    cancelAnimation(x); cancelAnimation(y);
    x.value = dx; y.value = dy;
    x.value = withTiming(0, shift); y.value = withTiming(0, shift);
    return {initialValues: {originX: values.targetOriginX, originY: values.targetOriginY, width: values.targetWidth, height: values.targetHeight}, animations: {}};
  }, [shift, transforms, x, y]);
  const entering = useMemo<EntryExitAnimationFunction>(() => () => {
    'worklet';
    return {initialValues: {opacity: 0}, animations: {opacity: withTiming(1, enter)}};
  }, [enter]);
  const exiting = useMemo<EntryExitAnimationFunction>(() => () => {
    'worklet';
    cancelAnimation(x); cancelAnimation(y);
    return {initialValues: {opacity: 1, transform: [{translateX: x.value}, {translateY: y.value}, {scale: 1}]},
      animations: {opacity: withTiming(0, leave), transform: [{translateX: x.value}, {translateY: y.value}, {scale: withTiming(transforms && mode === 'row' ? .96 : 1, leave)}]}};
  }, [leave, mode, transforms, x, y]);
  useLayoutEffect(() => {if (!transforms) {cancelAnimation(x); cancelAnimation(y); x.value = 0; y.value = 0;}}, [transforms, x, y]);
  const position = useAnimatedStyle(() => ({transform: [{translateX: x.value}, {translateY: y.value}]}));
  return <Animated.View {...props} collapsable={false} entering={animateEnter && motion.effects?.enterFades !== false ? entering : undefined} exiting={exiting} layout={layout} style={[style, position]} />;
}
