import type {SubtitlePreferences} from '../../../../../packages/client-core/src/preference-effects';
import type {PreferenceValues} from '../../../../../packages/client-core/src/preferences';

/** Existing platform command data. System style and file styling remain explicit
 * choices, never inferred output capabilities. */
export type CaptionStyle = SubtitlePreferences & Readonly<{scale: number}>;

const scales: Readonly<Record<SubtitlePreferences['size'], number>> = {small: 0.8, medium: 1, large: 1.3, 'extra-large': 1.6};

/** Effective values from the authority-owned reader. The registry owns defaults;
 * absent or malformed settings cannot silently become another caption style. */
export function nativeCaptionPreferences(values: PreferenceValues): SubtitlePreferences {
  const choice = <T extends string>(key: string, allowed: readonly T[]): T => {
    const value = values[key];
    if (typeof value !== 'string' || !allowed.includes(value as T)) throw new Error('invalid_playback_preference');
    return value as T;
  };
  const boolean = (key: string): boolean => {
    const value = values[key];
    if (typeof value !== 'boolean') throw new Error('invalid_playback_preference');
    return value;
  };
  return Object.freeze({
    size: choice('playback.subtitleSize', ['small', 'medium', 'large', 'extra-large'] as const),
    color: choice('playback.subtitleColor', ['white', 'yellow', 'cyan', 'green', 'pink'] as const),
    background: choice('playback.subtitleBackground', ['none', 'outline', 'shadow', 'translucent', 'opaque'] as const),
    position: choice('playback.subtitlePosition', ['bottom', 'raised', 'top'] as const),
    font: choice('playback.subtitleFont', ['default', 'serif', 'mono', 'rounded'] as const),
    keepStyling: boolean('playback.keepSubtitleStyling'),
    useSystemCaptionStyle: boolean('playback.useSystemCaptionStyle'),
  });
}

/** Detached command data; unrelated preference keys never cross the bridge. */
export function captionStyle(preferences: SubtitlePreferences): CaptionStyle {
  const {size, color, background, position, font, keepStyling, useSystemCaptionStyle} = preferences;
  return Object.freeze({size, scale: scales[size], color, background, position, font, keepStyling, useSystemCaptionStyle});
}

/** The drawn caption palette, the same values web's player and Settings preview use (web kit/media/caption-style):
 * the preview paints exactly what the player will. Media colours are independent of the app theme. */
const paintColors: Readonly<Record<SubtitlePreferences['color'], string>> = {white: '#fff', yellow: '#ff0', cyan: '#0ff', green: '#0f0', pink: '#ff80c0'};
const paintBackgrounds: Readonly<Record<SubtitlePreferences['background'], string>> = {none: 'transparent', outline: 'transparent', shadow: 'transparent', translucent: 'rgba(0,0,0,0.65)', opaque: '#000'};
const oneOf = <T extends string>(values: readonly T[], value: unknown, fallback: T): T => values.includes(value as T) ? value as T : fallback;
export type CaptionPaint = Readonly<{scale: number; color: string; backgroundColor: string; edge: 'none' | 'outline' | 'shadow'; position: SubtitlePreferences['position']}>;
/** What the Settings preview draws from the effective preferences; an unknown value falls back to the registry default. */
export function captionPaint(values: PreferenceValues): CaptionPaint {
  const size = oneOf(['small', 'medium', 'large', 'extra-large'] as const, values['playback.subtitleSize'], 'medium');
  const color = oneOf(['white', 'yellow', 'cyan', 'green', 'pink'] as const, values['playback.subtitleColor'], 'white');
  const background = oneOf(['none', 'outline', 'shadow', 'translucent', 'opaque'] as const, values['playback.subtitleBackground'], 'translucent');
  return Object.freeze({scale: scales[size], color: paintColors[color], backgroundColor: paintBackgrounds[background],
    edge: background === 'outline' ? 'outline' as const : background === 'shadow' ? 'shadow' as const : 'none' as const,
    position: oneOf(['bottom', 'raised', 'top'] as const, values['playback.subtitlePosition'], 'bottom')});
}
