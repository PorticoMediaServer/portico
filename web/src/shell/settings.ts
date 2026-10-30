import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {preferenceValue, type PreferenceSnapshot} from '@core/preferences.ts';
import {regionPreferences, subtitlePreferences, type SubtitlePreferences} from '@core/preference-effects.ts';
import {musicAudioEffects, musicQueueDefaults} from '@core/music-preferences.ts';
import {audiobookPreferences} from '@core/audiobook-preferences.ts';
import type {SkipMode} from '../../../packages/player-core/src/index.ts';
import type {WebPlaybackSettings} from '../platform/playback';

export function shellPlaybackSkips(preferences: PreferenceSnapshot, audiobook: boolean): Readonly<{back: number; forward: number}> {
  if (audiobook) {
    const selected = audiobookPreferences(preferences);
    return {back: selected.skipBackMs / 1000, forward: selected.skipForwardMs / 1000};
  }
  return {back: preferenceValue<number>(preferences, 'playback.skipBackMs') / 1000,
    forward: preferenceValue<number>(preferences, 'playback.skipForwardMs') / 1000};
}

export function shellLanguage(preferences: PreferenceSnapshot) {
  return createI18n(resolveRegion(regionPreferences(preferences), deviceRegion()));
}
/** Omit defaultQuality: the real server applies home/away defaults to the actual request. */
export function shellPlaybackSettings(source: PreferenceSnapshot | (() => PreferenceSnapshot), browser: Pick<Navigator, 'userAgent' | 'maxTouchPoints'> = navigator): WebPlaybackSettings & Readonly<{subtitlePreferences: () => SubtitlePreferences}> {
  const preferences = () => typeof source === 'function' ? source() : source;
  return {
    videoSpeed: () => preferenceValue<number>(preferences(), 'playback.defaultSpeed'),
    audiobookSpeed: () => preferenceValue<number>(preferences(), 'audiobooks.defaultSpeed'),
    audiobookPreferences: () => audiobookPreferences(preferences()),
    skipModes: () => ({intro: preferenceValue<SkipMode>(preferences(), 'playback.introSkip'), credits: preferenceValue<SkipMode>(preferences(), 'playback.creditsSkip'), recap: preferenceValue<SkipMode>(preferences(), 'playback.recapSkip')}),
    audioEffects: () => musicAudioEffects(preferences()), musicDefaults: () => musicQueueDefaults(preferences()),
    lyricsEnabled: () => preferenceValue<boolean>(preferences(), 'playback.showSyncedLyrics') === true,
    subtitlePreferences: () => subtitlePreferences(preferences()),
    webVolume: () => !(/iPhone|iPad|iPod/.test(browser.userAgent) || /Macintosh/.test(browser.userAgent) && browser.maxTouchPoints > 1),
  };
}
