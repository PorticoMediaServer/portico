import React, {useCallback, useEffect, useRef, useState} from 'react';
import {AccessibilityInfo, AppState, StyleSheet, Text, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {useScreenReader} from '../useScreenReader';
import {dismissToastGesture} from './geometry';
import {toastClearance} from './toast-clearance';
import {createToastClock} from './toast-clock';
import {alpha, useSurfaceMotion} from './surface';
import type {BannerProps, ToastHostProps, ToastMessage, ToastProps} from './types';

export function Toast({id, message, accessibilityLabel, icon, leading, action, actionOnTV = false, onDismiss, onPauseChange, testID}: ToastProps) {
  const theme = useTheme();
  const motion = useMotion();
  const x = useSharedValue(0);
  const y = useSharedValue(0);
  const opacity = useSharedValue(1);
  const startX = useSharedValue(0);
  const startY = useSharedValue(0);
  const recipe = glassRecipe();
  const reduce = motion.reduce;
  const exitTiming = motion.timing('quick', 'in');
  const fadeTiming = motion.timing();
  const snapSpring = motion.spring('snap');
  useEffect(() => {x.value = 0; y.value = 0; opacity.value = 1;}, [id, x, y, opacity]);
  const pause = useCallback((paused: boolean, reason: 'touch' | 'focus' | 'gesture') => onPauseChange?.(paused, reason), [onPauseChange]);
  const dismissed = useCallback(() => onDismiss(id, 'gesture'), [id, onDismiss]);
  const pan = Gesture.Pan().enabled(!theme.isTV).activeOffsetX([-10, 10]).activeOffsetY([-10, 10]).onStart(() => {cancelAnimation(x); cancelAnimation(y); startX.value = x.value; startY.value = y.value; runOnJS(pause)(true, 'gesture');}).onUpdate(event => {
    x.value = startX.value + event.translationX; y.value = Math.max(0, startY.value + event.translationY);
  }).onEnd(event => {
    if (dismissToastGesture(x.value, y.value, event.velocityX, event.velocityY)) {
      opacity.value = withTiming(0, exitTiming);
      runOnJS(dismissed)();
    } else {
      x.value = reduce ? 0 : withSpring(0, {...snapSpring, velocity: event.velocityX});
      y.value = reduce ? 0 : withSpring(0, {...snapSpring, velocity: event.velocityY});
      if (reduce) {opacity.value = 0; opacity.value = withTiming(1, fadeTiming);}
    }
  }).onFinalize(() => {runOnJS(pause)(false, 'gesture');});
  const drag = useAnimatedStyle(() => ({opacity: opacity.value, transform: [{translateX: x.value}, {translateY: y.value}]}));
  return <GestureDetector gesture={pan}><Animated.View testID={testID} accessible focusable={false} accessibilityLabel={accessibilityLabel ?? (typeof message === 'string' ? message : undefined)} accessibilityLiveRegion="polite" style={[{flexDirection: 'row', alignItems: 'center', gap: 14, borderRadius: theme.isTV ? 20 : 14, overflow: 'hidden', padding: theme.isTV ? 20 : 10, paddingLeft: theme.isTV ? 28 : 18, backgroundColor: alpha(theme.colors.raisedBright, 0.95), borderColor: alpha(theme.colors.text, 0.1), borderWidth: 1, maxWidth: theme.isTV ? 900 : 560}, drag]}
    onTouchStart={() => pause(true, 'touch')} onTouchEnd={() => pause(false, 'touch')} onTouchCancel={() => pause(false, 'touch')}
    accessibilityActions={[{name: 'dismiss', label: defaultI18n.t('next.overlays.native.dismissToast')}, ...(action && (!theme.isTV || actionOnTV) ? [{name: 'activate', label: action.label}] : [])]}
    onAccessibilityAction={event => {if (event.nativeEvent.actionName === 'activate' && action) {action.onPress(); onDismiss(id, 'action');} else if (event.nativeEvent.actionName === 'dismiss') onDismiss(id, 'accessibility');}}>
    {recipe.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.raisedBright}/>}
    {leading}{icon && <Icon name={icon} size={theme.isTV ? 28 : 20} color={theme.colors.accentStrong}/>}
    <Text accessibilityLabel={accessibilityLabel} numberOfLines={theme.isTV ? 1 : 2} style={[theme.isTV ? theme.type.body : nativeType({size: 14.5, lineHeight: 'normal', weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, flexShrink: 1}]}>{message}</Text>
    {action && (!theme.isTV || actionOnTV) && <Button variant="glass" size="small" onFocus={() => pause(true, 'focus')} onBlur={() => pause(false, 'focus')} onPress={() => {action.onPress(); onDismiss(id, 'action');}}>{action.label}</Button>}
  </Animated.View></GestureDetector>;
}

export function ToastHost({toast, onDismiss, bottomOffset = 84, obstructionTop, topOffset = 80, testID}: ToastHostProps) {
  const theme = useTheme();
  const motion = useMotion();
  // Do not spend an Undo deadline before the OS answers the initial AT query.
  const screenReader = useScreenReader(true);
  const insets = useSafeAreaInsets();
  const [displayed, setDisplayed] = useState<ToastMessage | null>(toast);

  const latest = useRef(toast);
  latest.current = toast;

  const dismissed = useRef<string | null>(null);
  const opacity = useSharedValue(0);
  const entry = useSharedValue(0);
  const replaceTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const dismiss = useCallback<ToastHostProps['onDismiss']>((id, reason) => {
    if (latest.current?.id !== id || dismissed.current === id) return;
    dismissed.current = id;
    onDismiss(id, reason);
  }, [onDismiss]);
  const displayedId = displayed?.id;
  useEffect(() => {
    clearTimeout(replaceTimer.current);
    if (toast?.id === displayedId) {if (toast) setDisplayed(toast); return;}
    const replacing = toast != null && displayedId != null;
    opacity.value = withTiming(0, motion.timing('quick', 'in'));
    replaceTimer.current = setTimeout(() => {
      setDisplayed(toast);
      if (toast) {
        entry.value = replacing ? 1 : 0;
        opacity.value = withTiming(1, motion.timing('standard'));
        entry.value = withTiming(1, motion.timing('standard'));
      }
    }, displayedId ? motion.duration('quick') : 0);
    return () => clearTimeout(replaceTimer.current);
  }, [toast, displayedId, motion, opacity, entry]);
  const dismissRef = useRef(dismiss);
  dismissRef.current = dismiss;
  const [clock] = useState(() => createToastClock({now: Date.now, schedule: (run, delay) => setTimeout(run, delay), cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>)}, id => dismissRef.current(id, 'timeout')));
  const hasAction = Boolean(displayed?.action && (!theme.isTV || displayed.actionOnTV));
  const accessibleAction = Boolean(displayed?.action);
  useEffect(() => {clock.pause('screen-reader', accessibleAction && screenReader);}, [clock, accessibleAction, screenReader]);
  useEffect(() => {
    dismissed.current = null;
    clock.replace(displayed?.id ?? null, displayed?.durationMs ?? (hasAction ? theme.spec.feedback.toast.durationWithActionMs : theme.spec.feedback.toast.durationMs));
  }, [displayed?.id, displayed?.durationMs, hasAction, theme.spec.feedback.toast, clock]);
  const spokenMessage = displayed?.accessibilityLabel ?? (typeof displayed?.message === 'string' ? displayed.message : undefined);
  const spokenLabel = spokenMessage ? [spokenMessage, displayed?.action?.label].filter(Boolean).join(', ') : undefined;
  useEffect(() => {
    if (!displayedId) return;
    opacity.value = withTiming(1, motion.timing('standard'));
    entry.value = withTiming(1, motion.timing('standard'));
    if (spokenLabel) AccessibilityInfo.announceForAccessibility(spokenLabel);
  }, [displayedId, spokenLabel, motion, opacity, entry]);
  useEffect(() => {
    clock.pause('background', AppState.currentState !== 'active');
    const listener = AppState.addEventListener('change', state => clock.pause('background', state !== 'active'));
    return () => {listener.remove(); clock.dispose();};
  }, [clock]);
  const pause = useCallback((paused: boolean, reason = 'touch') => clock.pause(reason, paused), [clock]);
  const animation = useAnimatedStyle(() => ({opacity: opacity.value, transform: motion.reduce ? [] : [{translateY: (1 - entry.value) * 24}]}));
  if (!displayed) return null;
  return <View pointerEvents="box-none" style={[StyleSheet.absoluteFill, {zIndex: theme.layers.notice, justifyContent: theme.isTV ? 'flex-start' : 'flex-end', alignItems: theme.isTV ? 'flex-end' : 'center', paddingHorizontal: theme.isTV ? 96 : 16, paddingTop: topOffset + insets.top, paddingBottom: toastClearance({bottomOffset, safeBottom: insets.bottom, obstructionTop})}]}>
    <Animated.View style={animation}><Toast {...displayed} onDismiss={dismiss} onPauseChange={pause} testID={testID}/></Animated.View>
  </View>;
}

