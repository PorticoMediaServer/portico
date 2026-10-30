import React, {createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {View, type StyleProp, type ViewStyle} from 'react-native';
import {Gesture, GestureDetector, type PanGesture} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, useAnimatedReaction, useAnimatedStyle, useSharedValue, withDelay, withTiming, type SharedValue} from 'react-native-reanimated';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {ArtworkSource} from '../artwork';
import {useMotion} from '../useMotion';
import {useTheme} from '../theme';
import type {OverlayPaintPermit} from '../overlays/SurfacePaintHost';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {CommittedPaintProvider} from '../overlays/CommittedPaint';
import {ListeningCoverPaint} from './ListeningCoverPaint';
import {validRect} from './model';
import type {DockRect, PlayerMorphController} from './types';

type Publication = Readonly<{fence: PlaybackFence; source?: ArtworkSource; controller: PlayerMorphController; scope: object | undefined; opening: object; pageOpen: boolean; current: () => boolean; subscribe?: (listener: () => void) => () => void}>;
export type ListeningScrubber = Readonly<{fence: PlaybackFence; gesture: PanGesture; scope: number;
  activeScope: SharedValue<number>; ticket: SharedValue<number>; drag: SharedValue<number | null>; base: SharedValue<number>; travel: SharedValue<number>;
  adjust: (direction: -1 | 1) => void}>;
type Slot = 'full' | 'mini';
type Frame = Readonly<{fence: PlaybackFence; scope: object | undefined; opening: object; revision: number; bounds: DockRect}>;
type ContextValue = Readonly<{canvas: RefObject<View | null>; revision: number; binding?: Publication; full?: Frame; mini?: Frame; decoded: boolean; active: boolean; pageAlpha: SharedValue<number>; pageHeight: SharedValue<number>; reportPageHeight: (height: number) => void; groups: readonly SharedValue<number>[]; pageOwner?: Readonly<{fence: PlaybackFence}>; holdPage: (owner: Readonly<{fence: PlaybackFence}>) => () => void;
  publish: (value: Publication) => () => void; report: (kind: Slot, value: Frame) => void}>;
const Context = createContext<ContextValue | undefined>(undefined);
const ScrubberPublication = createContext<((value: ListeningScrubber) => () => void) | undefined>(undefined);
const ScrubberValue = createContext<ListeningScrubber | undefined>(undefined);
const noSubscribe = () => () => {};
const notCurrent = () => false;
const sameSource = (a: ArtworkSource, b: ArtworkSource) => a.cacheKey === b.cacheKey && a.uri === b.uri
  && Object.keys(a.headers ?? {}).length === Object.keys(b.headers ?? {}).length
  && Object.keys(a.headers ?? {}).every(key => a.headers?.[key] === b.headers?.[key]);
const belongs = (frame: Frame | undefined, value: Publication | undefined, kind: Slot, revision: number): frame is Frame => !!frame && !!value
  && frame.revision === revision && frame.scope === value.scope && sameFence(frame.fence, value.fence) && (kind === 'mini' || frame.opening === value.opening);

/** Authority-scoped UI geometry and one passive cover image. No player, command,
 * read lifetime or output is owned here. A missing actual slot draws no morph. */
