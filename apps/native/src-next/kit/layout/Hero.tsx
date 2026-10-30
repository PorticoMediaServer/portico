import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {AppState, Text, View, type StyleProp, type TextStyle, type ViewStyle} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withDelay, withTiming, type SharedValue, type WithTimingConfig} from 'react-native-reanimated';
import {heroSpec} from '@portico/design';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {useTheme} from '../theme';
import {Focusable} from '../controls/Focusable';
import {focusTestID} from '../focus/registry';
import {useMotion} from '../useMotion';
import {Lockup} from './Lockup';
import {createArtworkReadiness, createRotationClock} from './model';
import type {ArtworkRenderContext, HeroCandidate, LayoutVariant, ThumbnailRenderContext} from './types';

export type HeroProps<T extends HeroCandidate> = {
  candidates: readonly T[];
  activeIndex?: number;
  onIndexChange?: (index: number) => void;
  active?: boolean;
  visible?: boolean;
  reducedMotion?: boolean;
  /** TV spotlight rotates only while one of its actual controls has focus. */
  heroFocused?: boolean;
  variant?: LayoutVariant;
  renderArtwork: (entry: T, context: ArtworkRenderContext) => ReactNode;
  renderActions: (entry: T) => ReactNode;
  renderMetadata?: (entry: T) => ReactNode;
  renderReason?: (entry: T) => ReactNode;
  renderAfterCopy?: (entry: T) => ReactNode;
  renderThumbnail?: (entry: T, context: ThumbnailRenderContext) => ReactNode;
  onArtworkError?: (error: unknown, entry: T) => void;
  /** The currently staged artwork decoded; presentation consumers may cover prior art now. */
  onArtworkReady?: (entry: T) => void;
  showOverview?: boolean;
  height?: number;
  copyTop?: number;
  /** A drawn sideways composition: the copy sits at this inset and width, left aligned, over a hero whose artwork owns the other side. Every number comes from the frame. */
  copySide?: Readonly<{inset: number; width: number; bottom: number}>;
  /** Optional composition metrics for native tablets; defaults retain each existing recipe. */
  copyStyle?: StyleProp<ViewStyle>;
  reasonTextStyle?: StyleProp<TextStyle>;
  metadataStyle?: StyleProp<ViewStyle>;
  overviewTextStyle?: StyleProp<TextStyle>;
  dotsStyle?: StyleProp<ViewStyle>;
  thumbnailSize?: Readonly<{width: number; height: number; labelSize?: number; bottom?: number; gap?: number; right?: number}>;
  /** The drawn lockup column for one composition, as one coherent size/lineHeight/tracking triple. */
  lockupStyle?: StyleProp<TextStyle>;
  longLockupStyle?: StyleProp<TextStyle>;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};

export function HeroMetadata({facts, centered = false}: {facts: readonly FactPart[]; centered?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: centered ? 'center' : 'flex-start', columnGap: heroSpec.facts.gap[theme.platform], rowGap: heroSpec.facts.rowGap[theme.platform]}}>
    {facts.map(fact => <Text key={fact.id} style={[theme.type.meta, {includeFontPadding: false, color: fact.id === 'year' || fact.role === 'identity' ? theme.colors.text : theme.colors.textSecondary, fontFamily: fact.id === 'year' || fact.role === 'identity' ? theme.type.bodyStrong.fontFamily : theme.type.meta.fontFamily}]}>{fact.text}</Text>)}
  </View>;
}

type LayerProps<T extends HeroCandidate> = {
  entry: T; shown: boolean; pending: boolean; fade: 'hero' | 'slow'; reducedMotion?: boolean; visible?: boolean;
  ready: () => void; failed: (error?: unknown) => void; departed: () => void;
  renderArtwork: HeroProps<T>['renderArtwork'];
};

