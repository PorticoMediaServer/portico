import React, {useState} from 'react';
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

/** The type scale's next title size below `size` (46, 42, 34, 26), for a title whose one word is wider than the line. */
export function smallerStep(size: number): number {
  const scale = typeScale.handheld, sizes = [scale.lockup.size, scale.lockupLong.size, scale.display.size, scale.title.size];
  return sizes.find(next => next < size) ?? size;
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
  const role = step === 'veryLong' ? 'lockupLong' : step, stepped = size ?? (step === 'veryLong' ? typeScale.handheld.display.size : typeScale[column][role].size);
  // A word wider than the line is never broken in the middle: the title takes the next size down instead (once).
  const key = `${title}|${stepped}`, [split, setSplit] = useState<string>(), down = handheld && split === key ? smallerStep(stepped) : stepped;
  const ratio = typeof lockup.lineHeight === 'number' ? lockup.lineHeight / lockup.size : undefined;
  const type = size !== undefined ? {size: down, lineHeight: 'normal' as const, weight: 800 as const, tracking: -.035}
    : handheld && (step === 'veryLong' || down !== stepped) ? {...lockup, size: down, lineHeight: ratio === undefined ? lockup.lineHeight : down * ratio}
    : typeScale[column][role];
  return <Text {...props} accessibilityRole="header" numberOfLines={lines ?? (handheld ? pageTitleLines : undefined)} ellipsizeMode="tail"
    onTextLayout={handheld && split !== key ? event => {if (event.nativeEvent.lines.slice(0, -1).some(line => !/[\s\-\u00ad]$/.test(line.text))) setSplit(key);} : undefined}
    textBreakStrategy="balanced" android_hyphenationFrequency="none"
    style={[nativeType(type, theme.textScale), {color: theme.colors.text, flexShrink: 1, includeFontPadding: false, textShadowColor: `${theme.colors.ink}80`, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}, style]}>{title}</Text>;
}
