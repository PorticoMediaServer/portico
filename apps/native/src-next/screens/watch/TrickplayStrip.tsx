import React from 'react';
import {Image, StyleSheet, Text, View} from 'react-native';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {TrickplaySet} from '../../../../../packages/client-core/src/trickplay';
import type {ArtworkSource} from '../../kit/artwork';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {nativeTrickplayWindow} from './trickplay-frame';

/** The five-picture strip frame 2 draws above the seek bar. Real trickplay
 * sheets supply each frame; a sheet the server has not published leaves the
 * tile dark rather than inventing a picture. The middle tile is the one the
 * viewer would land on. */
export type NativeTrickplayStripProps = Readonly<{
  /** The playing session's preview set (PB-2); absent or null leaves the tiles dark. */
  set?: TrickplaySet | null;
  /** The authority's image resolver: sheets are authenticated server paths. */
  artwork?: (path?: string) => ArtworkSource | undefined;
  /** The landing time in seconds; the strip is centred on it. */
  seconds: number;
  start: number;
  end: number;
}>;

const SMALL = {width: 256, height: 144}, LARGE = {width: 480, height: 270};
const SLOTS = [-2, -1, 0, 1, 2] as const;

function Tile({set, artwork, seconds, large, shadows}: {set?: TrickplaySet | null; artwork?: NativeTrickplayStripProps['artwork']; seconds: number; large: boolean; shadows: boolean}) {
  const theme = useTheme(), box = large ? LARGE : SMALL;
  // A download's sheets are files on this device, not server paths for the authority's resolver.
  const frame = nativeTrickplayWindow(set, seconds, box), image = frame ? frame.tileUrl.startsWith('file://') ? {uri: frame.tileUrl, headers: undefined} : artwork?.(frame.tileUrl) : undefined;
  // The tile's shadow follows the effects tier: the strip's five deep shadows are the costliest
  // thing a low-end TV draws while scrubbing.
  return <View style={[{width: box.width, height: box.height, borderRadius: 12, overflow: 'hidden', backgroundColor: '#0b1822', opacity: large ? 1 : .78,
    borderWidth: 1.5, borderColor: large ? '#edf5fa4d' : '#edf5fa29'}, shadows && (large ? styles.shadowLarge : styles.shadowSmall)]}>
    {frame && image && <View style={[styles.window, frame.window, {transform: [{scaleX: frame.scaleX}, {scaleY: frame.scaleY}]}]}>
      {/* The sheet at its own size: the same decode for every tile on it, shared, never upscaled. */}
      <Image source={{uri: image.uri, ...(image.headers ? {headers: {...image.headers}} : {})}} resizeMethod="scale" fadeDuration={0} style={[styles.sheet, frame.sheet]}/>
    </View>}
    <View style={{position: 'absolute', bottom: large ? 14 : 10, alignSelf: 'center', paddingHorizontal: large ? 14 : 10, paddingVertical: large ? 5 : 3, borderRadius: 8, backgroundColor: '#071018c7'}}>
      <Text style={[nativeType({size: large ? 30 : 18, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: large ? '#edf5fa' : '#c9d9e3', fontVariant: ['tabular-nums']}]}>{clockText(seconds)}</Text>
    </View>
  </View>;
}

/** Strip height 270; the small tiles sit on the same bottom line. It hangs
 * 12 above the bar (tv-player 2), which is the top 40 of the timeline box. */
export function NativeTrickplayStrip({set, artwork, seconds, start, end}: NativeTrickplayStripProps) {
  const shadows = useMotion().effects.shadows;
  const step = set ? set.intervalSeconds : 10;
  return <View pointerEvents="none" style={{position: 'absolute', bottom: 52, alignSelf: 'center', flexDirection: 'row', alignItems: 'flex-end', gap: 20}}>
    {SLOTS.map(slot => <Tile key={slot} set={set} artwork={artwork} large={slot === 0} shadows={shadows} seconds={Math.max(start, Math.min(end, seconds + slot * step))}/>)}
  </View>;
}

const CARD = {width: 178, height: 100};
/** phone-player 1b: while you drag, the picture at that point and its time, in a card above the
 * knob. Only a real preview frame draws it; without one the caller keeps the plain time bubble. */
export function NativeScrubPreview({set, artwork, seconds, left, fallback}: Readonly<{set?: TrickplaySet | null; artwork?: NativeTrickplayStripProps['artwork']; seconds: number; left: `${number}%`; fallback?: React.ReactNode}>) {
  const theme = useTheme();
  const frame = nativeTrickplayWindow(set, seconds, CARD), image = frame ? frame.tileUrl.startsWith('file://') ? {uri: frame.tileUrl, headers: undefined} : artwork?.(frame.tileUrl) : undefined;
  if (!frame || !image) return fallback ?? null;
  return <View pointerEvents="none" testID="scrub-preview" style={[{position: 'absolute', bottom: 44, left, width: CARD.width, marginLeft: -CARD.width / 2, height: CARD.height, borderRadius: 9,
    overflow: 'hidden', backgroundColor: '#000', borderWidth: 1.5, borderColor: '#edf5fa8c'}, styles.shadowCard]}>
    <View style={[styles.window, frame.window, {transform: [{scaleX: frame.scaleX}, {scaleY: frame.scaleY}]}]}>
      <Image source={{uri: image.uri, ...(image.headers ? {headers: {...image.headers}} : {})}} resizeMethod="scale" fadeDuration={0} style={[styles.sheet, frame.sheet]}/>
    </View>
    <View style={{position: 'absolute', left: 0, right: 0, bottom: 0, paddingTop: 14, paddingBottom: 5, alignItems: 'center'}}>
      <Svg style={StyleSheet.absoluteFill} width="100%" height="100%"><Defs><LinearGradient id="scrub-scrim" x1="0" y1="1" x2="0" y2="0">
        <Stop offset="0" stopColor="#03080c" stopOpacity=".85"/><Stop offset="1" stopColor="#03080c" stopOpacity="0"/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#scrub-scrim)"/></Svg>
      <Text style={[nativeType({size: 14, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: '#edf5fa', fontVariant: ['tabular-nums']}]}>{clockText(seconds)}</Text>
    </View>
  </View>;
}

const styles = StyleSheet.create({
  shadowCard: {shadowColor: '#000', shadowOpacity: .9, shadowRadius: 17, shadowOffset: {width: 0, height: 18}},
  shadowLarge: {shadowColor: '#000', shadowOpacity: .95, shadowRadius: 35, shadowOffset: {width: 0, height: 30}},
  shadowSmall: {shadowColor: '#000', shadowOpacity: .9, shadowRadius: 25, shadowOffset: {width: 0, height: 24}},
  window: {position: 'absolute', left: 0, top: 0, overflow: 'hidden', transformOrigin: 'left top'},
  sheet: {position: 'absolute'},
});
