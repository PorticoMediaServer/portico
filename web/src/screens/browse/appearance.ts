import type {appearancePreferences} from '@core/preference-effects.ts';

/** The parent projects its confirmed snapshot through the checked core facade. */
export type ScreenAppearance = ReturnType<typeof appearancePreferences>;
export type BrowsePresentation = Readonly<{view: 'grid' | 'list'; cardWidth: number; backdrops: boolean; cardSize: 'small' | 'medium' | 'large'}>;

export function cardScale(preferences?: ScreenAppearance): number {
  return preferences ? preferences.cardSizePercent / 100 : 1;
}

/** An absent per-library override follows the live device default. */
export function browsePresentation(base: BrowsePresentation, preferences?: ScreenAppearance, libraryViewOverride?: 'grid' | 'list'): BrowsePresentation {
  return preferences ? {...base, view: libraryViewOverride ?? preferences.libraryView,
    cardWidth: base.cardWidth * cardScale(preferences), backdrops: preferences.showBackdrops} : base;
}
