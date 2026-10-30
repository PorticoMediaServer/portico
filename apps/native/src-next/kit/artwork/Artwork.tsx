import React, {createContext, useContext, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Animated, Image, PixelRatio, StyleSheet, View, type LayoutChangeEvent} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {sizedUri, useArtworkPlacement, useArtworkSlot} from '../../platform/ArtworkPipeline';
import {artworkArrivalScreen, type createArtworkArrivals} from '../../platform/artwork-arrival';
import {useTheme} from '../theme';
import {useReducedMotion} from '../useMotion';
import {artworkDescriptor} from '../../../../../packages/client-core/src/artwork/index';
import {artworkArrivalDuration, artworkAttemptSource, artworkFrame} from './model';
import type {ArtworkProps, ArtworkSource} from './types';
import {Picture} from './Picture';

type ScheduledProps = ArtworkProps & {source: ArtworkSource; boxWidth: number; boxHeight: number};
/** A surface's authored picture fade (Home cards: 240 ms). An explicit prop wins;
 * reduced motion and cached art keep their own timing. */
export const ArtworkArrivalContext = createContext<number | undefined>(undefined);

function ScheduledImage({source, boxWidth, boxHeight, alt, fit = 'cover', resizeMode, position, zoom = 1, saturation = 1, brightness = 1, blurRadius, filter, priority = false, arrivalDurationMs, sized = false, imageStyle, imageOverlay, onReady, onLoad, onError, onStatusChange}: ScheduledProps) {
  const placement = useArtworkPlacement();
  // The request URL is chosen before anything is keyed on it: two boxes that need different
  // slots of one picture are two images, and one slot is one image wherever it appears.
  const scale = PixelRatio.get();
  const uri = sized ? source.uri : sizedUri(source.uri, boxWidth, boxHeight, scale, (resizeMode ?? fit) === 'contain' ? 'contain' : 'cover', zoom);
  const slot = useArtworkSlot(uri === source.uri ? source.cacheKey : JSON.stringify([source.cacheKey, uri]), placement, priority);
  const reduced = useReducedMotion();
  const theme = useTheme();
  const surfaceArrival = useContext(ArtworkArrivalContext), arrivalMs = arrivalDurationMs ?? surfaceArrival;
  const arrivalMotion = useRef({arrivalDurationMs: arrivalMs, standard: theme.motion.standard, reduced});
  arrivalMotion.current = {arrivalDurationMs: arrivalMs, standard: theme.motion.standard, reduced};
  const [decoded, setDecoded] = useState(false);
  const [admitted, setAdmitted] = useState(slot.cached);
  const cachedAtMount = useRef(slot.cached).current;
  const arrivalQueue = useRef(artworkArrivalScreen()).current;
  const arrival = useRef<ReturnType<ReturnType<typeof createArtworkArrivals>['register']> | undefined>(undefined);
  const [intrinsic, setIntrinsic] = useState<{width: number; height: number} | null>(null);
  const displayedAttempt = useRef(slot.attempt);
  if (!decoded) displayedAttempt.current = slot.attempt;
  const requestAttempt = displayedAttempt.current;
  const opacity = useRef(new Animated.Value(slot.cached ? 1 : 0)).current;
  const callbacks = useRef({source, onReady, onLoad, onError, onStatusChange});
  const live = useRef(true);
  const reportedFailure = useRef(false);
  callbacks.current = {source, onReady, onLoad, onError, onStatusChange};
  const status = slot.gaveUp ? 'error' : decoded && admitted && !slot.recovering ? 'ready' : slot.active ? 'loading' : 'queued';
  useEffect(() => {
    callbacks.current.onStatusChange?.(status);
    if (status === 'error' && !reportedFailure.current) {reportedFailure.current = true; callbacks.current.onError?.(callbacks.current.source);}
  }, [status]);
  useEffect(() => {
    if (!slot.active || slot.recovering && status !== 'ready') {
      setDecoded(false);
      opacity.stopAnimation();
      opacity.setValue(0);
    }
  }, [slot.active, slot.recovering, status, opacity]);
  useEffect(() => () => opacity.stopAnimation(), [opacity]);
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);

  const attemptIdentity = JSON.stringify([uri, requestAttempt]);
  const currentAttempt = useRef(attemptIdentity);
  currentAttempt.current = attemptIdentity;
  const order = placement ? placement.row * 1000 + (placement.column ?? 0) : undefined;
  const ordered = order !== undefined, arrivalOrder = useRef(order);
  arrivalOrder.current = order;
  useLayoutEffect(() => {
    // Standalone art and cached pictures are immediate. Siblings with a declared
    // visual placement share decode admission, independently of network slots.
    if (cachedAtMount || !ordered || slot.gaveUp) return;
    const ticket = arrivalQueue.register(arrivalOrder.current!);
    arrival.current = ticket;
    return () => {ticket.retire(); if (arrival.current === ticket) arrival.current = undefined;};
  }, [attemptIdentity, cachedAtMount, ordered, slot.gaveUp, arrivalQueue]);
  useEffect(() => {if (order !== undefined) arrival.current?.move(order);}, [order]);
  const mode = resizeMode ?? fit;
  const crop = mode === 'cover' || mode === 'contain';
  // The page described the chosen slot's actual size, so its painted frame is known before it
  // loads; the decoded size is learned on load for an undescribed picture.
  const chosen = artworkDescriptor(source.uri)?.[uri.includes('size=large') ? 'large' : 'small'];
  const natural = intrinsic ?? (chosen && !sized ? {width: chosen.width, height: chosen.height} : null);
  const frame = natural && crop ? artworkFrame(boxWidth, boxHeight, natural.width, natural.height, mode, position, zoom) : {width: boxWidth, height: boxHeight};
  const loaded = (event: Parameters<NonNullable<React.ComponentProps<typeof Image>['onLoad']>>[0]) => {
    if (!live.current || currentAttempt.current !== attemptIdentity) return;
    const natural = event.nativeEvent.source;
    if (natural?.width > 0 && natural.height > 0) setIntrinsic({width: natural.width, height: natural.height});
    slot.loaded();
    reportedFailure.current = false;
    setDecoded(true);
    const reveal = () => {
      if (!live.current || currentAttempt.current !== attemptIdentity) return;
      setAdmitted(true);
      callbacks.current.onReady?.(source);
      callbacks.current.onLoad?.(source);
      opacity.stopAnimation();
      const policy = arrivalMotion.current;
      Animated.timing(opacity, {toValue: 1, duration: artworkArrivalDuration(policy.arrivalDurationMs, policy.standard, policy.reduced, cachedAtMount), useNativeDriver: true}).start();
    };
    if (arrival.current) arrival.current.ready(reveal); else reveal();
  };
  return slot.active ? <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {opacity, filter: filter ?? (saturation !== 1 || brightness !== 1 ? [{saturate: saturation}, {brightness}] : undefined)}]}>
    <Picture key={requestAttempt} source={artworkAttemptSource(source, uri, requestAttempt, sized ? undefined : {width: frame.width, height: frame.height, scale})}
      accessible={Boolean(alt)} accessibilityLabel={alt} fadeDuration={0} blurRadius={blurRadius}
      // Decode at display size. iOS and tvOS decode to the declared painted
      // size × scale (the source's width, height and scale). Android decodes
      // oversized art near twice its displayed dimensions before GPU scaling;
      // its full-size scaling path visibly aliases detail. Already-sized
      // ambient64 keeps its original decoded representation.
      resizeMethod={sized ? 'scale' : 'resize'} resizeMultiplier={2}
      resizeMode={intrinsic && crop ? 'stretch' : mode}
      style={[{position: 'absolute'}, frame, !intrinsic && zoom !== 1 ? {transform: [{scale: zoom}]} : undefined, imageStyle]}
      onLoad={loaded} onError={() => {
        if (!live.current || currentAttempt.current !== attemptIdentity) return;
        setDecoded(false); opacity.stopAnimation(); opacity.setValue(0);
        setAdmitted(false); arrival.current?.retire(); arrival.current = undefined;
        slot.failed();
        if (!reportedFailure.current) {reportedFailure.current = true; callbacks.current.onError?.(source);}
      }}/>
    {imageOverlay}
  </Animated.View> : null;
}