export function ListeningCoverProvider({children}: {children: React.ReactNode}) {
  const theme = useTheme(), motion = useMotion(), canvas = useRef<View>(null);
  const pageHeight = useSharedValue(0), pageAlpha = useSharedValue(0), first = useSharedValue(0), second = useSharedValue(0), third = useSharedValue(0);
  const startedScene = useSharedValue(0), enteredScene = useSharedValue(0);
  const reportPageHeight = useCallback((height: number) => {pageHeight.value = Number.isFinite(height) && height > 0 ? height : 0;}, [pageHeight]);
  const groups = useMemo(() => [first, second, third] as const, [first, second, third]);
  const [revision, setRevision] = useState(0);
  const [binding, setBinding] = useState<Publication>();
  const [scrubber, setScrubber] = useState<ListeningScrubber>();
  const publishScrubber = useCallback((value: ListeningScrubber) => {
    setScrubber(value);
    return () => setScrubber(current => current === value ? undefined : current);
  }, []);
  const [pageOwner, setPageOwner] = useState<Readonly<{fence: PlaybackFence}>>();
  const holdPage = useCallback((owner: Readonly<{fence: PlaybackFence}>) => {
    setPageOwner(owner);
    return () => setPageOwner(current => current === owner ? undefined : current);
  }, []);
  const [full, setFull] = useState<Frame>(), [mini, setMini] = useState<Frame>();
  const [decodedSource, setDecodedSource] = useState<{source: ArtworkSource; width: number; height: number}>();
  const publish = useCallback((value: Publication) => {
    setBinding(value);
    return () => setBinding(now => now === value ? undefined : now);
  }, []);
  const report = useCallback((kind: Slot, frame: Frame) => {
    const set = kind === 'full' ? setFull : setMini;
    set(previous => previous && previous.revision === frame.revision && previous.scope === frame.scope && previous.opening === frame.opening && sameFence(previous.fence, frame.fence)
      && Object.keys(frame.bounds).every(key => frame.bounds[key as keyof DockRect] === previous.bounds[key as keyof DockRect]) ? previous : frame);
  }, []);
  const active = useSyncExternalStore(binding?.subscribe ?? noSubscribe, binding?.current ?? notCurrent);
  const phase = useRef<{fence: PlaybackFence; scope: object | undefined; opening: object; pageOpen: boolean; ticket: number} | undefined>(undefined);
  const counter = useRef(0);
  if (binding) {
    const prior = phase.current, sameScope = prior?.scope === binding.scope, sameSource = !!prior && sameFence(prior.fence, binding.fence);
    // A first mini drag can precede the admitted full body. That handoff keeps
    // its clock, as does a new track while the same full page remains open.
    const handoff = !!prior && !prior.pageOpen && binding.pageOpen && sameScope && sameSource;
    const fresh = !prior || !sameScope || prior.opening !== binding.opening && !handoff || !sameSource && !binding.pageOpen;
    phase.current = {fence: binding.fence, scope: binding.scope, opening: binding.opening, pageOpen: binding.pageOpen,
      ticket: fresh ? ++counter.current : prior.ticket};
  }
  const ticket = phase.current?.ticket ?? 0, progress = binding?.controller.progress, dragging = binding?.controller.dragging;
  const goal = binding?.controller.presentationGoal, standard = motion.timing('standard'), quick = motion.timing('quick', 'in');
  const reduced = motion.reduce;
  useAnimatedReaction(() => ({valid: active && !!progress, ticket, p: progress?.value ?? 1, drag: dragging?.value ?? false,
    goal: goal?.value ?? (progress?.value === 1 ? 1 : 0), reduced}), (now, previous) => {
    if (!now.valid) {
      cancelAnimation(pageAlpha); pageAlpha.value = 0;
      // Invalid paint is hidden by active/fence matching. Keep only numeric
      // opacity so a same-page replacement need not replay the entrance.
      for (const value of [first, second, third]) cancelAnimation(value);
      return;
    }
    const fresh = startedScene.value !== now.ticket;
    if (fresh) {
      startedScene.value = now.ticket;
      for (const value of [first, second, third]) {cancelAnimation(value); value.value = 0;}
    }
    const entering = now.goal === 0 || now.drag && now.p < .9999;
    const wasEntering = previous && (previous.goal === 0 || previous.drag && previous.p < .9999);
    if (entering && (fresh || !wasEntering)) {
      const interrupted = enteredScene.value === now.ticket;
      enteredScene.value = now.ticket;
      first.value = withDelay(now.reduced || interrupted ? 0 : 260, withTiming(1, standard));
      second.value = withDelay(now.reduced ? 0 : interrupted ? 60 : 320, withTiming(1, standard));
      third.value = withDelay(now.reduced ? 0 : interrupted ? 120 : 380, withTiming(1, standard));
    } else if (!entering && (fresh || wasEntering)) {
      first.value = withTiming(0, quick); second.value = withTiming(0, quick); third.value = withTiming(0, quick);
    } else if (!previous?.valid || previous.reduced !== now.reduced) {
      first.value = withTiming(entering ? 1 : 0, entering ? standard : quick);
      second.value = withTiming(entering ? 1 : 0, entering ? standard : quick);
      third.value = withTiming(entering ? 1 : 0, entering ? standard : quick);
    }
    if (now.reduced && !now.drag) {
      if (fresh || !previous?.valid || previous.goal !== now.goal || previous.drag || previous.reduced !== now.reduced) pageAlpha.value = withTiming(now.goal === 0 ? 1 : 0, standard);
    } else {
      cancelAnimation(pageAlpha); pageAlpha.value = Math.max(0, Math.min(1, (1 - now.p) * 2.5));
    }
  }, [active, ticket, progress, dragging, goal, reduced, standard, quick, pageAlpha, first, second, third, startedScene, enteredScene]);
  useEffect(() => () => {cancelAnimation(pageAlpha); cancelAnimation(first); cancelAnimation(second); cancelAnimation(third);}, [pageAlpha, first, second, third]);
  const fullFrame = belongs(full, binding, 'full', revision) ? full.bounds : undefined, miniFrame = belongs(mini, binding, 'mini', revision) ? mini.bounds : undefined;
  const decodeFrame = fullFrame ?? miniFrame;
  const request = useMemo(() => ({binding, width: decodeFrame?.width, height: decodeFrame?.height}), [binding, decodeFrame?.width, decodeFrame?.height]);
  const latestRequest = useRef(request); latestRequest.current = request;
  const decoded = !!binding?.source && !!decodedSource && !!decodeFrame && sameSource(binding.source, decodedSource.source)
    && decodedSource.width === decodeFrame.width && decodedSource.height === decodeFrame.height;
  const value = useMemo(() => ({canvas, revision, binding, full, mini, decoded, active, pageAlpha, pageHeight, reportPageHeight, groups, pageOwner, holdPage, publish, report}), [revision, binding, full, mini, decoded, active, pageAlpha, pageHeight, reportPageHeight, groups, pageOwner, holdPage, publish, report]);
  return <Context.Provider value={value}><CommittedPaintProvider><ScrubberPublication.Provider value={publishScrubber}><ScrubberValue.Provider value={scrubber}>{children}{!theme.isTV && <View ref={canvas} collapsable={false} pointerEvents="none" accessible={false}
    accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={() => setRevision(value => value + 1)}
    style={{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}}>
    {active && binding?.source && miniFrame && decodeFrame && <CoverLayer binding={binding as Publication & {source: ArtworkSource}} full={fullFrame} mini={miniFrame} decoded={decoded}
      onReady={source => {if (latestRequest.current === request && binding.current() && binding.source && sameSource(source, binding.source)) setDecodedSource({source, width: decodeFrame.width, height: decodeFrame.height});}}/>}
  </View>}</ScrubberValue.Provider></ScrubberPublication.Provider></CommittedPaintProvider></Context.Provider>;
}

