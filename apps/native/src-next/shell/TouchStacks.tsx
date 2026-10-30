import React, {useCallback, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {Platform, StyleSheet, View} from 'react-native';
import Animated, {Easing, ReduceMotion, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {ScreenStack, ScreenStackItem, type StackAnimationTypes} from 'react-native-screens';
import {NativeStackSlotOutlet, type NativeStackSlots} from '../kit/navigation/stack-slot';
import {isModalRoot, type NativeRoutePresentation, type NativeShellRoute, type NativeTabStack} from './routes';
import type {NativeTouchStacksPort} from './runtime';

/** The central motion tier (P2-70). Until it lands, Reduce Motion reads as `reduced`. */
export type NativeStackMotionTier = 'full' | 'reduced' | 'lite';
export const nativeStackMotionTier = (motion: Readonly<{reduce: boolean; tier?: NativeStackMotionTier}>): NativeStackMotionTier =>
  motion.tier ?? (motion.reduce ? 'reduced' : 'full');
/** motion.html 2.9/2.10: the system's own push and pop (iOS slide with the edge swipe,
 * Android's default). Cheaper tiers cross-fade (`reduced`) or cut (`lite`); the iOS
 * edge swipe still follows the finger in every tier. */
export const nativeStackAnimation = (tier: NativeStackMotionTier): StackAnimationTypes =>
  tier === 'full' ? 'default' : 'none';
/** M18: `reduced` is motion.html's 160 ms cross-fade (push and pop alike), drawn here rather than by the platform's
 * animators, which Android's "Remove animations" scales to nothing (React Native reports that
 * setting as Reduce Motion). Tab switches get their incoming fade (2.11: 120 ms; 160 reduced). */
export const NATIVE_PUSH_FADE_MS = 160;
export const nativeTabFadeMs = (tier: NativeStackMotionTier) => tier === 'full' ? 120 : tier === 'reduced' ? 160 : 0;

/** Fades its content in when `run` turns true (`appear`: also when it mounts running), unless `ms` is 0. */
function FadeIn({run, ms, appear = false, children, style}: {run: boolean; ms: number; appear?: boolean; children: ReactNode; style?: object}) {
  const opacity = useSharedValue(appear && run && ms > 0 ? 0 : 1);
  const previous = useRef<boolean | undefined>(undefined);
  React.useLayoutEffect(() => {
    const was = previous.current; previous.current = run;
    if (!run || ms <= 0 || was === true || was === undefined && !appear) return;
    if (was === false) opacity.value = 0;
    opacity.value = withTiming(1, {duration: ms, easing: Easing.linear, reduceMotion: ReduceMotion.Never});
  }, [run, ms, appear, opacity]);
  const animated = useAnimatedStyle(() => ({opacity: opacity.value}));
  return <Animated.View pointerEvents="box-none" style={[StyleSheet.absoluteFill, style, animated]}>{children}</Animated.View>;
}

/** `tabActive`: this page's tab is the one shown; `top`: it is also the page on screen. */
export type NativeStackPage = Readonly<{presentation: NativeRoutePresentation; top: boolean; tabActive: boolean; modal: boolean}>;

/** Touch route host: one native stack per visited tab, plus Settings' modal stack.
 * Tabs never slide (2.11); the shell fades the page area when the tab changes. */
export function TouchStacks({port, slots, tier, background, renderShell, routeStyle, onResync}: {
  port: NativeTouchStacksPort; slots: NativeStackSlots; tier: NativeStackMotionTier; background: string;
  /** A tab's first screen or another shell destination (Home, Library, Settings...). */
  renderShell: (page: NativeStackPage) => ReactNode;
  /** Wraps a pushed page and its retained views (tab bar inset, the player's first paint). */
  routeStyle?: (route: NativeShellRoute, content: ReactNode) => ReactNode;
  onResync?: () => void;
}) {
  const value = useSyncExternalStore(port.subscribe, port.getSnapshot, port.getSnapshot);
  return <View pointerEvents="box-none" style={StyleSheet.absoluteFill}>
    {value.stacks.map(stack => <TabStack key={stack.tab} stack={stack} active={stack.tab === value.active} port={port} slots={slots} tier={tier}
      background={background} renderShell={renderShell} routeStyle={routeStyle} onResync={onResync}/>)}
  </View>;
}

function TabStack({stack, active, port, slots, tier, background, renderShell, routeStyle, onResync}: {
  stack: NativeTabStack; active: boolean; port: NativeTouchStacksPort; slots: NativeStackSlots; tier: NativeStackMotionTier; background: string;
  renderShell: (page: NativeStackPage) => ReactNode; routeStyle?: (route: NativeShellRoute, content: ReactNode) => ReactNode; onResync?: () => void;
}) {
  const presentations = stack.presentations, modalAt = presentations.findIndex(value => isModalRoot(value.route));
  const base = modalAt > 0 ? presentations.slice(0, modalAt) : presentations, modal = modalAt > 0 ? presentations.slice(modalAt) : [];
  const covered = !active || modal.length > 0;
  return <View pointerEvents={active ? 'box-none' : 'none'} style={[StyleSheet.absoluteFill, active ? null : {display: 'none'}]}>
    <FadeIn run={active} ms={nativeTabFadeMs(tier)}>
    <View pointerEvents={covered ? 'none' : 'box-none'} accessibilityElementsHidden={covered} importantForAccessibility={covered ? 'no-hide-descendants' : 'auto'} style={StyleSheet.absoluteFill}>
      <Stack id={`${stack.tab}:base`} presentations={base} modal={false} tabActive={active} active={active && !modal.length} port={port} slots={slots} tier={tier}
        background={background} renderShell={renderShell} routeStyle={routeStyle} onResync={onResync}/>
    </View>
    {modal.length > 0 && <View pointerEvents="box-none" style={StyleSheet.absoluteFill}>
      <Stack id={`${stack.tab}:modal`} presentations={modal} modal tabActive={active} active={active} port={port} slots={slots} tier={tier}
        background={background} renderShell={renderShell} routeStyle={routeStyle} onResync={onResync}/>
    </View>}
    </FadeIn>
  </View>;
}

function Stack({id, presentations, modal, tabActive, active, port, slots, tier, background, renderShell, routeStyle, onResync}: {
  id: string; presentations: readonly NativeRoutePresentation[]; modal: boolean; tabActive: boolean; active: boolean; port: NativeTouchStacksPort; slots: NativeStackSlots;
  tier: NativeStackMotionTier; background: string; renderShell: (page: NativeStackPage) => ReactNode; routeStyle?: (route: NativeShellRoute, content: ReactNode) => ReactNode; onResync?: () => void;
}) {
  // A refused native pop leaves the system's stack ahead of navigation; a fresh
  // native stack is the only way back into agreement (the pages' modules keep their state).
  const [generation, setGeneration] = useState(0);
  const latest = useRef(presentations); latest.current = presentations;
  // An iOS edge swipe already showed the page underneath under the finger: it doesn't fade again.
  const swiped = useRef(false);
  React.useEffect(() => {swiped.current = false;});
  const dismissed = useCallback((count: number) => {
    if (Platform.OS === 'ios') swiped.current = true;
    for (let index = 0; index < count; index++) {
      const top = latest.current.at(-1);
      if (!top || latest.current.length < 2 || !port.commitNativePop(top.occurrence)) {setGeneration(value => value + 1); onResync?.(); return;}
      latest.current = latest.current.slice(0, -1);
    }
  }, [onResync, port]);
  const animation = nativeStackAnimation(tier);
  return <ScreenStack key={generation} style={StyleSheet.absoluteFill}>
    {presentations.map((presentation, index) => {
      const top = active && index === presentations.length - 1, shell = presentation.route.kind === 'shell';
      // The first screen keeps one native identity: switching library in place never slides.
      const key = index === 0 ? `${id}:root` : presentation.occurrence;
      const outlet = <NativeStackSlotOutlet slots={slots} route={presentation.route}/>;
      // A first screen's retained views (Search) cover the whole screen as before; a pushed
      // page and its retained views share one box.
      const page = shell ? <>{renderShell({presentation, top, tabActive, modal: modal && index === 0})}{outlet}</>
        : routeStyle ? routeStyle(presentation.route, <>{port.renderRoute(presentation.route)}{outlet}</>) : <>{port.renderRoute(presentation.route)}{outlet}</>;
      // Reduced: a pushed page fades in, and so does the page a pop returns to (the same 160 ms).
      // Only this stack's own pushes and pops count; a tab switch has the tab's fade.
      const content = tier === 'reduced' ? <FadeIn run={index === presentations.length - 1} appear={index > 0} ms={swiped.current ? 0 : NATIVE_PUSH_FADE_MS}>{page}</FadeIn> : page;
      return <ScreenStackItem key={key} screenId={key} headerConfig={{hidden: true}} stackPresentation="push" stackAnimation={animation}
        gestureEnabled={index > 0} onDismissed={index > 0 ? event => dismissed(event.nativeEvent.dismissCount) : undefined}
        style={StyleSheet.absoluteFill} contentStyle={{flex: 1, backgroundColor: modal && index === 0 ? 'transparent' : background}}>
        {content}
      </ScreenStackItem>;
    })}
  </ScreenStack>;
}
