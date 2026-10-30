import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {channelTint} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {fontFamilies, useTheme} from '../theme';
import {Artwork} from './Artwork';
import type {ChannelMarkProps} from './types';

/** mark.text was settled over the authoritative collision domain, never this visible page. */
export function ChannelMark({mark, source, size = 34, sized = false, alt, visible = true, style, onStatusChange}: ChannelMarkProps) {
  const theme = useTheme();
  return <View accessible={Boolean(alt)} accessibilityRole={alt ? 'image' : undefined} accessibilityLabel={alt} style={[{width: size, height: size, borderRadius: size * 8 / 34, overflow: 'hidden'}, style, {backgroundColor: channelTint(mark.seed)}]}>
    <View style={[StyleSheet.absoluteFillObject, {justifyContent: 'center', alignItems: 'center'}]}><Text accessible={false} numberOfLines={1} adjustsFontSizeToFit allowFontScaling={false} style={{fontFamily: fontFamilies[800], fontSize: size * (mark.text.length > 2 ? 0.32 : 0.38), color: theme.colors.text, maxWidth: size - 4, letterSpacing: -0.01 * size}}>{mark.text}</Text></View>
    {source ? <Artwork source={source} sized={sized} visible={visible} fit="contain" onStatusChange={onStatusChange}/> : null}
  </View>;
}
