import {preferenceValue, validatePreferencePatch, type PreferencePatch, type PreferenceSnapshot, type PreferenceValue} from './preferences.ts';

/** Registry values remain the sole defaults. These projections contain only
 * drawn controls; renderer support is reported by the actual platform. */
export type SubtitlePreferences = Readonly<{
  size: 'small' | 'medium' | 'large' | 'extra-large';
  color: 'white' | 'yellow' | 'cyan' | 'green' | 'pink';
  background: 'none' | 'outline' | 'shadow' | 'translucent' | 'opaque';
  position: 'bottom' | 'raised' | 'top';
  font: 'default' | 'serif' | 'mono' | 'rounded';
  keepStyling: boolean; useSystemCaptionStyle: boolean;
}>;

export function subtitlePreferences(snapshot: PreferenceSnapshot): SubtitlePreferences {
  const read = <T extends string | boolean>(key: string) => preferenceValue<T>(snapshot, key);
  return Object.freeze({size: read<SubtitlePreferences['size']>('playback.subtitleSize'),
    color: read<SubtitlePreferences['color']>('playback.subtitleColor'),
    background: read<SubtitlePreferences['background']>('playback.subtitleBackground'),
    position: read<SubtitlePreferences['position']>('playback.subtitlePosition'),
    font: read<SubtitlePreferences['font']>('playback.subtitleFont'),
    keepStyling: read<boolean>('playback.keepSubtitleStyling'),
    useSystemCaptionStyle: read<boolean>('playback.useSystemCaptionStyle')});
}

export function audioSelectionPreferences(snapshot: PreferenceSnapshot) {
  return Object.freeze({audioLanguages: preferenceValue<readonly string[]>(snapshot, 'playback.preferredAudioLanguages'),
    subtitleLanguages: preferenceValue<readonly string[]>(snapshot, 'playback.preferredSubtitleLanguages'),
    subtitleMode: preferenceValue<'off' | 'foreign' | 'forced' | 'always'>(snapshot, 'playback.subtitleMode'),
    audioDescription: preferenceValue<'avoid' | 'prefer'>(snapshot, 'playback.audioDescription'),
    audioTrackChoice: preferenceValue<'best' | 'channels' | 'stereo'>(snapshot, 'playback.audioTrackChoice'),
    preferSdh: preferenceValue<boolean>(snapshot, 'playback.preferSdhSubtitles'),
    forcedFallback: preferenceValue<'none' | 'full'>(snapshot, 'playback.forcedSubtitleFallback'),
    useUntaggedForced: preferenceValue<boolean>(snapshot, 'playback.useUntaggedForced')});
}

export function appearancePreferences(snapshot: PreferenceSnapshot, systemReducedMotion: boolean) {
  return Object.freeze({reducedMotion: preferenceValue<'follow' | 'reduce'>(snapshot, 'appearance.motion') === 'reduce' || systemReducedMotion,
    showBackdrops: preferenceValue<boolean>(snapshot, 'appearance.showBackdrops'),
    cardSizePercent: preferenceValue<number>(snapshot, 'appearance.cardSizePercent'),
    textSize: preferenceValue<'standard' | 'large'>(snapshot, 'appearance.textSize'),
    layout: preferenceValue<'auto' | 'television' | 'touch'>(snapshot, 'appearance.layout'),
    libraryView: preferenceValue<'grid' | 'list'>(snapshot, 'appearance.libraryView'),
    episodesLayout: preferenceValue<'auto' | 'tiles' | 'list'>(snapshot, 'appearance.episodesLayout')});
}

export function regionPreferences(snapshot: PreferenceSnapshot) {
  return Object.freeze({locale: preferenceValue<string>(snapshot, 'region.locale'),
    hourCycle: preferenceValue<'auto' | 'h12' | 'h23'>(snapshot, 'region.hourCycle'),
    formats: preferenceValue<string>(snapshot, 'region.formats'),
    dateOrder: preferenceValue<'auto' | 'dmy' | 'mdy' | 'ymd'>(snapshot, 'region.dateOrder'),
    firstDay: preferenceValue<'auto' | 'mon' | 'sun' | 'sat'>(snapshot, 'region.firstDay'),
    sizeUnits: preferenceValue<'binary' | 'decimal'>(snapshot, 'region.sizeUnits')});
}

