import React, {useEffect, useLayoutEffect, useRef} from 'react';
import {Animated, StyleSheet, View} from 'react-native';
import Reanimated from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, RadialGradient, Rect, Stop} from 'react-native-svg';
import {useTheme} from '../theme';
import {useReducedMotion} from '../useMotion';
import {useNativeObservation} from '../dev-observation';
import {ArtworkAlphaMask} from '../../platform/ArtworkAlphaMask';
import {Artwork} from './Artwork';
import {artworkArrivalDuration} from './model';
import type {AmbientArtProps, ArtworkPalette, ColourBackdropProps} from './types';

type Glow = Readonly<{x: number; y: number; rx: number; ry: number; colour: string; stops: readonly (readonly [number, number])[]}>;

function GlowLayer({glows, background}: {glows: readonly Glow[]; background?: string}) {
  return <Svg pointerEvents="none" accessible={false} width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none" style={StyleSheet.absoluteFillObject}>
    <Defs>{glows.map((g, i) => <RadialGradient key={i} id={`glow-${i}`} gradientUnits="userSpaceOnUse" cx={g.x * 1000} cy={g.y * 1000} rx={g.rx * 1000} ry={g.ry * 1000}>
      {g.stops.map(([offset, alpha]) => <Stop key={offset} offset={offset} stopColor={g.colour} stopOpacity={alpha}/>)}</RadialGradient>)}</Defs>
    {background ? <Rect width="1000" height="1000" fill={background}/> : null}
    {[...glows].map((_, i) => glows.length - i - 1).map(i => <Rect key={i} width="1000" height="1000" fill={`url(#glow-${i})`}/>)}
  </Svg>;
}

/** DESIGN.md "Boot and loading", the standard backdrop (binding): ink under the blue and green
 * ambient glows. Phone width draws the glows as a 560 pt band at the top; tablet, desktop and
 * TV draw them over the whole window. Every screen without artwork stands on exactly this, and
 * artwork screens layer their art over it. Every effects tier, lite included, draws all three
 * glows (Justin, 29 Oct): one static radial costs nothing worth saving. */
export const StandardBackdrop = React.memo(function StandardBackdrop({style, testID = 'standard-backdrop'}: Pick<ColourBackdropProps, 'style' | 'testID'>) {
  const {colors, width, layout} = useTheme();
  const band = width < layout.breakpoint.phone;
  const backdropStyle = [StyleSheet.absoluteFillObject, {backgroundColor: colors.ink}, style];
  const observation = useNativeObservation<View>({kind: 'view', semanticId: 'colour-backdrop',
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(backdropStyle) : undefined, declaredStyleComplete: true});
  const blue = colors.accentDeep, green = colors.generatedGreen;
  const glows: readonly Glow[] = band ? [
    {x: 0.96, y: -0.06, rx: 0.9, ry: 0.54, colour: blue, stops: [[0, 0.5], [0.48, 0.15], [0.76, 0]]},
    {x: -0.08, y: 0.3, rx: 0.84, ry: 0.44, colour: green, stops: [[0, 0.5], [0.52, 0.13], [0.78, 0]]},
    {x: 0.3, y: 0, rx: 0.7, ry: 0.4, colour: colors.ambient, stops: [[0, 0.8], [0.72, 0]]},
  ] : [
    {x: 0.86, y: 0.04, rx: 0.44, ry: 0.66, colour: blue, stops: [[0, 0.46], [0.46, 0.15], [0.74, 0]]},
    {x: 0.64, y: 1.04, rx: 0.42, ry: 0.58, colour: green, stops: [[0, 0.6], [0.5, 0.18], [0.76, 0]]},
    {x: 0.04, y: 0, rx: 0.38, ry: 0.5, colour: colors.ambient, stops: [[0, 0.75], [0.72, 0]]},
  ];
  return <View ref={observation.ref} pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants" testID={testID} style={backdropStyle}>
    <View style={band ? {position: 'absolute', top: 0, left: 0, right: 0, height: 560} : StyleSheet.absoluteFillObject}><GlowLayer glows={glows}/></View>
  </View>;
});

