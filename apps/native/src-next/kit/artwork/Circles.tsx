import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Svg, {Defs, RadialGradient, Rect, Stop} from 'react-native-svg';
import {fontFamilies, useTheme} from '../theme';
import {Artwork} from './Artwork';
import {PaletteWash} from './Surfaces';
import {boundedCovers, circleArtworkFrames} from './model';
import type {CircleArtProps, PersonCircleProps} from './types';

function CircleBase({size}: {size: number}) {
  const {colors} = useTheme();
  return <Svg accessible={false} pointerEvents="none" width={size} height={size} style={StyleSheet.absoluteFillObject}>
    <Defs><RadialGradient id="circle-base" cx="30%" cy="25%" r="90%"><Stop offset="0" stopColor={colors.raisedBright}/><Stop offset="1" stopColor={colors.raised}/></RadialGradient></Defs>
    <Rect width="100%" height="100%" fill="url(#circle-base)"/>
  </Svg>;
}

/** Bounded own-album samples, supplied by the caller's actual content entry. */
export function CircleArt({covers, initials, alt, size = 100, visible = true, style}: CircleArtProps) {
  const {colors, isTV} = useTheme();
  const samples = boundedCovers(covers, 4);
  const frames = circleArtworkFrames(samples.length, size, isTV ? 3 : 2);
  return <View accessible={Boolean(alt)} accessibilityRole={alt ? 'image' : undefined} accessibilityLabel={alt} style={[{width: size, height: size, borderRadius: size / 2, overflow: 'hidden', backgroundColor: samples.length > 1 ? colors.ink : colors.raised}, style]}>
    {!samples.length ? <CircleBase size={size}/> : null}
    {initials ? <Text accessible={false} allowFontScaling={false} style={{position: 'absolute', width: size, top: 0, bottom: 0, textAlign: 'center', textAlignVertical: 'center', fontFamily: fontFamilies[800], fontSize: size * 0.3125, lineHeight: size, color: colors.textSecondary, letterSpacing: -size * 0.015}}>{initials}</Text> : null}
    {samples.length === 1 ? <Artwork source={samples[0]} visible={visible} zoom={isTV ? 1 : 1.22}/> : samples.map((source, index) => {
      const split = samples.length === 2;
      const frame = frames[index]!;
      return <Artwork key={`${source.cacheKey}-${index}`} source={source} visible={visible} width={frame.width} height={frame.height}
        style={{position: 'absolute', left: frame.left, top: frame.top}}
        zoom={split && !isTV ? 1.5 : 1} position={split && !isTV ? {x: index ? 0.6 : 0.3, y: 0.5} : undefined}/>;
    })}
    <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {borderRadius: size / 2, borderWidth: isTV ? 1.5 : 1, borderColor: colors.text, opacity: isTV ? 0.1 : 0.12}]}/>
  </View>;
}

export type ArtistCircleProps = CircleArtProps & {rings?: readonly number[]};
export function ArtistCircle({rings, ...props}: ArtistCircleProps) {
  const {colors} = useTheme();
  const size = props.size ?? 100;
  return <View style={{width: size, height: size}}>
    <CircleArt {...props}/>
    {rings?.map(inset => <View key={inset} pointerEvents="none" style={[StyleSheet.absoluteFillObject, {inset: -inset, borderRadius: (size + inset * 2) / 2, borderWidth: 1.5, borderColor: colors.text, opacity: 0.08}]}/>)}
  </View>;
}

/** Real portraits when supplied; authors otherwise keep initials over only their own covers. */
export function PersonCircle({source, covers = [], ambientCovers = [], palette, initials, alt, size = 96, visible = true, style}: PersonCircleProps) {
  const {colors, isTV} = useTheme();
  const samples = boundedCovers(isTV ? ambientCovers : covers, 4);
  return <View accessible={Boolean(alt)} accessibilityRole={alt ? 'image' : undefined} accessibilityLabel={alt} style={[{width: size, height: size, borderRadius: size / 2, overflow: 'hidden', backgroundColor: colors.raised}, style]}>
    <CircleBase size={size}/>
    {palette ? <PaletteWash palette={palette}/> : null}
    {samples.length ? <View style={[StyleSheet.absoluteFillObject, {left: -size * 0.25, right: -size * 0.25, top: -size * 0.25, bottom: -size * 0.25, flexDirection: 'row'}]}>
      {samples.map((cover, i) => <View key={`${cover.cacheKey}-${i}`} style={{flex: 1}}><Artwork source={cover} visible={visible} sized={isTV} blurRadius={isTV ? undefined : 20} filter={isTV ? undefined : [{saturate: 1.5}, {brightness: 0.56}]} imageStyle={isTV ? {opacity: 0.56} : undefined}/></View>)}
    </View> : null}
    {samples.length ? <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {backgroundColor: colors.ink, opacity: 0.34}]}/> : null}
    <View style={[StyleSheet.absoluteFillObject, {alignItems: 'center', justifyContent: 'center'}]}><Text accessible={false} allowFontScaling={false} style={{fontFamily: fontFamilies[800], fontSize: size * (samples.length ? 50 / 172 : 30 / 96), letterSpacing: -size * 0.0145, color: samples.length ? colors.text : colors.textSecondary}}>{initials}</Text></View>
    {source ? <Artwork source={source} visible={visible} fallback={null}/> : null}
    <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {borderRadius: size / 2, borderWidth: 1, borderColor: colors.text, opacity: 0.12}]}/>
  </View>;
}
