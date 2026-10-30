import React from 'react';
import {Image, StyleSheet, Text, View} from 'react-native';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {PlaybackOptions} from '../../../../../packages/client-core/src/playback-v1/types';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import {nativeTrickplayWindow} from './trickplay-frame';

/** The five-picture strip frame 2 draws above the seek bar. Real trickplay
 * sheets supply each frame; a sheet the server has not published leaves the
 * tile dark rather than inventing a picture. The middle tile is the one the
 * viewer would land on. */
export type NativeTrickplayStripProps = Readonly<{
  trickplay?: PlaybackOptions['trickplay'];
  /** The landing time in seconds; the strip is centred on it. */
  seconds: number;
  start: number;
  end: number;
}>;

const SMALL = {width: 256, height: 144}, LARGE = {width: 480, height: 270};
const SLOTS = [-2, -1, 0, 1, 2] as const;

function Tile({trickplay, seconds, large, shadows}: {trickplay?: PlaybackOptions['trickplay']; seconds: number; large: boolean; shadows: boolean}) {
  const theme = useTheme(), box = large ? LARGE : SMALL;
  const frame = nativeTrickplayWindow(trickplay, seconds, box);
  // The tile's shadow follows the effects tier: the strip's five deep shadows are the costliest
  // thing a low-end TV draws while scrubbing.
  return <View style={[{width: box.width, height: box.height, borderRadius: 12, overflow: 'hidden', backgroundColor: '#0b1822', opacity: large ? 1 : .78,
    borderWidth: 1.5, borderColor: large ? '#edf5fa4d' : '#edf5fa29'}, shadows && (large ? styles.shadowLarge : styles.shadowSmall)]}>
    {frame && <View style={[styles.window, frame.window, {transform: [{scaleX: frame.scaleX}, {scaleY: frame.scaleY}]}]}>
      {/* The sheet at its own size: the same decode for every tile, shared, never upscaled. */}
      <Image source={{uri: trickplay!.url}} resizeMethod="scale" fadeDuration={0} style={[styles.sheet, frame.sheet]}/>
    </View>}
    <View style={{position: 'absolute', bottom: large ? 14 : 10, alignSelf: 'center', paddingHorizontal: large ? 14 : 10, paddingVertical: large ? 5 : 3, borderRadius: 8, backgroundColor: '#071018c7'}}>
      <Text style={[nativeType({size: large ? 30 : 18, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: large ? '#edf5fa' : '#c9d9e3', fontVariant: ['tabular-nums']}]}>{clockText(seconds)}</Text>
    </View>
  </View>;
}

/** Strip height 270; the small tiles sit on the same bottom line. It hangs
 * 12 above the bar (tv-player 2), which is the top 40 of the timeline box. */
export function NativeTrickplayStrip({trickplay, seconds, start, end}: NativeTrickplayStripProps) {
  const shadows = useMotion().effects.shadows;
  const step = trickplay && trickplay.intervalMs > 0 ? trickplay.intervalMs / 1000 : 10;
  return <View pointerEvents="none" style={{position: 'absolute', bottom: 52, alignSelf: 'center', flexDirection: 'row', alignItems: 'flex-end', gap: 20}}>
    {SLOTS.map(slot => <Tile key={slot} trickplay={trickplay} large={slot === 0} shadows={shadows} seconds={Math.max(start, Math.min(end, seconds + slot * step))}/>)}
  </View>;
}

const styles = StyleSheet.create({
  shadowLarge: {shadowColor: '#000', shadowOpacity: .95, shadowRadius: 35, shadowOffset: {width: 0, height: 30}},
  shadowSmall: {shadowColor: '#000', shadowOpacity: .9, shadowRadius: 25, shadowOffset: {width: 0, height: 24}},
  window: {position: 'absolute', left: 0, top: 0, overflow: 'hidden', transformOrigin: 'left top'},
  sheet: {position: 'absolute'},
});