/** The standard backdrop under its older name. Shape, fill and size no longer vary per screen
 * (Justin, 29 Oct): every caller gets the one standard picture for its window. */
export function ColourBackdrop({style, testID}: ColourBackdropProps) {
  return <StandardBackdrop style={style} testID={testID}/>;
}

export function PaletteWash({palette, generated = false}: {palette: ArtworkPalette; generated?: boolean}) {
  const {colors} = useTheme();
  return <GlowLayer background={generated ? colors.raisedSoft : undefined} glows={[
    {x: generated ? 0.85 : 0.22, y: generated ? 0.1 : 0.12, rx: 0.7, ry: 1.2, colour: palette.first, stops: [[0, generated ? 1 : 0.62], [0.6, 0]]},
    {x: 0.6, y: 1.1, rx: 0.6, ry: 0.9, colour: palette.second, stops: [[0, generated ? 1 : 0.52], [0.65, 0]]},
  ]}/>;
}

function ArtScrims({phone, side, bottomAt, top = false, phoneOverlay = false, tvSide = false}: {phone: boolean; side: boolean; bottomAt: number; top?: boolean; phoneOverlay?: boolean; tvSide?: boolean}) {
  const {colors, spec} = useTheme();
  return <Svg accessible={false} pointerEvents="none" width="100%" height="100%" style={StyleSheet.absoluteFillObject}>
    <Defs>
      <LinearGradient id="art-bottom" x1="0" y1="100%" x2="0" y2="0"><Stop offset="0" stopColor={colors.ink}/><Stop offset={bottomAt} stopColor={colors.ink} stopOpacity={0}/></LinearGradient>
      <LinearGradient id="art-side"><Stop offset="0" stopColor={colors.ink}/><Stop offset={phone ? 0.48 : 0.38} stopColor={colors.ink} stopOpacity={0}/></LinearGradient>
      <LinearGradient id="art-top" x1="0" y1="0" x2="0" y2="100%"><Stop offset="0" stopColor={colors.ink} stopOpacity={0.4}/><Stop offset="0.35" stopColor={colors.ink} stopOpacity={0}/></LinearGradient>
      <LinearGradient id="phone-overlay" x1="0" y1="0" x2="0" y2="100%">{spec.hero.scrim.phoneOverlay.map(stop => <Stop key={stop.at} offset={stop.at} stopColor={colors.ink} stopOpacity={stop.alpha}/>)}</LinearGradient>
      <LinearGradient id="art-tv-side" x1="0%" y1="0%" x2="100%" y2="0%">{spec.hero.scrim.tvLeft.map(stop => <Stop key={stop.at} offset={stop.at} stopColor={colors.ink} stopOpacity={stop.alpha}/>)}</LinearGradient>
    </Defs>
    <Rect width="100%" height="100%" fill="url(#art-bottom)"/>
    {side ? <Rect width="100%" height="100%" fill="url(#art-side)"/> : null}
    {top ? <Rect width="100%" height="100%" fill="url(#art-top)"/> : null}
    {phoneOverlay ? <Rect width="100%" height="100%" fill="url(#phone-overlay)"/> : null}
    {tvSide ? <Rect width="100%" height="100%" fill="url(#art-tv-side)"/> : null}
  </Svg>;
}