/** The platform queue controls Image mounts, retries and offscreen cancellation. No I/O lives here. */
export function Artwork({source, width, height, style, fallback, visible = true, testID, ...props}: ArtworkProps) {
  const [box, setBox] = useState({width: width ?? 0, height: height ?? 0});
  const onLayout = (event: LayoutChangeEvent) => {
    const next = event.nativeEvent.layout;
    setBox(previous => previous.width === next.width && previous.height === next.height ? previous : {width: next.width, height: next.height});
  };
  const w = width ?? box.width, h = height ?? box.height;
  return <View testID={testID} onLayout={onLayout} style={[styles.box, width === undefined && height === undefined ? StyleSheet.absoluteFillObject : undefined, {width, height}, style]}>
    {fallback}
    {visible && source && w > 0 && h > 0 ? <ScheduledImage key={JSON.stringify([source.cacheKey, source.uri])} {...props} source={source} boxWidth={w} boxHeight={h}/> : null}
  </View>;
}

export type MediaArtKind = 'poster' | 'wide' | 'square' | 'still' | 'book' | 'periodical' | 'newspaper';
export type MediaArtProps = ArtworkProps & {kind: MediaArtKind; backdropFallback?: boolean};

/** Geometry/treatment only. Progress, selection, playback and navigation belong to the card. */
export function MediaArt({kind, backdropFallback = false, style, width, height, position, zoom, saturation, brightness, ...props}: MediaArtProps) {
  const theme = useTheme();
  const aspectRatio = kind === 'poster' || kind === 'book' ? 2 / 3 : kind === 'wide' || kind === 'still' ? 16 / 9 : kind === 'square' ? 1 : 3 / 4;
  const fallback = kind === 'still' && backdropFallback;
  return <View style={[{width, height, aspectRatio: height === undefined ? aspectRatio : undefined, overflow: 'hidden', borderRadius: theme.radii.artwork, backgroundColor: theme.colors.raised}, style]}>
    <Artwork {...props} style={StyleSheet.absoluteFillObject} position={position ?? (kind === 'newspaper' ? {x: 0.5, y: 0} : undefined)}
      zoom={zoom ?? (fallback ? theme.spec.episode.fallback.cropScale : 1)}
      saturation={saturation ?? (fallback ? theme.spec.episode.fallback.saturation : 1)}
      brightness={brightness ?? (fallback ? theme.spec.episode.fallback.brightness : 1)}
      imageOverlay={kind === 'book' ? <BookSpine/> : props.imageOverlay}/>
  </View>;
}

export function BookSpine() {
  const {colors} = useTheme();
  return <Svg pointerEvents="none" accessible={false} style={StyleSheet.absoluteFillObject} width="100%" height="100%">
    <Defs><LinearGradient id="spine"><Stop offset="0" stopColor={colors.ink} stopOpacity={0.34}/><Stop offset="0.016" stopColor={colors.text} stopOpacity={0.16}/><Stop offset="0.036" stopColor={colors.ink} stopOpacity={0.1}/><Stop offset="0.08" stopColor={colors.ink} stopOpacity={0}/></LinearGradient></Defs>
    <Rect width="100%" height="100%" fill="url(#spine)"/>
  </Svg>;
}

const styles = StyleSheet.create({box: {overflow: 'hidden'}});
