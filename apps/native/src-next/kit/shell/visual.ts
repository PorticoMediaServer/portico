import {activeTextScale, nativeType} from '../theme';
import type {TypeStyle} from '@portico/design';

/** CSS normal remains the font's native line metrics; never turn it into a numeric line height.
 * The fifth argument is the confirmed scale; omitted callers read the one published by ThemeProvider. */
export const shellType = (size: number, weight: TypeStyle['weight'] = 600, lineHeight: TypeStyle['lineHeight'] = 'normal', tracking = 0, textScale?: number) => nativeType({size, weight, lineHeight, tracking}, textScale ?? activeTextScale());
export function alpha(color: string, opacity: number) {
  const byte = Math.round(Math.max(0, Math.min(1, opacity)) * 255).toString(16).padStart(2, '0');
  return `${color.slice(0, 7)}${byte}`;
}
