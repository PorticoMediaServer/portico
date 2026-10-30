import {settingsBinding, type SettingsBinding} from '@core/presentation/settings-bindings.ts';
import {settingsRowIndex, type SettingsRow} from '@core/presentation/settings-spec.ts';

/** Availability of actual MAIN consumers, not a storage/binding/default map.
 * Home/Browse/Saved consume the same confirmed appearance facade; Show owns
 * episode layout and bootstrap applies the system-motion intersection.
 * The Next episode trio is published by server/internal/httpapi/playback_v1_routes.go
 * as postPlay and drives the web queue's countdown/automatic advance through
 * packages/client-core/src/post-play.ts and up-next.ts. The two watched/started
 * percentages are read by server/internal/catalog/personal_activity.go when it
 * records a play, so they decide the watched and continue-watching state the
 * screens show. The audiobook controls use the current shell steps and the
 * listening/server resume path; stopAtSeasonEnd still has no engine. */
const preferenceEffects = new Set([
  'playback.autoplayNext', 'playback.upNextCountdownMs', 'playback.startNextDuringCredits',
  'playback.playedThresholdPercent', 'playback.startedThresholdPercent',
  'playback.defaultSpeed', 'playback.introSkip', 'playback.creditsSkip', 'playback.recapSkip',
  'playback.passoutProtection', 'playback.passoutAfterEpisodes', 'playback.skipBackMs', 'playback.skipForwardMs', 'audiobooks.defaultSpeed',
  'audiobooks.skipBackMs', 'audiobooks.skipForwardMs', 'audiobooks.resumeRewindMs',
  'accessibility.reducedMotion', 'appearance.motion', 'appearance.showBackdrops', 'appearance.cardSizePercent',
  'appearance.libraryView', 'appearance.episodesLayout', 'notifications.badges',
  // `shellLanguage` builds the shell's one I18n from regionPreferences, so words, clock,
  // formats, date order, week start and file sizes all have a real web consumer.
  'region.locale', 'region.hourCycle', 'region.formats', 'region.dateOrder', 'region.firstDay', 'region.sizeUnits',
  /* Account Audio & subtitles: the server applies the preferred languages and
     subtitle mode when it builds the playback session (delivery_context.go,
     subtitles/provider.go), and the web player applies the four appearance
     values to its own cue surface (preference-effects.ts -> subtitle-surface.ts). */
  'playback.preferredAudioLanguages', 'playback.preferredSubtitleLanguages', 'playback.subtitleMode',
  'playback.subtitleSize', 'playback.subtitleColor', 'playback.subtitleBackground', 'playback.subtitlePosition',
  // The server selects home/remote by reach and mobile by actual cellular transport.
  // These are saved defaults; a plain start supplies no client quality authority.
  'quality.home', 'quality.remote', 'quality.mobile',
  // The saved download default: the server reads it when a download asks for no
  // quality of its own (downloads/options.go -> requestedDownloadQuality).
  /* Account Privacy: the server applies all four in the profile's own session. personal_activity.go
     pauses watch history and decides what counts in Popular; recommendation_engine.go and
     rec_title.go gate activity sharing; social/privacy.go hides Watch Together membership; and
     catalog/search_history.go stops keeping recent searches. */
  'privacy.pauseWatchHistory', 'privacy.showActivityToMembers', 'privacy.includeInWatchTogether', 'search.rememberHistory',
  /* Account Music: the web audio engine reads these through music-preferences.ts and
     applies them in DecodeAudioRender (gapless, crossfade, smooth transitions, leveling
     and never-boost). music.qualityAway is the server's music delivery policy for the
     away network class, and music.dsdGainDb is the server's audio-conversion gain. */
  'music.audioNormalization', 'music.crossfadeMs', 'music.gapless',
  'music.smoothTransitions', 'music.neverBoost', 'music.qualityAway', 'music.dsdGainDb',
  'quality.downloads',
]);
function preferenceEffect(binding: SettingsBinding): boolean {
  if (preferenceEffects.has(binding.key) || binding.key.startsWith('subtitles.')) return true;
  const parts = Object.values(binding.fields ?? {});
  return parts.length > 0 && parts.every(key => preferenceEffects.has(key));
}
/** A composite/preset is offered only when every canonical target has a real
 * effect. Unsupported native outputs and Library eligibility remain separate. */
export function hasWebPreferenceEffect(row: SettingsRow, visited: ReadonlySet<string> = new Set()): boolean {
  if (visited.has(row.id)) return false;
  const next = new Set([...visited, row.id]);
  const presets = row.control.options?.filter(option => option.sets);
  if (presets?.length) {
    const targets = [...new Set(presets.flatMap(option => Object.keys(option.sets!)))];
    return targets.length > 0 && targets.every(id => {const target = settingsRowIndex.get(id)?.row; return !!target && hasWebPreferenceEffect(target, next);});
  }
  const binding = settingsBinding(row.id);
  return binding?.form === 'preferences' && preferenceEffect(binding);
}
