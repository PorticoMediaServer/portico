import {defaultI18n, type I18n} from '../../../../../packages/i18n/src';
import {episodeLabel, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {GuideProgram} from '../../../../../packages/client-core/src/guide/types';

/** UI language and number/date format locale may differ. Preserve the actual
 * resolved auto-hour policy, including undefined; old runtimes keep their
 * existing fallback only when they have no regional formatter port. */
export function nativeGuideRegion(text: Pick<TextEnvironment, 'locale'> & Readonly<{region?: I18n['region']}>): Readonly<{locale: string; hour12: boolean | undefined}> {
  const region = text.region;
  return region ? {locale: region.formatLocale, hour12: region.hour12}
    : {locale: text.locale ?? defaultI18n.region.formatLocale, hour12: defaultI18n.region.hour12};
}

/** Source69 TV guide clocks omit the day period in the ruler/cells. Sheets retain the full
 * canonical programme range. Only format actual instants, in the device's genuine zone. */
export function nativeGuideClock(locale: string, timeZone: string, hour12?: boolean, omitDayPeriod = true): (at: number) => string | undefined {
  let formatter: Intl.DateTimeFormat;
  try {formatter = new Intl.DateTimeFormat(locale, {hour: 'numeric', minute: '2-digit', timeZone, ...(hour12 === undefined ? {} : {hour12})});}
  catch {return () => undefined;}
  return at => Number.isFinite(at) && Number.isFinite(new Date(at).getTime())
    ? formatter.formatToParts(at).filter(part => !omitDayPeriod || part.type !== 'dayPeriod').map(part => part.value).join('').replace(/[\u00a0\u202f]/g, ' ').trim()
    : undefined;
}
/** Separate facts keep the drawn spacing and the stored episode subtitle intact. */
export function nativeProgrammeCaption(programme: GuideProgram | undefined, onNow: boolean, isTV: boolean, clock: (at: number) => string | undefined, text: TextEnvironment & Pick<I18n, 't'>): readonly string[] {
  if (!programme) return [];
  const episode = programme.episode?.number === undefined ? undefined : episodeLabel({mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number}, 'ordinary', text)?.text;
  if (episode || programme.subtitle) return [episode, programme.subtitle].filter((value): value is string => !!value);
  const start = clock(programme.start), end = clock(programme.end);
  if (!end) return [];
  return !isTV && onNow ? [text.t('guide.ends', {time: end})] : start ? [text.t('guide.range', {start, end})] : [];
}

/** The next-programme line uses the same resolved Guide clock as its row. */
export function nativeGuideNext(programme: GuideProgram | undefined, clock: (at: number) => string | undefined, text: Pick<I18n, 't'>): string | undefined {
  if (!programme) return;
  const time = clock(programme.start);
  return time ? text.t('guide.next', {title: programme.title, time}) : undefined;
}

/** Date labels share the Guide's resolved format locale and genuine timezone. */
export function nativeGuideDate(locale: string, timeZone: string, options: Pick<Intl.DateTimeFormatOptions, 'weekday' | 'month' | 'day'>): (at: number) => string {
  let formatter: Intl.DateTimeFormat;
  try {formatter = new Intl.DateTimeFormat(locale, {...options, timeZone});}
  catch {return () => '';}
  return at => Number.isFinite(at) && Number.isFinite(new Date(at).getTime()) ? formatter.format(at) : '';
}

export type NativeChannelMenuEntry = Readonly<{id: 'favorite' | 'hidden' | 'edit'; label: string; icon: 'i-star' | 'i-star-f' | 'i-eye' | 'i-eyeoff' | 'i-edit'}>;
/** The guide's channel menu (long press on phone, hold OK on TV): Add to Favorites for everyone;
 * an owner may also hide a channel for everyone, or show one they hid. */
export function nativeChannelMenuEntries(channel: Readonly<{favorite: boolean; kind?: string}>, hidden: boolean, owner: boolean, t: Pick<I18n, 't'>['t'], isTV = false): readonly NativeChannelMenuEntry[] {
  return [
    {id: 'favorite', label: t(channel.favorite ? 'channels.removeFavorite' : 'channels.addFavorite'), icon: channel.favorite ? 'i-star-f' : 'i-star'},
    ...(owner && channel.kind !== 'library' ? [{id: 'hidden' as const, label: t(hidden ? 'channels.unhide' : 'channels.hide'), icon: hidden ? 'i-eye' as const : 'i-eyeoff' as const}] : []),
    ...(owner && channel.kind === 'library' && !isTV ? [{id: 'edit' as const, label: t('channels.editChannel'), icon: 'i-edit' as const}] : []),
  ];
}

/** Phone-only Library Channel editing uses the existing Settings stack. */
export const nativeLibraryChannelEditorRoute = (channelId: string) => ({kind: 'server-settings', page: 'live', channelId} as const);
