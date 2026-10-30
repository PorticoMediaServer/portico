import React, {forwardRef, useCallback, useEffect, useRef, useState} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import {toggleSpec} from '@portico/design';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {Focusable, type FocusableProps} from './Focusable';
import {minimumHitSlop, switchDragFraction, switchReleaseValue} from './control-model';
import {useControlValue} from './useControlValue';

export type SwitchVariant = 'compact' | 'phone' | 'episode' | 'selection';
export type SwitchProps = Omit<FocusableProps, 'children'> & {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  variant?: SwitchVariant;
  children?: React.ReactNode;
  description?: React.ReactNode;
};

export const Switch = forwardRef<View, SwitchProps>(function Switch({
  checked, defaultChecked = false, onCheckedChange, variant = 'phone', children, description,
  disabled, style, onPress, accessibilityState, hitSlop, ...props
}, ref) {
  const theme = useTheme();
  const motion = useMotion();
  const [value, setValue] = useControlValue(checked, defaultChecked, onCheckedChange);
  const geometry = theme.isTV ? {width: 64, height: 36, thumb: 28, inset: 4, onLeft: 32}
    : variant === 'selection' ? {width: 44, height: 26, thumb: 20, inset: 3, onLeft: 21}
      : variant === 'episode' ? {width: 40, height: 24, thumb: 18, inset: 3, onLeft: 19}
        : variant === 'compact' ? {width: 38, height: 22, thumb: 16, inset: 3, onLeft: 19}
          : {width: 48, height: 29, thumb: 23, inset: 3, onLeft: 22};
  const offThumb = theme.isTV || variant === 'compact' || variant === 'selection' ? theme.colors.textTertiary : theme.colors.textSecondary;
  const position = useSharedValue(value ? 1 : 0);
  const dragging = useSharedValue(0);
  const start = useSharedValue(0);
  const committed = useSharedValue(false);
  const gestureTarget = useRef<boolean | undefined>(undefined);
  const [release, setRelease] = useState(0);
  useEffect(() => {
    const fromGesture = gestureTarget.current === value;
    gestureTarget.current = undefined;
    if (!fromGesture) position.value = withTiming(value ? 1 : 0, motion.timing('quick'));
  }, [value, motion, position, release]);
  const onLayer = useAnimatedStyle(() => ({opacity: position.value}));
  const offThumbLayer = useAnimatedStyle(() => ({opacity: (1 - dragging.value) * (1 - position.value)}));
  const onThumbLayer = useAnimatedStyle(() => ({opacity: (1 - dragging.value) * position.value}));
  const thumb = useAnimatedStyle(() => ({opacity: motion.reduce ? dragging.value : 1, transform: motion.reduce && !dragging.value ? [] : [{translateX: position.value * (geometry.onLeft - geometry.inset)}]}));
  const commitGesture = useCallback((next: boolean) => {
    if (!disabled) {gestureTarget.current = next; setValue(next);}
    setRelease(previous => previous + 1);
  }, [disabled, setValue]);
  const releaseSpring = motion.spring('snap');
  const releaseTiming = motion.timing('quick');
  const travel = geometry.onLeft - geometry.inset;
  const pan = Gesture.Pan().enabled(!theme.isTV && !disabled).activeOffsetX([-4, 4]).failOffsetY([-8, 8])
    .onBegin(() => {cancelAnimation(position); start.value = position.value; committed.value = false;})
    .onStart(() => {dragging.value = 1;})
    .onUpdate(event => {position.value = switchDragFraction(start.value, event.translationX, travel);})
    .onEnd(event => {
      const next = switchReleaseValue(position.value, event.velocityX, travel);
      committed.value = true;
      position.value = releaseSpring ? withSpring(next ? 1 : 0, {...releaseSpring, velocity: event.velocityX / travel}) : withTiming(next ? 1 : 0, releaseTiming);
      runOnJS(commitGesture)(next);
    })
    .onFinalize(() => {
      dragging.value = 0;
      if (!committed.value) position.value = withTiming(value ? 1 : 0, releaseTiming);
    });
  const slop = minimumHitSlop(geometry.height, theme.isTV);
  const labelled = children !== undefined;
  return <Focusable {...props} ref={ref} collapsable={false} disabled={disabled} accessibilityRole="switch" accessibilityState={{...accessibilityState, checked: value}}
    focusKind={labelled ? 'row' : 'button'} focusRadius={labelled ? 10 : geometry.height / 2}
    hitSlop={hitSlop ?? {top: slop, bottom: slop, left: slop, right: slop}}
    onPress={event => {if (!disabled) {setValue(!value); onPress?.(event);}}}
    style={state => [styles.row, labelled ? styles.labelled : undefined, {minHeight: labelled ? theme.isTV ? 64 : 44 : undefined, opacity: disabled ? toggleSpec.disabledOpacity : 1}, typeof style === 'function' ? style(state) : style]}>
    {labelled ? <View style={styles.copy}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{children}</Text>{description !== undefined ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text> : null}</View> : null}
    <GestureDetector gesture={pan}><View collapsable={false} accessible={false} importantForAccessibility="no-hide-descendants" style={{width: geometry.width, height: geometry.height}}>
      <View style={[StyleSheet.absoluteFill, {borderRadius: geometry.height / 2, backgroundColor: theme.colors.raisedBright}]} />
      <Animated.View style={[StyleSheet.absoluteFill, {borderRadius: geometry.height / 2, backgroundColor: theme.colors.accent}, onLayer]} />
      <Animated.View style={[styles.thumb, {width: geometry.thumb, height: geometry.thumb, borderRadius: geometry.thumb / 2, top: geometry.inset, left: geometry.inset, overflow: 'hidden', backgroundColor: offThumb}, thumb]}>
        <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: theme.colors.white}, onLayer]} />
      </Animated.View>
      {motion.reduce ? <>
        <Animated.View style={[styles.thumb, {width: geometry.thumb, height: geometry.thumb, borderRadius: geometry.thumb / 2, top: geometry.inset, left: geometry.inset, backgroundColor: offThumb}, offThumbLayer]} />
        <Animated.View style={[styles.thumb, {width: geometry.thumb, height: geometry.thumb, borderRadius: geometry.thumb / 2, top: geometry.inset, left: geometry.onLeft, backgroundColor: theme.colors.white}, onThumbLayer]} />
      </> : null}
    </View></GestureDetector>
  </Focusable>;
});

const styles = StyleSheet.create({row: {flexDirection: 'row', alignItems: 'center', gap: 16, alignSelf: 'flex-start'}, labelled: {alignSelf: 'stretch', minHeight: 44, paddingVertical: 8}, copy: {flex: 1, gap: 4}, thumb: {position: 'absolute'}});