/** Before Phase 8 only Settings reads this projection. Reader-core effects and
 * per-book overrides belong to Phase 8; this facade stores no open-book state. */
export function readingPreferences(snapshot: PreferenceSnapshot) {
  return Object.freeze({theme: preferenceValue<'dark' | 'black' | 'sepia' | 'light'>(snapshot, 'reading.theme'),
    font: preferenceValue<'publisher' | 'literata' | 'manrope'>(snapshot, 'reading.font'),
    textSize: preferenceValue<number>(snapshot, 'reading.textSize'),
    lineSpacing: preferenceValue<'tight' | 'normal' | 'loose'>(snapshot, 'reading.lineSpacing'),
    margins: preferenceValue<'narrow' | 'normal' | 'wide'>(snapshot, 'reading.margins'),
    alignment: preferenceValue<'left' | 'justified'>(snapshot, 'reading.alignment'),
    layout: preferenceValue<'pages' | 'scroll'>(snapshot, 'reading.layout'),
    twoPages: preferenceValue<boolean>(snapshot, 'reading.twoPages'),
    tapToTurn: preferenceValue<boolean>(snapshot, 'reading.tapToTurn'),
    hyphenation: preferenceValue<boolean>(snapshot, 'reading.hyphenation'),
    highlightColour: preferenceValue<'yellow' | 'green' | 'blue' | 'pink'>(snapshot, 'reading.highlightColour'),
    pdfView: preferenceValue<'automatic' | 'spread' | 'width' | 'page'>(snapshot, 'reading.pdfView'),
    comicFit: preferenceValue<'auto' | 'width' | 'height'>(snapshot, 'comics.fit'),
    comicDirection: preferenceValue<'file' | 'ltr' | 'rtl' | 'vertical'>(snapshot, 'comics.direction'),
    firstPageAlone: preferenceValue<boolean>(snapshot, 'comics.firstPageAlone')});
}
export type ReadingPreferences = ReturnType<typeof readingPreferences>;

export type ReadingPreferencesChange = Partial<{[K in keyof ReadingPreferences]: ReadingPreferences[K] | null}>;

const readingPreferenceKeys: Readonly<Record<keyof ReadingPreferences, string>> = Object.freeze({
  theme: 'reading.theme', font: 'reading.font', textSize: 'reading.textSize',
  lineSpacing: 'reading.lineSpacing', margins: 'reading.margins', alignment: 'reading.alignment',
  layout: 'reading.layout', twoPages: 'reading.twoPages', tapToTurn: 'reading.tapToTurn',
  hyphenation: 'reading.hyphenation', highlightColour: 'reading.highlightColour', pdfView: 'reading.pdfView',
  comicFit: 'comics.fit', comicDirection: 'comics.direction', firstPageAlone: 'comics.firstPageAlone',
});

/** Pure Settings patch: only submitted facade properties are changed; null
 * clears that profile override. PreferencesReader owns transport and revision. */
export function readingPreferencesPatch(snapshot: PreferenceSnapshot, change: ReadingPreferencesChange): PreferencePatch {
  if (!change || typeof change !== 'object' || Array.isArray(change)) throw new Error('Invalid Reading preference change.');
  const patch: Record<string, PreferenceValue | null> = {};
  for (const property of Object.keys(change)) {
    if (!Object.prototype.hasOwnProperty.call(readingPreferenceKeys, property)) throw new Error(`Unknown Reading preference ${property}.`);
    const name = property as keyof ReadingPreferences;
    const value = change[name];
    if (value === undefined) throw new Error(`No value was supplied for the Reading preference ${property}.`);
    patch[readingPreferenceKeys[name]] = value;
  }
  return validatePreferencePatch(snapshot.registry, 'profile-server', patch);
}