function CoverLayer({binding, full, mini, decoded, onReady}: {binding: Publication & {source: ArtworkSource}; full?: DockRect; mini: DockRect; decoded: boolean; onReady: (source: ArtworkSource) => void}) {
  const motion = useMotion(), {progress, dragging} = binding.controller;
  const moving = useAnimatedStyle(() => {
    const p = progress.value;
    const travelling = dragging.value || motion.transforms && p > .0001 && p < .9999;
    return {opacity: decoded && full && travelling ? 1 : 0};
  }, [decoded, full, motion.transforms, progress, dragging]);
  // Before the full slot exists, decode the same source at the actual mini frame.
  // This is never promoted to a guessed full target or shown as a moving cover.
  return <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
    style={[{position: 'absolute', inset: 0}, moving]}>
    <ListeningCoverPaint source={binding.source} full={full ?? mini} mini={mini} progress={progress} onReady={onReady}/>
  </Animated.View>;
}

export function useListeningCoverPublication(value: Publication | undefined) {
  const publish = useContext(Context)?.publish;
  useLayoutEffect(() => {if (publish && value) return publish(value);}, [publish, value]);
}

/** Measure the original native slot into the persistent host's coordinate space.
 * Retain only that measured geometry after a business publisher unmounts. Late
 * callbacks cannot publish through a replaced node, scope, fence or publication. */
