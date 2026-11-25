import React from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

export type GuideTextProps = {
  title: string;
  lines: number;
  style: StyleProp<TextStyle>;
  maxHeight?: number;
  whole?: boolean;
  onHeight?: (height: number) => void;
};

/** Renders a guide or card title with deterministic single-pass native truncation,
 * avoiding hidden measurement text, layout retry timers, and global fit caches. */
export function GuideProgrammeTitle({title, lines, style, maxHeight, onHeight}: GuideTextProps) {
  if (!title) return null;
  return <View style={{flex: 1, minWidth: 0}}>
    <Text numberOfLines={lines} ellipsizeMode="tail" textBreakStrategy="simple" android_hyphenationFrequency="none"
      style={[style, maxHeight !== undefined ? {maxHeight} : undefined]}
      onLayout={onHeight ? event => onHeight(event.nativeEvent.layout.height) : undefined}>{title}</Text>
  </View>;
}
