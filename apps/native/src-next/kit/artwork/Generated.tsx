import React, {useState} from 'react';
import {StyleSheet, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import Svg, {Defs, RadialGradient, Rect, Stop} from 'react-native-svg';
import {fontFamilies, useTheme} from '../theme';
import {BookSpine} from './Artwork';
import {mixColour} from './model';
import {PaletteWash} from './Surfaces';
import type {ArtworkPalette, GeneratedCoverProps} from './types';

export type GeneratedArtProps = Readonly<{
  glyph?: string;
  palette?: ArtworkPalette;
  width?: number;
  height?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** This title-art fallback is never used by auth/state/Server bands; those use ColourBackdrop. */
export function GeneratedArt({glyph, palette, width, height, style, testID}: GeneratedArtProps) {
  const theme = useTheme();
  return <View testID={testID} accessible={false} style={[StyleSheet.absoluteFillObject, {width, height, overflow: 'hidden'}, style]}>
    <PaletteWash generated palette={palette ?? {first: theme.colors.accentDeep, second: theme.colors.generatedGreen}}/>
    {glyph ? <Text accessible={false} allowFontScaling={false} style={[theme.type.generatedGlyph, {position: 'absolute', right: theme.spec.generatedArt.glyph.right, bottom: theme.spec.generatedArt.glyph.bottom, color: theme.colors.text, opacity: theme.spec.generatedArt.glyph.alpha}]}>{glyph}</Text> : null}
  </View>;
}

/** A typographic cover at the size drawn; tint is canonical and explicitly supplied. */
export function GeneratedCover({title, author, tint, fontScale = 12.6, glyph, width, height, style, testID}: GeneratedCoverProps) {
  const theme = useTheme();
  const [measuredWidth, setMeasuredWidth] = useState(width ?? 0);
  const w = width ?? measuredWidth;
  const fontSize = w * fontScale / 100;
  const small = w <= 80;
  const authorSize = Math.min(22, Math.max(8, w * 0.068));
  return <View testID={testID} accessible accessibilityRole="image" accessibilityLabel={title}
    onLayout={event => setMeasuredWidth(event.nativeEvent.layout.width)}
    style={[{width, height, aspectRatio: height === undefined ? 2 / 3 : undefined, overflow: 'hidden', backgroundColor: mixColour('#0a1620', tint.second, 0.24)}, style]}>
    <Svg pointerEvents="none" accessible={false} width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none" style={StyleSheet.absoluteFillObject}>
      <Defs>
        <RadialGradient id="cover-first" gradientUnits="userSpaceOnUse" cx="1000" cy="0" rx="1100" ry="740"><Stop offset="0" stopColor={tint.first}/><Stop offset="0.68" stopColor={tint.first} stopOpacity={0}/></RadialGradient>
        <RadialGradient id="cover-second" gradientUnits="userSpaceOnUse" cx="140" cy="1180" rx="960" ry="640"><Stop offset="0" stopColor={tint.second}/><Stop offset="0.74" stopColor={tint.second} stopOpacity={0}/></RadialGradient>
      </Defs>
      <Rect width="1000" height="1000" fill="url(#cover-second)"/><Rect width="1000" height="1000" fill="url(#cover-first)"/>
    </Svg>
    {w > 0 ? <>
      <Text accessible={false} allowFontScaling={false} style={{fontFamily: fontFamilies[800], color: theme.colors.text, opacity: 0.085, position: 'absolute', right: -0.06 * w, bottom: -0.27 * w, fontSize: 1.04 * w, lineHeight: 1.04 * w, letterSpacing: -0.06 * 1.04 * w, includeFontPadding: false}}>{glyph}</Text>
      <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {paddingTop: w * (small ? 0.11 : 0.12), paddingRight: w * 0.11, paddingBottom: w * 0.11, paddingLeft: w * 0.13}]}>
        {!small ? <View style={{width: w * 0.09, height: w * 0.012, borderRadius: w * 0.01, backgroundColor: theme.colors.text, opacity: 0.55, marginBottom: w * 0.065}}/> : null}
        <Text accessible={false} allowFontScaling={false} numberOfLines={5} textBreakStrategy="balanced" style={{fontFamily: fontFamilies[800], color: theme.colors.text, fontSize, lineHeight: fontSize * 1.03, letterSpacing: fontSize * -0.04, textShadowColor: `${theme.colors.ink}66`, textShadowOffset: {width: 0, height: 1}, textShadowRadius: 14, includeFontPadding: false}}>{title}</Text>
        {!small && author ? <Text accessible={false} allowFontScaling={false} style={{fontFamily: fontFamilies[700], color: theme.colors.textSecondary, fontSize: authorSize, lineHeight: authorSize * 1.25, marginTop: 'auto'}}>{author}</Text> : null}
      </View>
    </> : null}
    <BookSpine/>
    <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {borderWidth: 1, borderColor: theme.colors.text, opacity: 0.07}]}/>
  </View>;
}
