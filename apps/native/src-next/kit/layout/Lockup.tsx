import React, {useState} from 'react';
import {StyleSheet, Text, type TextProps, type TextStyle} from 'react-native';
import {typeScale} from '@portico/design';
import {nativeType, useTheme} from '../theme';
import type {LayoutVariant} from './types';

export type LockupProps = Omit<TextProps, 'children'> & {
  title: string; long?: boolean; lines?: number; variant?: LayoutVariant;
  /** A page whose frame names its own title size (phone-app `.alb h1` 30, the tablet music pages): that size, no length steps. */
  size?: number;
  /** With `size`: the frame's line height and letter spacing as shares of the size, so a step down scales them too. */
  lineRatio?: number; tracking?: number;
};

/** The frames' page-title steps on a phone or tablet: 46; over 20 characters 42 (`.lockup.md`, the web's `longTitle`);
 * over 40 the type scale's display size, 34 (phone-read `.lockup.md`), so a 60-character title shows whole. */
export function lockupStep(title: string): 'lockup' | 'lockupLong' | 'veryLong' {
  const length = [...title.trim()].length;
  return length > 40 ? 'veryLong' : length > 20 ? 'lockupLong' : 'lockup';
}

/** The column's page-title sizes from the type scale, largest first: on a phone or tablet 64, 58, 46, 42, 34 and 26. */
export function titleSizes(column: 'handheld' | 'desktop' | 'tenFoot' = 'handheld'): number[] {
  const scale = typeScale[column];
  return [...new Set([scale.lockup, scale.lockupLong, scale.lockupLarge, scale.lockupMedium, scale.display, scale.title].map(style => style.size))].sort((x, y) => y - x);
}

/** The scale's next title size below `size` (or `size` itself at the bottom). */
export function smallerStep(size: number, column: 'handheld' | 'desktop' | 'tenFoot' = 'handheld'): number {
  return titleSizes(column).find(next => next < size) ?? size;
}

/** A page title takes the scale's next size down while it runs past this many lines, down to the display size (34 on a
 * phone, still a title); there it wraps as far as it needs. It is never cut (owner, 13 Nov: "never end a title in dots"). */
export const pageTitleLines = 3;

/** The one page title: Home's hero, a title, album, artist or collection page. Off TV it steps down, then wraps; it is never
 * cut. A word wider than the line also takes the next size down rather than break in the middle. `lines` (TV) caps it. */
export function Lockup({title, long = false, lines, size, lineRatio, tracking = -.035, variant, style, ...props}: LockupProps) {
  const theme = useTheme();
  const column = variant === 'desktop' || !variant && theme.layoutMode === 'desktop' ? 'desktop' : variant === 'tv' || (!variant && theme.isTV) ? 'tenFoot' : 'handheld';
  const page = column !== 'tenFoot', phoneSteps = size === undefined && column === 'handheld' && theme.platform === 'phone', lockup = typeScale.handheld.lockup;
  // The phone frames' starting size by length (46; 42 past 20 characters; 34 past 40); a frame-sized page names its own.
  const step = phoneSteps ? (lockupStep(title) === 'lockup' && long ? 'lockupLong' : lockupStep(title)) : long ? 'lockupLong' : 'lockup';
  const base = typeScale[column][step === 'veryLong' ? 'lockupLong' : step], display = typeScale.handheld.display.size;
  const type = size !== undefined ? {size, lineHeight: lineRatio === undefined ? 'normal' as const : size * lineRatio, weight: 800 as const, tracking}
    : step === 'veryLong' ? {...lockup, size: display, lineHeight: display * (lockup.lineHeight as number) / lockup.size} : base;
  // A caller's own size (a tablet hero's drawn column) is where the steps start; its line height and tracking scale with it.
  const drawn = StyleSheet.flatten([nativeType(type, theme.textScale), style]) as TextStyle, start = (drawn.fontSize ?? type.size) / theme.textScale;
  const key = `${title}|${start}|${column}`, [fit, setFit] = useState<{key: string; size: number}>();
  const current = fit?.key === key ? fit.size : start, next = smallerStep(current, column), share = current / start;
  const floor = typeScale[column].display.size;
  const scaled = share === 1 ? undefined : {fontSize: drawn.fontSize! * share, ...(typeof drawn.lineHeight === 'number' ? {lineHeight: drawn.lineHeight * share} : {}), ...(typeof drawn.letterSpacing === 'number' ? {letterSpacing: drawn.letterSpacing * share} : {})};
  return <Text {...props} accessibilityRole="header" numberOfLines={lines} ellipsizeMode="tail"
    onTextLayout={page && next < current ? event => {
      const shown = event.nativeEvent.lines, split = shown.slice(0, -1).some(line => !/[\s\-\u00ad]$/.test(line.text));
      if (split || (shown.length > pageTitleLines && current > floor)) setFit({key, size: next});
    } : undefined}
    textBreakStrategy="balanced" android_hyphenationFrequency="none"
    style={[nativeType(type, theme.textScale), {color: theme.colors.text, flexShrink: 1, includeFontPadding: false, textShadowColor: `${theme.colors.ink}80`, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}, style, scaled]}>{title}</Text>;
}
