import {appearancePreferences} from '../../../../../packages/client-core/src/preference-effects';
import {preferenceValue, type PreferenceSnapshot} from '../../../../../packages/client-core/src/preferences';

/** A missing or newer vocabulary is unsupported, not an invented device value. */
export function nativeAppearanceMotion(snapshot: PreferenceSnapshot): 'follow' | 'reduce' | undefined {
  if (!snapshot.registry.fields.some(field => field.key === 'appearance.motion')) return;
  const value = preferenceValue(snapshot, 'appearance.motion');
  return value === 'follow' || value === 'reduce' ? value : undefined;
}

/** Ruling R46-TV-TEXT-SIZE: only the published enum is a confirmed preference. */
export function nativeAppearanceTextSize(snapshot: PreferenceSnapshot): 'standard' | 'large' | undefined {
  if (!snapshot.registry.fields.some(field => field.key === 'appearance.textSize')) return;
  const value = preferenceValue(snapshot, 'appearance.textSize');
  return value === 'standard' || value === 'large' ? value : undefined;
}

/** The three drawn Card size steps are 85, 100 and 125 percent; anything else is unsupported, not clamped. */
export function nativeAppearanceCardSizePercent(snapshot: PreferenceSnapshot): 85 | 100 | 125 | undefined {
  if (!snapshot.registry.fields.some(field => field.key === 'appearance.cardSizePercent')) return;
  const value = appearancePreferences(snapshot, false).cardSizePercent;
  return value === 85 || value === 100 || value === 125 ? value : undefined;
}

/** Web publishes `data-backdrops="off"`; native keeps the drawn picture off the same confirmed switch. */
export function nativeAppearanceShowBackdrops(snapshot: PreferenceSnapshot): boolean | undefined {
  if (!snapshot.registry.fields.some(field => field.key === 'appearance.showBackdrops')) return;
  const value = appearancePreferences(snapshot, false).showBackdrops;
  return typeof value === 'boolean' ? value : undefined;
}