function HeroLayer<T extends HeroCandidate>({entry, shown, pending, fade, ready, failed, departed, visible = true, renderArtwork, reducedMotion}: LayerProps<T>) {
  const motion = useMotion(), opacity = useSharedValue(0), drift = useSharedValue<number>(heroSpec.rotation.driftScaleFrom);
  const reduced = motion.reduce || !!reducedMotion;
  useEffect(() => {
    opacity.value = withTiming(shown ? 1 : 0, {...motion.timing(fade), ...(reduced ? {duration: heroSpec.rotation.reducedMs} : {})}, finished => {
      if (finished && !shown && !pending) runOnJS(departed)();
    });
    return () => cancelAnimation(opacity);
  }, [departed, fade, motion, opacity, pending, reduced, shown]);
  useEffect(() => {
    drift.value = reduced || motion.effects?.drift === false || fade !== 'hero' ? 1 : shown ? withTiming(1, {...motion.timing('hero'), duration: heroSpec.rotation.driftMs}) : heroSpec.rotation.driftScaleFrom;
    return () => cancelAnimation(drift);
  }, [drift, fade, motion, reduced, shown]);
  const layerStyle = useAnimatedStyle(() => ({opacity: opacity.value}));
  const foregroundStyle = useAnimatedStyle<ViewStyle>(() => ({transform: [{scale: drift.value}]}));
  return <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[{position: 'absolute', inset: 0}, layerStyle]}>
    {renderArtwork(entry, {onReady: ready, onError: failed, visible, active: shown, foregroundStyle})}
  </Animated.View>;
}

type CopyProps<T extends HeroCandidate> = Omit<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange'> & {entry: T; fade: 'hero' | 'slow'; wordsOnly?: boolean};

function CopyPart({identity, step, stagger, translate, timing, children, still = false}: {still?: boolean; identity: string; step: number; stagger: number; translate: number; timing: WithTimingConfig; children: ReactNode}) {
  const words = useSharedValue(still ? 1 : 0);
  useLayoutEffect(() => {
    if (still) {words.value = 1; return;}
    words.value = 0;
    words.value = withDelay(step * stagger, withTiming(1, timing));
    return () => cancelAnimation(words);
  }, [identity, stagger, step, still, timing, words]);
  const style = useAnimatedStyle(() => ({opacity: words.value, transform: [{translateY: (1 - words.value) * translate}]}));
  return <Animated.View pointerEvents="box-none" style={style}>{children}</Animated.View>;
}