/** tv.html .np-shade, over Q43's served ambient image or the real palette. */
function MusicScrims() {
  const {colors} = useTheme();
  return <Svg accessible={false} pointerEvents="none" width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none" style={StyleSheet.absoluteFillObject}>
    <Defs>
      <RadialGradient id="music-radial" gradientUnits="userSpaceOnUse" cx="260" cy="420" rx="600" ry="700">
        <Stop offset="0" stopColor={colors.ink} stopOpacity={0}/><Stop offset="0.62" stopColor={colors.ink} stopOpacity={.55}/><Stop offset="1" stopColor={colors.ink} stopOpacity={.9}/>
      </RadialGradient>
      <LinearGradient id="music-bottom" x1="0" y1="100%" x2="0" y2="0">
        <Stop offset="0" stopColor={colors.ink} stopOpacity={.94}/><Stop offset="0.4" stopColor={colors.ink} stopOpacity={.4}/><Stop offset="0.62" stopColor={colors.ink} stopOpacity={0}/>
      </LinearGradient>
      <LinearGradient id="music-right" x1="0" y1="0" x2="100%" y2="0"><Stop offset="0.5" stopColor={colors.ink} stopOpacity={0}/><Stop offset="1" stopColor={colors.ink} stopOpacity={.6}/></LinearGradient>
    </Defs>
    <Rect width="1000" height="1000" fill="url(#music-right)"/><Rect width="1000" height="1000" fill="url(#music-bottom)"/><Rect width="1000" height="1000" fill="url(#music-radial)"/>
  </Svg>;
}

function PersonScrim() {
  const {colors} = useTheme();
  return <Svg accessible={false} pointerEvents="none" width="100%" height="100%" style={StyleSheet.absoluteFillObject}>
    <Defs><LinearGradient id="person-shade" x1="0" y1="0" x2="0" y2="100%">
      <Stop offset="0" stopColor={colors.ink} stopOpacity={.05}/><Stop offset="0.44" stopColor={colors.ink} stopOpacity={.6}/>
      <Stop offset="0.7" stopColor={colors.ink} stopOpacity={.94}/><Stop offset="0.88" stopColor={colors.ink}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#person-shade)"/>
  </Svg>;
}

