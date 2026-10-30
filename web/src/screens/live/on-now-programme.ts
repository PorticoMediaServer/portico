import {nowNext, type GuideProgram} from '@core/guide/index.ts';
import {programmeFactParts} from '@portico/client-core/kit-model';
import type {I18n} from '@i18n';
import type {LiveCardProps} from '../../kit/cards/FeatureCards';

/** Only actual on-air facts decorate the card. A future airing remains future. */
export function onNowProgramme(programs: readonly GuideProgram[], now: number, i18n: I18n, timezone: string): LiveCardProps['programme'] {
  const current = nowNext(programs, now), programme = current.now;
  if (!programme) return {title: i18n.t('guide.noInformation'), facts: []};
  const parts = programmeFactParts({startMs: programme.start, endMs: programme.end,
    episode: programme.episode ? {mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number} : undefined,
    subtitle: programme.subtitle}, {t: i18n.t, locale: i18n.region.formatLocale, timeZone: timezone});
  const episodic = parts.some(part => part.id === 'episode-label' || part.id === 'programme-subtitle');
  const remaining = programme.end - now;
  return {title: programme.title, facts: parts.filter(part => episodic ? part.id === 'episode-label' || part.id === 'programme-subtitle' : part.id === 'time-range'),
    progress: current.progress, remaining: remaining < 3_600_000 ? i18n.t('guide.minutesLeft', {count: Math.max(1, Math.round(remaining / 60_000))})
      : i18n.t('guide.timeLeft', {time: i18n.duration(Math.round(remaining / 60_000) * 60)}),
    live: programme.flags?.live, isNew: programme.flags?.new, premiere: programme.flags?.premiere, recording: programme.recording?.state === 'recording'};
}