/** One actual copy/control tree keeps native TV focus while the artwork layers crossfade. */
function HeroCopy<T extends HeroCandidate>({entry, fade, variant, renderActions, renderMetadata, renderReason, renderAfterCopy, showOverview = true, copyTop, copySide, copyStyle, reasonTextStyle, metadataStyle, overviewTextStyle, lockupStyle, longLockupStyle, reducedMotion, wordsOnly = false}: CopyProps<T>) {
  const theme = useTheme(), motion = useMotion();
  const reduced = motion.reduce || !!reducedMotion;
  const phone = variant === 'phone-app' || variant === 'phone-web' || (!variant && theme.layoutMode === 'phone'), tv = variant === 'tv' || (!variant && theme.isTV);
  const timing = useMemo(() => ({...motion.timing('standard'), duration: reduced ? heroSpec.rotation.reducedMs : fade === 'hero' ? heroSpec.rotation.copyMs : theme.motion.standard}), [fade, motion, reduced, theme.motion.standard]);
  // kit.css draws .actions at 12 px (18 px under .tv); the phone/tablet spec column said 10.
  const actionsGap = theme.isTV ? 18 : 12;
  const side = !!copySide;
  const partProps = {still: wordsOnly, identity: entry.id, stagger: reduced || fade !== 'hero' ? 0 : heroSpec.rotation.copyStaggerMs, translate: reduced || tv || phone ? 0 : heroSpec.rotation.copyTranslate, timing};
  let step = 0;
  return <View style={[copySide ? {position: 'absolute', left: copySide.inset, right: undefined, width: copySide.width, top: undefined, bottom: copySide.bottom}
    : {position: 'absolute', left: tv ? theme.layout.gutter.tv : phone ? theme.layout.gutter.phone : theme.gutter,
      right: phone ? theme.layout.gutter.phone : undefined, width: phone ? undefined : tv ? heroSpec.copy.width.tv : heroSpec.copy.width.web,
      top: copyTop ?? (tv ? 220 : undefined), bottom: copyTop !== undefined || tv ? undefined : phone ? heroSpec.copy.homeBottom.phone : heroSpec.copy.homeBottom.web}, copyStyle]}>
    {(renderReason || entry.reason) ? <CopyPart {...partProps} step={step++}>{renderReason ? renderReason(entry) : <Text style={[theme.type.eyebrow, {includeFontPadding: false, textAlign: side ? 'left' : phone ? 'center' : 'left', color: theme.colors.accentStrong, marginBottom: phone ? 8 : tv ? 16 : 14}, reasonTextStyle]}>{entry.reason}</Text>}</CopyPart> : null}
    <CopyPart {...partProps} step={step++}><Lockup title={entry.title} long={entry.long} variant={variant} lines={phone || tv ? 2 : undefined} style={[{textAlign: side ? 'left' : phone ? 'center' : 'left', marginBottom: phone ? 10 : tv ? 22 : 20}, lockupStyle, entry.long && longLockupStyle]} /></CopyPart>
    {(renderMetadata || entry.facts?.length) ? <CopyPart {...partProps} step={step++}><View style={[{marginBottom: phone ? 18 : tv ? 18 : 16}, metadataStyle]}>{renderMetadata ? renderMetadata(entry) : <HeroMetadata facts={entry.facts ?? []} centered={side ? false : phone} />}</View></CopyPart> : null}
    {showOverview && !phone && entry.overview ? <CopyPart {...partProps} step={step++}><Text numberOfLines={tv ? 2 : 3} style={[theme.type.synopsis, {includeFontPadding: false, color: theme.colors.textSecondary, maxWidth: tv ? 800 : 540, marginBottom: tv ? 30 : 28}, overviewTextStyle]}>{entry.overview}</Text></CopyPart> : null}
    {/* tv-focus F-HOME-11: on TV the controls hold native focus through a rotation, so they
        never re-fade; only their labels change in place. Every other layout keeps the stagger. */}
    {!wordsOnly && <CopyPart {...partProps} still={tv} step={step++}><View style={{flexDirection: 'row', alignItems: 'center', justifyContent: side ? 'flex-start' : phone ? 'center' : 'flex-start', gap: actionsGap}}>{renderActions(entry)}</View></CopyPart>}
    {!wordsOnly && renderAfterCopy && <CopyPart {...partProps} still={tv} step={step}>{renderAfterCopy(entry)}</CopyPart>}
  </View>;
}

/** Only words are retained: no action renderer or trailing interactive tree is called. */
function OutgoingHeroWords<T extends HeroCandidate>({retire, ...props}: CopyProps<T> & {retire: () => void}) {
  const motion = useMotion(), opacity = useSharedValue(1);
  const reduce = motion.reduce || !!props.reducedMotion;
  useLayoutEffect(() => {
    opacity.value = withTiming(0, {...motion.timing('standard'), duration: reduce ? heroSpec.rotation.reducedMs : heroSpec.rotation.copyMs}, finished => {if (finished) runOnJS(retire)();});
    return () => cancelAnimation(opacity);
  }, [motion, opacity, reduce, retire]);
  const style = useAnimatedStyle(() => ({opacity: opacity.value}));
  return <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[{position: 'absolute', inset: 0}, style]}>
    <HeroCopy {...props} wordsOnly/>
  </Animated.View>;
}

type StagedEntry<T> = Readonly<{entry: T; generation: number}>;

type StagedLayerProps<T extends HeroCandidate> = Omit<LayerProps<T>, 'entry' | 'ready' | 'failed' | 'departed'> & {
  layer: StagedEntry<T>; ready: (generation: number) => void; failed: (generation: number, error?: unknown) => void; departed: (generation: number) => void;
};
function StagedHeroLayer<T extends HeroCandidate>({layer, ready, failed, departed, ...props}: StagedLayerProps<T>) {
  const generation = layer.generation;
  const decoded = useCallback(() => ready(generation), [generation, ready]);
  const decodeFailed = useCallback((error?: unknown) => failed(generation, error), [failed, generation]);
  const faded = useCallback(() => departed(generation), [departed, generation]);
  return <HeroLayer {...props} entry={layer.entry} ready={decoded} failed={decodeFailed} departed={faded} />;
}