export function useListeningCoverSlot(fence: PlaybackFence | undefined, kind: Slot, node: RefObject<View | null>, enabled = true) {
  const context = useContext(Context);
  const binding = context?.binding;
  const matching = !!fence && !!binding && sameFence(fence, binding.fence);
  const own = matching && enabled;
  const live = useRef(false), generation = useRef(0), latest = useRef({binding, own}); latest.current = {binding, own};
  useLayoutEffect(() => {const mounted = live, epoch = generation; mounted.current = true; return () => {mounted.current = false; epoch.current++;};}, []);
  const report = context?.report, canvas = context?.canvas, revision = context?.revision;
  const measure = useCallback(() => {
    void revision;
    const source = binding, element = node.current, host = canvas?.current, version = ++generation.current;
    if (!own || !source || !element || !host || !source.current()) return;
    const current = () => live.current && generation.current === version && node.current === element && canvas?.current === host
      && latest.current.binding === source && latest.current.own && source.current();
    element.measureInWindow((x, y, width, height) => {
      if (!current() || !validRect({x, y, width, height})) return;
      host.measureInWindow((hx, hy, hostWidth, hostHeight) => {
        if (!current() || !validRect({x: hx, y: hy, width: hostWidth, height: hostHeight})) return;
        report?.(kind, {fence: source.fence, scope: source.scope, opening: source.opening, revision: revision ?? 0, bounds: {x: x - hx, y: y - hy, width, height}});
      });
    });
  }, [binding, own, node, report, kind, canvas, revision]);
  useLayoutEffect(() => {measure();}, [measure]);
  const style = useListeningCoverPaint(fence, kind);
  return {measure, style};
}


/** Static props make this safe to capture as audited passive dock paint. The
 * animation handles are created inside the component, never frozen as data. */
export function ListeningCoverLocalPaint({fence, children, style}: {fence?: PlaybackFence; children: React.ReactNode; style?: StyleProp<ViewStyle>}) {
  const paint = useListeningCoverPaint(fence, 'mini');
  return <Animated.View style={[style, paint]}>{children}</Animated.View>;
}
function useListeningCoverPaint(fence: PlaybackFence | undefined, kind: Slot) {
  const context = useContext(Context), motion = useMotion(), paint = useOverlayPaintMode();
  const binding = context?.binding;
  const usable = paint.interactive && !!fence && !!binding && sameFence(fence, binding.fence) && context.active && context.decoded
    && belongs(context.full, binding, 'full', context.revision) && belongs(context.mini, binding, 'mini', context.revision);
  const matching = !!fence && !!binding && sameFence(fence, binding.fence) && context.active;
  const progress = binding?.controller.progress, dragging = binding?.controller.dragging, page = context?.pageAlpha, height = context?.pageHeight;
  return useAnimatedStyle(() => {
    const p = Math.max(0, Math.min(1, progress?.value ?? 0));
    const travelling = dragging?.value || motion.transforms && p > .0001 && p < .9999;
    if (usable && travelling) return {opacity: 0};
    if (kind === 'full' && matching) return {opacity: page?.value ?? 1,
      transform: [{translateY: motion.transforms || dragging?.value ? (height?.value ?? 0) * p : 0}]};
    return {opacity: 1};
  }, [usable, matching, kind, progress, dragging, page, height, motion.transforms]);
}


/** The page paint follows the same admitted player's UI progress. The measured
 * cover slot itself stays untransformed; only its painted image/background move. */
export function useListeningPageStyle(fence: PlaybackFence | undefined, height: number, fallbackVisible = false) {
  const context = useContext(Context), motion = useMotion();
  const binding = context?.binding;
  const hasBinding = !!binding;
  const current = !!fence && !!binding && sameFence(fence, binding.fence) && context.active;
  const progress = binding?.controller.progress, dragging = binding?.controller.dragging, pageAlpha = context?.pageAlpha, measuredHeight = context?.pageHeight;
  return useAnimatedStyle(() => {
    if (!hasBinding) return {opacity: fallbackVisible ? 1 : 0};
    if (!current || !progress) return {opacity: 0};
    const open = Math.max(0, Math.min(1, 1 - progress.value));
    return {opacity: pageAlpha?.value ?? Math.min(1, open * 2.5), transform: [{translateY: motion.transforms || dragging?.value ? (measuredHeight?.value ?? height) * (1 - open) : 0}]};
  }, [hasBinding, current, progress, dragging, pageAlpha, measuredHeight, height, fallbackVisible, motion.transforms]);
}


