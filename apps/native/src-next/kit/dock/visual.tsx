import React from 'react';
import {StyleSheet, Text, View, type TextStyle} from 'react-native';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {nativeType, useTheme, type Theme} from '../theme';
import {expandedDockMetrics, mediumDockMetrics} from './model';

export const alpha = (color: string, opacity: number) => `${color.slice(0, 7)}${Math.round(opacity * 255).toString(16).padStart(2, '0')}`;
export const dockType = (theme: Theme, size: number, weight: 400 | 500 | 600 | 700 | 800, tracking = 0, lineHeight: number | 'normal' = 'normal'): TextStyle => nativeType({size, weight, tracking, lineHeight}, theme.textScale);

/** Facts remain distinct elements; one part may truncate without deleting its leading code. */
export function DockFacts({parts, style, gap = 12}: {parts: readonly FactPart[]; style: TextStyle; gap?: number}) {
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap, minWidth: 0, flexShrink: 1, overflow: 'hidden'}}>
    {parts.map((part, index) => <Text key={part.id} numberOfLines={1} ellipsizeMode="tail" style={[style, {flexShrink: index === parts.length - 1 ? 1 : 0}]}>{part.text}</Text>)}
  </View>;
}

/** Three deliberately still bars. Paused is a separate icon at the caller, never an animation. */
export function LevelMeter({large = false, color}: {large?: boolean; color?: string}) {
  const theme = useTheme();
  const height = large ? 20 : 16;
  return <View accessible={false} importantForAccessibility="no-hide-descendants" style={{height, gap: large ? 4 : 3, flexDirection: 'row', alignItems: 'flex-end'}}>
    {[.55, 1, .38].map((factor, index) => <View key={index} style={{width: large ? 5 : 4, height: height * factor, borderRadius: 1.5, backgroundColor: color ?? theme.colors.accentStrong}}/>)}
  </View>;
}

export const dockStyles = StyleSheet.create({
  fill: {position: 'absolute', inset: 0},
  center: {alignItems: 'center', justifyContent: 'center'},
  phone: {height: 62, borderRadius: 14, flexDirection: 'row', alignItems: 'center', paddingLeft: 9, paddingRight: 6, gap: 12, overflow: 'hidden'},
  medium: {height: mediumDockMetrics.height, gap: mediumDockMetrics.gap},
  mediumButton: {width: mediumDockMetrics.button, height: mediumDockMetrics.button, borderRadius: mediumDockMetrics.button / 2},
  video: {paddingLeft: 7, gap: 11},
  art: {width: 44, height: 44, borderRadius: 7, overflow: 'hidden'},
  videoArt: {width: 80, height: 45, borderRadius: 7, overflow: 'hidden'},
  expand: {position: 'absolute', right: 3, top: 3, width: 17, height: 17, borderRadius: 5, alignItems: 'center', justifyContent: 'center'},
  body: {flex: 1, minWidth: 0, alignItems: 'center', flexDirection: 'row', gap: 12},
  text: {flex: 1, minWidth: 0, overflow: 'hidden'},
  transports: {flexDirection: 'row', alignItems: 'center', marginLeft: -4},
  phoneButton: {width: 40, height: 40, alignItems: 'center', justifyContent: 'center', borderRadius: 20},
  close: {width: 34},
  line: {position: 'absolute', left: 12, right: 12, bottom: 0, height: 2},
  /** tablet-play 4a/13: the expanded dock at the content's foot. */
  expanded: {height: expandedDockMetrics.height, flexDirection: 'row', alignItems: 'center', paddingLeft: expandedDockMetrics.sideLeft,
    paddingRight: expandedDockMetrics.sideRight, overflow: 'hidden'},
  expandedBody: {width: 232, flexDirection: 'row', alignItems: 'center', gap: 12},
  expandedCard: {width: expandedDockMetrics.cardWidth, height: expandedDockMetrics.cardHeight, borderRadius: 8, overflow: 'hidden'},
  expandedMid: {position: 'absolute', left: 0, right: 0, top: 10, alignItems: 'center'},
  expandedButton: {width: expandedDockMetrics.button, height: expandedDockMetrics.button, alignItems: 'center', justifyContent: 'center', borderRadius: expandedDockMetrics.button / 2},
  expandedPrimary: {width: expandedDockMetrics.primary, height: expandedDockMetrics.primary, alignItems: 'center', justifyContent: 'center', borderRadius: expandedDockMetrics.primary / 2},
  expandedScrub: {width: expandedDockMetrics.scrubWidth, marginTop: 4, height: 17, flexDirection: 'row', alignItems: 'center', gap: 8},
  tvQuiet: {width: 440, height: 84, borderRadius: 18, flexDirection: 'row', alignItems: 'center', gap: 18, paddingLeft: 10, paddingRight: 24, overflow: 'hidden'},
  tvFlyout: {width: 700, padding: 30, borderRadius: 24, overflow: 'hidden'},
  tvButton: {width: 72, height: 72, borderRadius: 36, alignItems: 'center', justifyContent: 'center'},
  tvPrimary: {width: 88, height: 88, borderRadius: 44},
});