/** The shown foreground and one actual incoming foreground are the only mounted layers. */
export function HeroSurface<T extends HeroCandidate>({entry, fade = 'hero', ...props}: Omit<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange'> & {entry: T | undefined; fade?: 'hero' | 'slow'}) {
  const [layers, setLayers] = useState<readonly StagedEntry<T>[]>([]), [shown, setShown] = useState<number>();
  const layersRef = useRef(layers), shownRef = useRef(shown), target = useRef<number | undefined>(undefined), generation = useRef(0);
  const onErrorRef = useRef(props.onArtworkError), onReadyRef = useRef(props.onArtworkReady);
  onReadyRef.current = props.onArtworkReady;
  onErrorRef.current = props.onArtworkError;
  shownRef.current = shown; layersRef.current = layers;
  const [gate] = useState(() => createArtworkReadiness<number>(setShown, (failedGeneration, error) => {
    const failedLayer = layersRef.current.find(layer => layer.generation === failedGeneration);
    if (failedGeneration !== shownRef.current) setLayers(current => current.filter(layer => layer.generation !== failedGeneration));
    if (failedLayer) onErrorRef.current?.(error, failedLayer.entry);
  }));
  useEffect(() => {
    if (!entry) {target.current = undefined; setLayers([]); setShown(undefined); gate.clear(); return;}
    const currentTarget = layersRef.current.find(layer => layer.generation === target.current);
    if (currentTarget?.entry.id === entry.id) {
      if (currentTarget.entry !== entry) setLayers(current => current.map(layer => layer.generation === currentTarget.generation ? {...layer, entry} : layer));
      return;
    }
    const currentShown = layersRef.current.find(layer => layer.generation === shownRef.current);
    if (currentShown?.entry.id === entry.id) {
      target.current = currentShown.generation; gate.stage(currentShown.generation);
      setLayers([{...currentShown, entry}]); return;
    }
    // A fading-out former target is remounted as a fresh incoming foreground.
    // Its old decode callback cannot reveal this new generation.
    const incoming = {entry, generation: ++generation.current};
    target.current = incoming.generation; gate.stage(incoming.generation);
    setLayers(currentShown ? [currentShown, incoming] : [incoming]);
  }, [entry, gate]);
  useEffect(() => () => {target.current = undefined; gate.clear();}, [gate]);
  const ready = useCallback((layerGeneration: number) => {
    if (target.current !== layerGeneration || !gate.ready(layerGeneration)) return;
    const layer = layersRef.current.find(candidate => candidate.generation === layerGeneration);
    if (layer) onReadyRef.current?.(layer.entry);
  }, [gate]);
  const failed = useCallback((layerGeneration: number, error: unknown) => {if (target.current === layerGeneration) gate.error(layerGeneration, error);}, [gate]);
  const departed = useCallback((layerGeneration: number) => {
    if (target.current === layerGeneration || shownRef.current === layerGeneration) return;
    setLayers(current => current.filter(layer => layer.generation !== layerGeneration));
  }, []);
  const shownLayer = layers.find(layer => layer.generation === shown);
  const theme = useTheme();
  const tv = props.variant === 'tv' || (!props.variant && theme.isTV);
  const lastCopy = useRef<StagedEntry<T> | undefined>(undefined);
  const [outgoing, setOutgoing] = useState<StagedEntry<T>>();
  useLayoutEffect(() => {
    const previous = lastCopy.current;
    lastCopy.current = shownLayer;
    if (!shownLayer || !tv) {setOutgoing(undefined); return;}
    if (previous && previous.generation !== shownLayer.generation) setOutgoing(previous);
  }, [shownLayer, tv]);
  const retireWords = useCallback(() => {setOutgoing(current => current?.generation === outgoing?.generation ? undefined : current);}, [outgoing?.generation]);
  return <>{layers.map(layer => <StagedHeroLayer key={`${layer.entry.id}:${layer.generation}`} layer={layer} renderArtwork={props.renderArtwork} visible={props.visible} reducedMotion={props.reducedMotion}
    shown={shown === layer.generation} pending={target.current === layer.generation && shown !== layer.generation} ready={ready} failed={failed} departed={departed} fade={fade} />)}
    {outgoing && <OutgoingHeroWords key={outgoing.generation} {...props} entry={outgoing.entry} fade={fade} retire={retireWords}/>}
    {shownLayer && <HeroCopy {...props} entry={shownLayer.entry} fade={fade} />}
  </>;
}

