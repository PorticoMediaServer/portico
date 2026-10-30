import type {TextStyle} from 'react-native';
import {nativeType, type Theme} from '../theme';
import type {BrowsePlatform} from './types';

/** Exact caption/page-CSS sizes live in this kit, with Manrope's normal line metrics. */
export function browseType(theme: Theme, size: number, weight: 400 | 500 | 600 | 700 | 800 = 600, tracking = 0, lineHeight: number | 'normal' = 'normal'): TextStyle {
  return nativeType({size, weight, tracking, lineHeight}, theme.textScale);
}
export const browsePlatform = (theme: Theme, supplied?: BrowsePlatform): BrowsePlatform => supplied ?? (theme.isTV ? 'tv' : theme.platform === 'phone' ? 'phone' : 'desktop');

/** Native alpha derives from the canonical six-digit colour, never a parallel palette. */
export function alpha(color: string, opacity: number): string {
  return `${color}${Math.round(Math.max(0, Math.min(1, opacity)) * 255).toString(16).padStart(2, '0')}`;
}

export {nextFacetState, changeFacetSelection} from './facet-selection';
