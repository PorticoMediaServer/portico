import React, {useState} from 'react';
import {Text, type TextProps} from 'react-native';
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

/** The type scale's next title size below `size` (46, 42, 34, 26), for a title whose one word is wider than the line. */
export function smallerStep(size: number, column: 'handheld' | 'desktop' | 'tenFoot' = 'handheld'): number {
  // The column's title sizes from the type scale, largest first; a title steps from its own size to the next one below it.
  const sizes = [...new Set(Object.values(typeScale[column]).filter(style => style.weight >= 700 && style.size >= 26).map(style => style.size))].sort((x, y) => y - x);
  return sizes.find(next => next < size) ?? size;
}

/** Lines a page title may take before it is cut (the frames draw it unclamped; the cut is a safety for absurd titles
 * and the largest text sizes). */
export const pageTitleLines = 4;

/** The one page title: Home's hero, a title, album, artist or collection page. It wraps; it is never cut at normal size. */
export function Lockup({title, long = false, lines, size, lineRatio, tracking = -.035, variant, style, ...props}: LockupProps) {
  const theme = useTheme();
  const column = variant === 'desktop' || !variant && theme.layoutMode === 'desktop' ? 'desktop' : variant === 'tv' || (!variant && theme.isTV) ? 'tenFoot' : 'handheld';
  // Every page title but TV's wraps up to four lines and never breaks a word; the length steps are the phone frames'.
  const page = column !== 'tenFoot', phoneSteps = size === undefined && column === 'handheld' && theme.platform === 'phone', lockup = typeScale.handheld.lockup;
  const step = phoneSteps ? (lockupStep(title) === 'lockup' && long ? 'lockupLong' : lockupStep(title)) : long ? 'lockupLong' : 'lockup';
  const role = step === 'veryLong' ? 'lockupLong' : step, base = typeScale[column][role];
  const stepped = size ?? (step === 'veryLong' ? typeScale.handheld.display.size : base.size);
  // A word wider than the line is never broken in the middle: the title takes the scale's next size down instead (once).
  const key = `${title}|${stepped}`, [split, setSplit] = useState<string>(), down = page && split === key ? smallerStep(stepped, column) : stepped;
  const ratio = (style: typeof base) => typeof style.lineHeight === 'number' ? style.lineHeight / style.size : undefined;
  const type = size !== undefined ? {size: down, lineHeight: lineRatio === undefined ? 'normal' as const : down * lineRatio, weight: 800 as const, tracking}
    : step === 'veryLong' || down !== stepped ? {...(step === 'veryLong' ? lockup : base), size: down, lineHeight: ratio(step === 'veryLong' ? lockup : base) === undefined ? base.lineHeight : down * ratio(step === 'veryLong' ? lockup : base)!}
    : base;
  return <Text {...props} accessibilityRole="header" numberOfLines={lines ?? (page ? pageTitleLines : undefined)} ellipsizeMode="tail"
    onTextLayout={page && split !== key ? event => {if (event.nativeEvent.lines.slice(0, -1).some(line => !/[\s\-\u00ad]$/.test(line.text))) setSplit(key);} : undefined}
    textBreakStrategy="balanced" android_hyphenationFrequency="none"
    style={[nativeType(type, theme.textScale), {color: theme.colors.text, flexShrink: 1, includeFontPadding: false, textShadowColor: `${theme.colors.ink}80`, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}, style]}>{title}</Text>;
}
