import React from 'react';
import {Text, type TextProps} from 'react-native';
import {typeScale} from '@portico/design';
import {nativeType, useTheme} from '../theme';
import type {LayoutVariant} from './types';

export type LockupProps = Omit<TextProps, 'children'> & {title: string; long?: boolean; lines?: number; variant?: LayoutVariant};

export function Lockup({title, long = false, lines, variant, style, ...props}: LockupProps) {
  const theme = useTheme();
  const column = variant === 'desktop' || !variant && theme.layoutMode === 'desktop' ? 'desktop' : variant === 'tv' || (!variant && theme.isTV) ? 'tenFoot' : 'handheld';
  return <Text {...props} accessibilityRole="header" numberOfLines={lines} ellipsizeMode="tail"
    textBreakStrategy="balanced" android_hyphenationFrequency="none"
    style={[nativeType(typeScale[column][long ? 'lockupLong' : 'lockup'], theme.textScale), {color: theme.colors.text, flexShrink: 1, includeFontPadding: false, textShadowColor: `${theme.colors.ink}80`, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}, style]}>{title}</Text>;
}