/** Three opacity groups share the original UI opening clock, so replacing the
 * mini preview by the admitted full body does not restart its delays. */
export function ListeningGroup({fence, index, height, children, style: layout}: {fence: PlaybackFence; index: 0 | 1 | 2; height: number; children: React.ReactNode; style?: StyleProp<ViewStyle>}) {
  const context = useContext(Context), motion = useMotion();
  const matching = !context?.binding || context.active && sameFence(context.binding.fence, fence);
  const alpha = context?.groups[index], page = context?.pageAlpha, measuredHeight = context?.pageHeight;
  const progress = context?.binding?.controller.progress, dragging = context?.binding?.controller.dragging;
  const style = useAnimatedStyle(() => {
    const p = Math.max(0, Math.min(1, progress?.value ?? 0));
    return {opacity: matching ? (alpha?.value ?? 1) * (page?.value ?? 1) : 0,
      transform: [{translateY: motion.transforms || dragging?.value ? (measuredHeight?.value ?? height) * p : 0}]};
  }, [matching, alpha, page, measuredHeight, progress, dragging, height, motion.transforms]);
  return <Animated.View style={[{width: '100%', alignItems: 'center'}, layout, style]}>{children}</Animated.View>;
}


export function useListeningPaintPermit(fence: PlaybackFence): OverlayPaintPermit | undefined {
  const binding = useContext(Context)?.binding;
  return useMemo(() => binding && sameFence(binding.fence, fence) ? {current: binding.current, subscribe: binding.subscribe ?? noSubscribe} : undefined, [binding, fence]);
}
export function useListeningPageOwner(fence: PlaybackFence) {
  const hold = useContext(Context)?.holdPage;
  const owner = useMemo(() => ({fence}), [fence]);
  useLayoutEffect(() => {if (hold) return hold(owner);}, [hold, owner]);
}
export function useListeningPagePresent(fence: PlaybackFence): boolean {
  const owner = useContext(Context)?.pageOwner;
  return !!owner && sameFence(owner.fence, fence);
}

export function useListeningPageHeight(): (height: number) => void {
  return useContext(Context)?.reportPageHeight ?? (() => {});
}

/** motion2.5 moves the actual phone bar below its own measured footprint. The
 * same page alpha supplies reduction; no second clock or window guess is used. */
export function useListeningTabBarPaint() {
  const context = useContext(Context), motion = useMotion(), height = useSharedValue(0);
  const active = !!context?.binding && context.active;
  const progress = context?.binding?.controller.progress, dragging = context?.binding?.controller.dragging, page = context?.pageAlpha;
  const measure = useCallback((value: number) => {height.value = Number.isFinite(value) && value > 0 ? value : 0;}, [height]);
  const style = useAnimatedStyle(() => {
    if (!active) return {opacity: 1, transform: [{translateY: 0}]};
    if (motion.reduce && !dragging?.value) return {opacity: 1 - (page?.value ?? 0), transform: [{translateY: 0}]};
    const open = Math.max(0, Math.min(1, 1 - (progress?.value ?? 1)));
    return {opacity: 1, transform: [{translateY: height.value * Math.min(1, open * 1.2)}]};
  }, [active, progress, dragging, page, height, motion.reduce]);
  return {style, measure};
}


export function useListeningMiniPaint(fence: PlaybackFence | undefined) {
  const context = useContext(Context), binding = context?.binding;
  return {active: !!fence && !!binding && sameFence(fence, binding.fence) && context.active,
    progress: binding?.controller.progress, dragging: binding?.controller.dragging, page: context?.pageAlpha};
}

/** Missing optional bridge keeps standalone callers' ordinary paint. A real
 * published opening is active only in its original current page and fence. */
