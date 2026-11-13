import React from 'react';
import {Text, type TextProps} from 'react-native';
import {typeScale} from '@portico/design';
import {nativeType, useTheme} from '../theme';
import type {LayoutVariant} from './types';

export type LockupProps = Omit<TextProps, 'children'> & {
  title: string; long?: boolean; lines?: number; variant?: LayoutVariant;
  /** A page whose frame names its own title size (phone-app `.alb h1` 30, the tablet music pages): that size, no steps. */
  size?: number;
};

/** The frames' page-title steps on a phone or tablet: 46; over 20 characters 42 (`.lockup.md`, the web's `longTitle`);
 * over 40 the type scale's display size, 34 (phone-read `.lockup.md`), so a 60-character title shows whole. */
export function lockupStep(title: string): 'lockup' | 'lockupLong' | 'veryLong' {
  const length = [...title.trim()].length;
  return length > 40 ? 'veryLong' : length > 20 ? 'lockupLong' : 'lockup';
}

/** Lines a page title may take before it is cut (the frames draw it unclamped; the cut is a safety for absurd titles
 * and the largest text sizes). */
export const pageTitleLines = 4;

/** The one page title: Home's hero, a title, album, artist or collection page. It wraps; it is never cut at normal size. */
export function Lockup({title, long = false, lines, size, variant, style, ...props}: LockupProps) {
  const theme = useTheme();
  const column = variant === 'desktop' || !variant && theme.layoutMode === 'desktop' ? 'desktop' : variant === 'tv' || (!variant && theme.isTV) ? 'tenFoot' : 'handheld';
  const handheld = column === 'handheld', lockup = typeScale.handheld.lockup;
  const step = handheld ? (lockupStep(title) === 'lockup' && long ? 'lockupLong' : lockupStep(title)) : long ? 'lockupLong' : 'lockup';
  const type = size !== undefined ? {size, lineHeight: 'normal' as const, weight: 800 as const, tracking: -.035}
    : step === 'veryLong' ? {...lockup, size: typeScale.handheld.display.size, lineHeight: typeof lockup.lineHeight === 'number' ? typeScale.handheld.display.size * lockup.lineHeight / lockup.size : lockup.lineHeight}
    : typeScale[column][step];
  return <Text {...props} accessibilityRole="header" numberOfLines={lines ?? (handheld ? pageTitleLines : undefined)} ellipsizeMode="tail"
    textBreakStrategy="balanced" android_hyphenationFrequency="none"
    style={[nativeType(type, theme.textScale), {color: theme.colors.text, flexShrink: 1, includeFontPadding: false, textShadowColor: `${theme.colors.ink}80`, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}, style]}>{title}</Text>;
}