function HeroThumbnail<T extends HeroCandidate>({entry, index, active, visible, progress, render, onPress, testID, size}: {testID?: string; size?: HeroProps<T>['thumbnailSize'];
  entry: T; index: number; active: boolean; visible: boolean; progress: SharedValue<number>;
  render: NonNullable<HeroProps<T>['renderThumbnail']>; onPress: () => void;
}) {
  const theme = useTheme(), [hovered, setHovered] = useState(false), [focused, setFocused] = useState(false);
  const width = size?.width ?? heroSpec.thumbnail.width, height = size?.height ?? heroSpec.thumbnail.height;
  const fill = useAnimatedStyle(() => ({transform: [{translateX: (progress.value - 1) * width / 2}, {scaleX: progress.value}]}));
  return <Focusable testID={testID} focusTreatment="none" accessibilityRole="button" accessibilityLabel={entry.title} accessibilityState={{selected: active}} onPress={onPress}
    onHoverIn={() => setHovered(true)} onHoverOut={() => setHovered(false)} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
    style={{width, opacity: active ? 1 : hovered ? heroSpec.thumbnail.hoverOpacity : heroSpec.thumbnail.opacity}}>
    <View style={{width, height, borderRadius: heroSpec.thumbnail.radius, overflow: 'hidden'}}>{render(entry, {index, active, visible})}</View>
    {focused && <View pointerEvents="none" style={{position: 'absolute', left: -6, right: -6, top: -6, height: height + 12, borderRadius: heroSpec.thumbnail.radius + 6, borderWidth: theme.spec.focus.ringWidth.phone, borderColor: theme.colors.focus}} />}
    <View style={{marginTop: heroSpec.thumbnail.progressGap, height: heroSpec.thumbnail.progressHeight, borderRadius: 3, overflow: 'hidden', backgroundColor: `${theme.colors.text}2E`}}>
      {active && <Animated.View style={[{height: heroSpec.thumbnail.progressHeight, backgroundColor: theme.colors.text}, fill]} />}
    </View>
    <Text numberOfLines={1} style={[theme.type.thumbnailLabel, size?.labelSize === undefined ? undefined : {fontSize: size.labelSize * theme.textScale}, {marginTop: heroSpec.thumbnail.labelGap, color: theme.colors.textSecondary}]}>{entry.title}</Text>
  </Focusable>;
}

