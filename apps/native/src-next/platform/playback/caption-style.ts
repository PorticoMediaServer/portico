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
