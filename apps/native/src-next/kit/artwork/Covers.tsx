import React from 'react';
import {StyleSheet, View} from 'react-native';
import {useTheme} from '../theme';
import {Artwork, BookSpine} from './Artwork';
import {boundedCovers, coverAspect} from './model';
import type {CoverArrangementProps} from './types';

/** Exact homogeneous art fans. This component never selects or requests more member entries. */
export function CoverFan({covers, shape, variant = 'library', spine = false, width, height, alt, visible = true, style, renderFallback}: CoverArrangementProps) {
  const theme = useTheme();
  const samples = boundedCovers(covers, 3);
  const recipe = variant === 'series'
    ? {width: 112, height: 96, cover: shape === 'square' ? 60 : 50, right: [54, 28, 2], top: [14, 4, 12], rotate: [-10, -1, 9]}
    : variant === 'music'
      ? {width: 120, height: 90, cover: 76, right: [62, 30, 0], top: [12, 2, 10], rotate: [-12, -1, 11]}
      : {width: 112, height: shape === 'square' ? 66 : 82, cover: shape === 'square' ? 60 : 50, right: [58, 30, 2], top: [3, 0, 3], rotate: [-9, 0, 9]};
  const w = width ?? recipe.width, h = height ?? recipe.height;
  const scale = Math.min(w / recipe.width, h / recipe.height);
  const cw = recipe.cover * scale, ch = cw / coverAspect(shape);
  const count = renderFallback ? 3 : samples.length;
  return <View accessible={Boolean(alt)} accessibilityRole={alt ? 'image' : undefined} accessibilityLabel={alt} style={[{width: w, height: h}, style]}>
    {Array.from({length: count}, (_, index) => <View key={index} style={{position: 'absolute', width: cw, height: ch,
      right: recipe.right[index]! * scale, top: recipe.top[index]! * scale, transform: [{rotate: `${recipe.rotate[index]}deg`}],
      borderRadius: 5 * scale, backgroundColor: theme.colors.raised,
      shadowColor: theme.colors.shadow, shadowOpacity: 0.85, shadowOffset: {width: 0, height: 12 * scale}, shadowRadius: 11 * scale}}>
      <View style={[StyleSheet.absoluteFillObject, {borderRadius: 5 * scale, overflow: 'hidden'}]}>
        <Artwork source={samples[index]} visible={visible} fallback={renderFallback?.(index)} imageOverlay={spine ? <BookSpine/> : undefined}/>
        <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {borderWidth: 1, borderColor: theme.colors.text, opacity: 0.16, borderRadius: 5 * scale}]}/>
      </View>
    </View>)}
  </View>;
}

export function CoverMosaic({covers, shape, width = 120, height = width / coverAspect(shape), alt, visible = true, style, renderFallback}: CoverArrangementProps) {
  const {colors} = useTheme();
  const samples = boundedCovers(covers, 4);
  const count = renderFallback ? 4 : samples.length;
  const columns = count === 1 ? 1 : 2, rows = count <= 2 ? 1 : 2;
  return <View accessible={Boolean(alt)} accessibilityRole={alt ? 'image' : undefined} accessibilityLabel={alt} style={[{width, height, backgroundColor: colors.raised, overflow: 'hidden'}, style]}>
    {Array.from({length: count}, (_, index) => <View key={index} style={{position: 'absolute', width: width / columns, height: height / rows,
      left: index % columns * width / columns, top: Math.floor(index / columns) * height / rows}}>
      <Artwork source={samples[index]} visible={visible} fallback={renderFallback?.(index)}/>
    </View>)}
  </View>;
}