export function Hero<T extends HeroCandidate>({candidates, activeIndex, onIndexChange, active = true, visible = true, reducedMotion = false, heroFocused = false, variant, height, style, testID, ...props}: HeroProps<T>) {
  const theme = useTheme(), motion = useMotion(), [localIndex, setLocalIndex] = useState(0), [appVisible, setAppVisible] = useState(AppState.currentState === 'active'), [hovered, setHovered] = useState(false), [controlFocused, setControlFocused] = useState(false);
  const index = Math.max(0, Math.min(candidates.length - 1, activeIndex ?? localIndex));
  const tv = variant === 'tv' || (!variant && theme.isTV), phone = variant === 'phone-app' || variant === 'phone-web' || (!variant && theme.layoutMode === 'phone');
  const desktop = variant === 'desktop' || (!variant && theme.layoutMode === 'desktop');
  const renderThumbnail = props.renderThumbnail;
  const candidateId = candidates[index]?.id;
  // A replacement at the same index owns a fresh dwell clock.
  const rotation = useMemo(() => ({index, id: candidateId, clock: createRotationClock(heroSpec.rotation.intervalMs)}), [index, candidateId]), progress = useSharedValue(0);
  const rotationRun = useRef<object | undefined>(undefined);
  const change = useCallback((next: number) => {rotation.clock.reset(); progress.value = 0; if (activeIndex === undefined) setLocalIndex(next); onIndexChange?.(next);}, [activeIndex, rotation, onIndexChange, progress]);
  useEffect(() => {const subscription = AppState.addEventListener('change', next => setAppVisible(next === 'active')); return () => subscription.remove();}, []);
  const [touching, setTouching] = useState(false);
  const canRotate = candidates.length > 1 && active && visible && appVisible && !motion.reduce && motion.effects?.drift !== false && !reducedMotion && !hovered && !touching && (tv ? heroFocused : !controlFocused);
  useEffect(() => {progress.value = 0;}, [rotation, progress]);
  useEffect(() => {
    if (!canRotate) return;
    const owner = rotationRun, run = {rotation, animation: progress};
    owner.current = run;
    const remaining = run.rotation.clock.resume(Date.now());
    run.animation.value = withTiming(1, {...motion.timing('hero', 'linear'), duration: remaining});
    const timer = setTimeout(() => {if (owner.current === run) change((run.rotation.index + 1) % candidates.length);}, remaining);
    return () => {
      clearTimeout(timer);
      if (owner.current !== run) return;
      owner.current = undefined;
      run.rotation.clock.pause(Date.now());
      cancelAnimation(run.animation);
    };
  }, [canRotate, candidates.length, change, rotation, motion, progress]);
  const dots = candidates.length > 1 ? <View style={[{marginTop: tv ? 34 : heroSpec.phoneDots.marginTop, flexDirection: 'row', justifyContent: props.copySide || !phone ? 'flex-start' : 'center', gap: tv ? 10 : heroSpec.phoneDots.gap}, props.dotsStyle]}>
    {candidates.map((candidate, i) => tv ? <View key={candidate.id} style={{width: i === index ? 34 : 10, height: 10, borderRadius: 5, backgroundColor: i === index ? theme.colors.text : `${theme.colors.text}47`}} /> : <Focusable testID={testID ? focusTestID(testID, JSON.stringify(['candidate', candidate.id])) : undefined} focusTreatment="none" key={candidate.id} accessibilityRole="button" accessibilityLabel={candidate.title} accessibilityState={{selected: i === index}} onPress={() => change(i)} hitSlop={8}>
      <View style={{width: i === index ? heroSpec.phoneDots.activeWidth : heroSpec.phoneDots.size, height: heroSpec.phoneDots.size, borderRadius: 3, backgroundColor: i === index ? theme.colors.text : `${theme.colors.text}4D`}} />
    </Focusable>)}
  </View> : null;
  return <View testID={testID} onPointerEnter={() => setHovered(true)} onPointerLeave={() => setHovered(false)}
    {...(tv ? {} : {onTouchStart: () => setTouching(true),
      onTouchEnd: (event: {nativeEvent: {touches: readonly unknown[]}}) => {if (event.nativeEvent.touches.length === 0) setTouching(false);},
      onTouchCancel: () => setTouching(false)})}
    onFocus={() => setControlFocused(true)} onBlur={() => setControlFocused(false)}
    style={[{height: height ?? (variant === 'phone-web' ? heroSpec.mobileWeb.homeHeight : variant === 'desktop' ? heroSpec.homeHeight.web : heroSpec.homeHeight[theme.platform]), position: 'relative'}, style]}>
    <HeroSurface {...props} entry={candidates[index]} variant={variant} active={active} visible={visible && appVisible} reducedMotion={reducedMotion} heroFocused={heroFocused}
      renderAfterCopy={entry => <>{props.renderAfterCopy?.(entry)}{!desktop && dots}</>} />
    {desktop && renderThumbnail && <View style={{position: 'absolute', right: props.thumbnailSize?.right ?? theme.gutter, bottom: props.thumbnailSize?.bottom ?? heroSpec.thumbnail.bottom, flexDirection: 'row', gap: props.thumbnailSize?.gap ?? heroSpec.thumbnail.gap}}>
      {candidates.map((candidate, i) => <HeroThumbnail testID={testID ? focusTestID(testID, JSON.stringify(['thumbnail', candidate.id])) : undefined} key={candidate.id} entry={candidate} index={i} active={index === i} visible={visible && appVisible} progress={progress} render={renderThumbnail} size={props.thumbnailSize} onPress={() => change(i)} />)}
    </View>}
  </View>;
}