export function useListeningPageActive(fence: PlaybackFence): boolean {
  const context = useContext(Context);
  return !context || !!context.binding && sameFence(context.binding.fence, fence) && context.active;
}


/** Live recogniser/preview handles belong to the business publisher and are
 * removed in its layout cleanup. The retained widget keeps only its own pixels. */
export function useListeningScrubberPublication(value: ListeningScrubber | undefined): boolean {
  const publish = useContext(ScrubberPublication);
  useLayoutEffect(() => {if (publish && value) return publish(value);}, [publish, value]);
  return !!publish;
}
export function ListeningSeekPaint({fence, fraction, span, seconds, label, testID, fallbackGesture, fallbackAdjust}: {
  fence: PlaybackFence; fraction: number; span: number; seconds: number; label: string; testID?: string; fallbackGesture?: PanGesture; fallbackAdjust?: (direction: -1 | 1) => void;
}) {
  const context = useContext(Context), mode = useOverlayPaintMode(), theme = useTheme();
  const published = useContext(ScrubberValue);
  const channel = published && sameFence(published.fence, fence) ? published : undefined;
  const live = mode.interactive && (!context || context.active), hasChannel = !!channel, owner = channel?.scope;
  const active = channel?.activeScope, ticket = channel?.ticket, drag = channel?.drag, base = channel?.base;
  const at = useSharedValue(fraction), width = useSharedValue(0);
  useAnimatedReaction(() => ({live, valid: hasChannel && active?.value === owner && (ticket?.value ?? 0) > 0, value: drag?.value ?? base?.value ?? fraction}), now => {
    // INERT holds the last actual preview; retiring the live publisher cannot
    // overwrite it with a guessed engine/rest position or a successor's cursor.
    if (now.live && now.valid) at.value = Math.max(0, Math.min(1, now.value));
  }, [live, hasChannel, active, owner, ticket, drag, base, fraction, at]);
  useLayoutEffect(() => {if (!context && mode.interactive) at.value = fraction;}, [context, mode.interactive, at, fraction]);
  useLayoutEffect(() => {if (live && channel && width.value > 0) channel.travel.value = width.value;}, [live, channel, width]);
  const fill = useAnimatedStyle(() => ({transform: [{translateX: -(1 - at.value) * width.value / 2}, {scaleX: at.value}]}));
  const disabled = useMemo(() => Gesture.Pan().enabled(false), []);
  const gesture = live ? channel?.gesture ?? fallbackGesture ?? disabled : disabled;
  const adjustable = live && span > 0 && !!(channel?.adjust ?? fallbackAdjust);
  return <GestureDetector gesture={gesture}><View testID={testID} accessible={live} accessibilityRole="adjustable" accessibilityLabel={label}
    accessibilityState={{disabled: !adjustable}}
    accessibilityValue={{min: 0, max: Math.round(span), now: Math.round(Math.max(0, Math.min(span, seconds)))}}
    accessibilityActions={adjustable ? [{name: 'increment'}, {name: 'decrement'}] : []}
    onAccessibilityAction={event => {
      if (!adjustable || channel && (active?.value !== owner || (ticket?.value ?? 0) <= 0)) return;
      const adjust = channel?.adjust ?? fallbackAdjust;
      if (event.nativeEvent.actionName === 'increment') adjust?.(1);
      else if (event.nativeEvent.actionName === 'decrement') adjust?.(-1);
    }}
    onLayout={event => {
      const next = event.nativeEvent.layout.width;
      width.value = Number.isFinite(next) && next > 0 ? next : 0;
      if (live && channel && active?.value === owner && (ticket?.value ?? 0) > 0) channel.travel.value = width.value;
    }} style={{height: 28, justifyContent: 'center'}}>
    <View style={{height: 4, borderRadius: 2, backgroundColor: 'rgba(237,245,250,.18)'}}>
      <Animated.View style={[{position: 'absolute', top: 0, bottom: 0, left: 0, right: 0, borderRadius: 2, backgroundColor: theme.colors.accentStrong}, fill]}/>
    </View>
  </View></GestureDetector>;
}