/** Continuous ambient wash and independently sharp foreground, with Q43's cheap TV representation. */
export function AmbientArt({source, sources, foreground, palette, treatment = 'hero', width, height, visible = true, priority = false, arrivalDurationMs, position, style, foregroundFrameStyle, foregroundStyle, foregroundFraction, foregroundFade, onReady, onFallbackReady, onError, onStatusChange, testID}: AmbientArtProps) {
  const theme = useTheme();
  const reduce = useReducedMotion();
  const fallbackOpacity = useRef(new Animated.Value(1)).current;
  const phone = treatment === 'phone' || (theme.layoutMode === 'phone' && (treatment === 'hero' || treatment === 'title'));
  const desktop = theme.layoutMode === 'desktop' && (treatment === 'hero' || treatment === 'title');
  const foregroundWidth = desktop ? treatment === 'title' ? theme.spec.hero.backdrop.titleWidthFraction.web : theme.spec.hero.backdrop.widthFraction.web : theme.spec.hero.backdrop.widthFraction.tv;
  const wash = treatment === 'wash' || treatment === 'library';
  const cover = treatment === 'cover' || treatment === 'artist';
  const music = treatment === 'music';
  const person = treatment === 'person';
  const maskedHero = theme.isTV && treatment === 'hero';
  const list = (sources ?? (source ? [source] : [])).slice(0, 3);
  const sourceIdentity = JSON.stringify(list.map(item => item.cacheKey));
  const empty = !foreground && list.length === 0, fallbackReported = useRef(false);
  useLayoutEffect(() => {
    if (!empty) {fallbackReported.current = false; return;}
    // The palette is a real committed presentation when no image was offered.
    // Keep this separate from onReady: no source or decode fact is fabricated.
    if (visible && !fallbackReported.current && onFallbackReady) {
      fallbackReported.current = true;
      onFallbackReady();
    }
  });
  useEffect(() => {fallbackOpacity.stopAnimation(); fallbackOpacity.setValue(1);}, [sourceIdentity, fallbackOpacity]);
  useEffect(() => () => fallbackOpacity.stopAnimation(), [fallbackOpacity]);
  const alpha = person ? .55 : music ? .85 : wash ? 0.42 : cover ? 0.62 : theme.spec.hero.ambient.alpha[theme.platform];
  const defaultPalette = {first: theme.colors.accentDeep, second: theme.colors.generatedGreen};
  // ambient-v1 is already blurred by the server. Android TV never runs a live blur filter.
  const allowLiveBlur = !theme.isTV;
  const sharp = <Reanimated.View style={[StyleSheet.absoluteFillObject, foregroundStyle]}>
    <Artwork source={foreground} visible={visible} priority={priority} arrivalDurationMs={arrivalDurationMs} position={position ?? (desktop && treatment === 'title' ? {x: 0.3, y: 0.25} : {x: phone ? 0.38 : 0.5, y: 0.3})}
      onReady={onReady} onError={onError} onStatusChange={onStatusChange}/>
  </Reanimated.View>;
  return <View pointerEvents="none" accessible={false} testID={testID} style={[StyleSheet.absoluteFillObject, {overflow: 'hidden', width, height}, style]}>
    <Animated.View style={[StyleSheet.absoluteFillObject, {backgroundColor: theme.colors.ink, opacity: fallbackOpacity}]}>
      <PaletteWash palette={palette ?? defaultPalette}/>
    </Animated.View>
    {list.map((ambient, index) => <View key={ambient.cacheKey} style={[StyleSheet.absoluteFillObject, {
      left: person ? '-20%' : music ? '-25%' : wash && list.length > 1 ? `${(index * 0.68 - 0.25) * 100}%` : '-12%',
      top: person ? '-20%' : music ? '-25%' : wash ? '-30%' : cover ? '-50%' : '-12%', right: undefined, bottom: undefined,
      width: person ? '140%' : music ? '150%' : wash && list.length > 1 ? '70%' : cover ? '130%' : '124%', height: person ? '140%' : music ? '150%' : cover ? '160%' : '124%',
      opacity: index === 1 && wash ? 0.34 : alpha,
    }]}>
      <Artwork source={ambient} visible={visible} sized priority={priority} arrivalDurationMs={arrivalDurationMs} onReady={decoded => {
        if (index === 0) {
          fallbackOpacity.stopAnimation();
          Animated.timing(fallbackOpacity, {toValue: 0, duration: artworkArrivalDuration(arrivalDurationMs, theme.motion.standard, reduce, false), useNativeDriver: true}).start();
          if (!foreground) onReady?.(decoded);
        }
      }} onError={!foreground && index === 0 ? onError : undefined}
        onStatusChange={status => {
          if (index === 0 && status === 'error') {fallbackOpacity.stopAnimation(); fallbackOpacity.setValue(1);}
          if (!foreground && index === 0) onStatusChange?.(status);
        }}
        blurRadius={allowLiveBlur ? wash ? 12 : 8 : undefined}/>
    </View>)}
    {foreground ? <View style={[{position: 'absolute', top: 0, right: 0, height: '100%',
      width: foregroundFraction !== undefined ? `${foregroundFraction * 100}%` : phone ? '100%' : `${foregroundWidth * 100}%`}, foregroundFrameStyle]}>
      {maskedHero ? <ArtworkAlphaMask leftOpaqueAt={theme.spec.hero.mask.leftOpaqueAt} bottomOpaqueAt={theme.spec.hero.mask.bottomOpaqueAt}>{sharp}</ArtworkAlphaMask>
        : <>{sharp}<ArtScrims phone={phone} side={!phone || foregroundFade === 'left'} bottomAt={phone ? theme.spec.hero.mask.phoneBottomOpaqueAt : theme.spec.hero.mask.bottomOpaqueAt} phoneOverlay={phone}/></>}
    </View> : null}
    {person ? <PersonScrim/> : music ? <MusicScrims/> : <ArtScrims phone={phone} side={false} bottomAt={wash ? 1 : cover ? 0.7 : 0.42} tvSide={maskedHero}/>}
  </View>;
}
