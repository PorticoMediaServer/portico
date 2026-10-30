import React, {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {AccessibilityInfo, Animated, Easing, Image, Platform, StyleSheet, Text, View} from 'react-native';
import Svg, {Circle} from 'react-native-svg';
import Reanimated, {cancelAnimation, Easing as ReanimatedEasing, runOnJS, useAnimatedStyle, useSharedValue, withDelay, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../packages/i18n/src';
import {ColourBackdrop} from '../kit/artwork';
import {nativeType, useTheme} from '../kit/theme';
import {useMotion} from '../kit/useMotion';
import {OVERLAY_PAINT_INERT, OVERLAY_PAINT_LIVE, OverlayPaintProvider} from '../kit/overlays/OverlayPaintContext';
import {startupWait, type NativeStartupWait} from './startup-recovery';

/** DESIGN's boot picture. The destination mounts in place beneath the departing
 * layer; a ready result is never held for a minimum spinner or wordmark time. */
export function StartupBoot({active, waiting, serverName, children, onHidden}: {
  active: boolean; waiting: NativeStartupWait; serverName?: string; children: React.ReactNode; onHidden?: () => void;
}) {
  const theme = useTheme(), motion = useMotion();
  const [shown, setShown] = useState(active), visible = useRef(active);
  const complete = useRef<(() => void) | undefined>(undefined);
  // The exit runs on the UI thread (Reanimated): a busy JavaScript thread drawing Home can't hold
  // the boot screen over a Home that is already painted (P2-74 4b: hidden 2.0 s after Home drew).
  const opacity = useSharedValue(active ? 1 : 0), contentOpacity = useSharedValue(active ? 0 : 1);
  const scale = useSharedValue(1), activity = useSharedValue(1);
  const finish = useCallback((done: boolean) => {if (done && !visible.current) setShown(false);}, []);
  useLayoutEffect(() => {
    if (active) {
      visible.current = true; complete.current = undefined; setShown(true);
      cancelAnimation(opacity); cancelAnimation(contentOpacity); cancelAnimation(scale); cancelAnimation(activity);
      opacity.value = 1; contentOpacity.value = 0; scale.value = 1; activity.value = 1;
      return;
    }
    if (!visible.current) return;
    const slow = motion.duration('slow'), quick = motion.duration('quick');
    visible.current = false; complete.current = onHidden;
    contentOpacity.value = withDelay(motion.reduce ? 0 : 60, withTiming(1, {duration: slow, easing: motion.reduce ? ReanimatedEasing.linear : ReanimatedEasing.bezier(.2, .8, .2, 1)}));
    scale.value = withTiming(motion.reduce ? 1 : 1.05, {duration: slow, easing: motion.reduce ? ReanimatedEasing.linear : ReanimatedEasing.bezier(.4, 0, .2, 1)});
    activity.value = withTiming(0, {duration: quick, easing: motion.reduce ? ReanimatedEasing.linear : ReanimatedEasing.bezier(.4, 0, 1, 1)});
    opacity.value = withTiming(0, {duration: slow, easing: motion.reduce ? ReanimatedEasing.linear : ReanimatedEasing.bezier(.4, 0, .2, 1)},
      done => {'worklet'; runOnJS(finish)(done === true);});
  }, [active, motion, opacity, contentOpacity, scale, activity, onHidden, finish]);
  const contentStyle = useAnimatedStyle(() => ({opacity: contentOpacity.value}));
  const bootStyle = useAnimatedStyle(() => ({opacity: opacity.value}));
  const wordmarkStyle = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  const activityStyle = useAnimatedStyle(() => ({opacity: activity.value}));
  useLayoutEffect(() => {
    if (shown || active) return;
    const callback = complete.current; complete.current = undefined; callback?.();
  }, [shown, active]);
  const phone = theme.platform === 'phone', tv = theme.isTV;
  // A television is laid out on the 1920 design canvas, so 640 is DESIGN's 640 pt (Apple TV, Vega)
  // and 320 dp (640 px at 1080p) on Android TV, Google TV and Fire TV.
  const wordmarkWidth = tv ? 640 : Platform.OS === 'ios' && (Platform as {isPad?: boolean}).isPad ? 224 : 176;
  const wordmarkHeight = wordmarkWidth * 405 / 1774;
  const diameter = tv ? 64 : phone ? 28 : 32, stroke = tv ? 5 : phone ? 2.5 : 3;
  const gap = tv ? 72 : phone ? 40 : 44, messageGap = tv ? 32 : phone ? 18 : 20;
  const size = tv ? 32 : phone ? 15 : 17;
  const connecting = serverName ? defaultI18n.t('web.chooser.connecting', {server: serverName}) : defaultI18n.t('next.boot.connecting');
  const stillConnecting = defaultI18n.t('next.boot.stillConnecting');
  const message = waiting.message === 'still-connecting' ? stillConnecting : waiting.message === 'connecting' ? connecting : '';
  useEffect(() => {
    if (active && message && Platform.OS === 'ios') AccessibilityInfo.announceForAccessibility(message);
  }, [active, message]);
  const label = waiting.spinner ? `${defaultI18n.t('accounts.brand')}, ${defaultI18n.t('status.loading')}` : defaultI18n.t('accounts.brand');
  return <View style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <Reanimated.View pointerEvents={shown ? 'none' : 'box-none'} accessibilityElementsHidden={shown} importantForAccessibility={shown ? 'no-hide-descendants' : 'auto'}
      style={[{flex: 1}, contentStyle]}><OverlayPaintProvider mode={shown ? OVERLAY_PAINT_INERT : OVERLAY_PAINT_LIVE}>{children}</OverlayPaintProvider></Reanimated.View>
    {shown && <Reanimated.View testID="native-startup-boot" accessible focusable={false} accessibilityLabel={message ? `${label}, ${message}` : label}
      accessibilityLiveRegion="polite" accessibilityState={{busy: true}}
      style={[StyleSheet.absoluteFillObject, {backgroundColor: theme.colors.ink, alignItems: 'center', justifyContent: 'center'}, bootStyle]}>
      <ColourBackdrop shape={phone ? 'top' : 'side'} fill={!phone}/>
      <Reanimated.View style={wordmarkStyle}><Image accessible={false} source={require('../../assets/wordmark.png')}
        resizeMode="contain" style={{width: wordmarkWidth, height: wordmarkHeight}}/></Reanimated.View>
      <Reanimated.View pointerEvents="none" style={[StyleSheet.absoluteFillObject, activityStyle]}>
        <View style={{position: 'absolute', top: '50%', left: '50%', marginLeft: -diameter / 2, marginTop: wordmarkHeight / 2 + gap}}>
          <BootSpinner shown={waiting.spinner} size={diameter} stroke={stroke}/>
        </View>
        <View style={{position: 'absolute', top: '50%', left: 24, right: 24, marginTop: wordmarkHeight / 2 + gap + diameter + messageGap}}>
          <BootMessage visible={waiting.message === 'connecting'} size={size}>{connecting}</BootMessage>
          <BootMessage visible={waiting.message === 'still-connecting'} size={size}>{stillConnecting}</BootMessage>
        </View>
      </Reanimated.View>
    </Reanimated.View>}
  </View>;
}

/** boot.html §3 for a wait that happens on a screen of its own (the profile pick, E-mobile-01):
 * the same ring at 1 s, "Connecting to {server}" at 3 s and "still connecting" at 8 s, in place,
 * under the screen's content. Nothing shows for a pick that answers within a second. */
export function BootWaitLine({active, serverName, testID}: {active: boolean; serverName?: string; testID?: string}) {
  const theme = useTheme(), [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (!active) {setElapsed(0); return;}
    const timers = [1_000, 3_000, 8_000].map(at => setTimeout(() => setElapsed(at), at));
    return () => timers.forEach(clearTimeout);
  }, [active]);
  const waiting = startupWait(active ? elapsed : 0);
  const phone = theme.platform === 'phone', tv = theme.isTV;
  const diameter = tv ? 64 : phone ? 28 : 32, stroke = tv ? 5 : phone ? 2.5 : 3, messageGap = tv ? 32 : phone ? 18 : 20, size = tv ? 32 : phone ? 15 : 17;
  const connecting = serverName ? defaultI18n.t('web.chooser.connecting', {server: serverName}) : defaultI18n.t('next.boot.connecting');
  const stillConnecting = defaultI18n.t('next.boot.stillConnecting');
  const message = waiting.message === 'still-connecting' ? stillConnecting : waiting.message === 'connecting' ? connecting : '';
  useEffect(() => {if (active && message && Platform.OS === 'ios') AccessibilityInfo.announceForAccessibility(message);}, [active, message]);
  if (!active) return null;
  return <View testID={testID} accessible={!!message} accessibilityLiveRegion="polite" accessibilityLabel={message || undefined} style={{alignItems: 'center', marginTop: tv ? 48 : 28}}>
    <BootSpinner shown={waiting.spinner} size={diameter} stroke={stroke}/>
    <View style={{alignSelf: 'stretch', marginTop: messageGap, minHeight: size * 1.45 * 2}}>
      <BootMessage visible={waiting.message === 'connecting'} size={size}>{connecting}</BootMessage>
      <BootMessage visible={waiting.message === 'still-connecting'} size={size}>{stillConnecting}</BootMessage>
    </View>
  </View>;
}

function BootMessage({visible, size, children}: {visible: boolean; size: number; children: string}) {
  const theme = useTheme(), motion = useMotion();
  const opacity = useRef(new Animated.Value(visible ? 1 : 0)).current;
  useEffect(() => {
    const fade = Animated.timing(opacity, {toValue: visible ? 1 : 0, duration: motion.duration('standard'),
      easing: motion.reduce ? Easing.linear : Easing.bezier(.2, .8, .2, 1), useNativeDriver: true});
    fade.start(); return () => fade.stop();
  }, [visible, motion, opacity]);
  return <Animated.View style={{position: 'absolute', left: 0, right: 0, opacity}}><Text accessible={false}
    style={[nativeType({size, lineHeight: size * 1.45, weight: 600, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, textAlign: 'center'}]}>{children}</Text></Animated.View>;
}

function BootSpinner({shown, size, stroke}: {shown: boolean; size: number; stroke: number}) {
  const theme = useTheme(), motion = useMotion();
  const turn = useRef(new Animated.Value(0)).current, pulse = useRef(new Animated.Value(1)).current, opacity = useRef(new Animated.Value(shown ? 1 : 0)).current;
  useEffect(() => {
    const fade = Animated.timing(opacity, {toValue: shown ? 1 : 0, duration: motion.duration('slow'),
      easing: motion.reduce ? Easing.linear : Easing.bezier(.2, .8, .2, 1), useNativeDriver: true});
    fade.start(); return () => fade.stop();
  }, [shown, motion, opacity]);
  useEffect(() => {
    turn.setValue(0); pulse.setValue(motion.reduce ? .4 : 1);
    if (!shown) return;
    const loop = Animated.loop(motion.reduce ? Animated.sequence([
      Animated.timing(pulse, {toValue: 1, duration: 800, easing: Easing.linear, useNativeDriver: true, isInteraction: false}),
      Animated.timing(pulse, {toValue: .4, duration: 800, easing: Easing.linear, useNativeDriver: true, isInteraction: false}),
    ]) : Animated.timing(turn, {toValue: 1, duration: 1_000, easing: Easing.linear, useNativeDriver: true, isInteraction: false}));
    loop.start(); return () => loop.stop();
  }, [shown, motion, turn, pulse]);
  const radius = (size - stroke) / 2, circumference = 2 * Math.PI * radius;
  return <Animated.View accessible={false} style={{width: size, height: size, opacity}}>
    <Animated.View style={{opacity: pulse, transform: [{rotate: turn.interpolate({inputRange: [0, 1], outputRange: ['0deg', '360deg']})}]}}>
      <Svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} accessible={false}>
        <Circle cx={size / 2} cy={size / 2} r={radius} fill="none" stroke={theme.colors.text} strokeOpacity={.16} strokeWidth={stroke}/>
        <Circle cx={size / 2} cy={size / 2} r={radius} fill="none" stroke={theme.colors.accentStrong} strokeWidth={stroke}
          strokeDasharray={`${circumference / 4} ${circumference * 3 / 4}`} transform={`rotate(-90 ${size / 2} ${size / 2})`}/>
      </Svg>
    </Animated.View>
  </Animated.View>;
}
