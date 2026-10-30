import {appearancePreferences} from '@core/preference-effects.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {AmbientArtSource} from '../../kit/artwork';

/** Live presentation from the parent's canonical preference facade, never
 * another persisted view/key map. The parent owns writes and route history. */
export type SavedPresentation = Readonly<{
  view: 'grid' | 'list';
  cardWidth: number;
  backdrops: boolean;
  reducedMotion: boolean;
  backdrop?: AmbientArtSource;
  setView(view: 'grid' | 'list'): void;
}>;

export type SavedPresentationInput = Readonly<{
  /** The actual responsive frame baseline, before the viewer's card scaling. */
  baseCardWidth: number;
  systemReducedMotion: boolean;
  backdrop?: AmbientArtSource;
  isCurrent(): boolean;
  /** The parent writes through its captured canonical preferences owner. */
  setView(view: 'grid' | 'list'): void;
}>;

/** Recompute from the SAME live parent PreferencesReader snapshot. No view
 * cache, preference key map, invented default or optimistic mutation lives here. */
export function savedPresentation(snapshot: PreferenceSnapshot, input: SavedPresentationInput): SavedPresentation {
  if (!Number.isFinite(input.baseCardWidth) || input.baseCardWidth <= 0) throw new Error('Saved requires the actual responsive card width.');
  const appearance = appearancePreferences(snapshot, input.systemReducedMotion);
  return Object.freeze({
    view: appearance.libraryView,
    cardWidth: input.baseCardWidth * appearance.cardSizePercent / 100,
    backdrops: appearance.showBackdrops,
    reducedMotion: appearance.reducedMotion,
    ...(appearance.showBackdrops && input.backdrop ? {backdrop: input.backdrop} : {}),
    setView(view: 'grid' | 'list') { if (input.isCurrent()) input.setView(view); },
  });
}