export function Banner({message, accessibilityLabel, variant = 'quiet', tone = 'warning', icon, action, visible = true, placement = 'overlay', topInset = 0, testID}: BannerProps) {
  const theme = useTheme();
  const animation = useSurfaceMotion(visible, 'banner');
  const recipe = glassRecipe();
  const toneColor = tone === 'warning' ? theme.colors.amber : tone === 'error' ? theme.colors.danger : tone === 'success' ? theme.colors.healthy : theme.colors.accentStrong;
  if (!animation.present) return null;
  return <View pointerEvents="box-none" style={placement === 'inline' ? {alignSelf: 'stretch'} : {position: 'absolute', top: topInset, left: 0, right: 0, zIndex: theme.layers.notice, alignItems: 'center', paddingHorizontal: theme.isTV ? 96 : 16}}>
    <Animated.View testID={testID} accessibilityLiveRegion="polite" style={[{flexDirection: 'row', alignItems: 'center', gap: variant === 'quiet' ? 10 : 14, borderRadius: variant === 'quiet' ? 20 : 14, paddingVertical: variant === 'quiet' ? 0 : 10, paddingLeft: variant === 'quiet' ? 14 : 16, paddingRight: action ? 10 : 18, minHeight: theme.isTV ? 64 : 40, backgroundColor: alpha(theme.colors.raised, 0.92), borderWidth: 1, borderColor: alpha(theme.colors.text, 0.1), overflow: 'hidden', maxWidth: theme.isTV ? 1200 : 720}, animation.style]}>
      {recipe.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.raised}/>}
      {variant === 'quiet' ? <View accessible={false} style={{width: 7, height: 7, borderRadius: 4, backgroundColor: toneColor}}/> : <Icon name={icon ?? 'i-info'} color={toneColor} size={theme.isTV ? 28 : 20}/>}
      <Text accessible accessibilityLabel={accessibilityLabel} style={[theme.isTV ? theme.type.bodyStrong : nativeType({size: variant === 'quiet' ? 14 : 14.5, lineHeight: variant === 'quiet' ? 'normal' : 21.025, weight: variant === 'quiet' ? 600 : 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, flexShrink: 1}]}>{message}</Text>
      {variant === 'notice' && action && <Button variant="glass" size="small" onPress={action.onPress}>{action.label}</Button>}
    </Animated.View>
  </View>;
}
