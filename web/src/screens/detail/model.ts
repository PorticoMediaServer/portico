import type {ContentEntry, ContentView} from '@core/library-content.ts';
import {holderViewOf} from '@core/items.ts';
import type {TitleFile} from '@core/detail.ts';
import {cardText, titleFactParts, type CardSubject, type TextEnvironment, type FileFacts, type TitleFacts, type FactPart} from '@core/presentation/kit-model';

/** A title with a page of its own in its library (the one its card opens: a show, a season, an artist, an album, an
 * audiobook, a book series, a collection) goes there when opened by its address, instead of drawing one title's page.
 * The kinds are client-core's own map, so an address and a click land on the same page. */
export function holderPage(item: Readonly<{id: string; kind: string}>, libraryId: string): Readonly<{libraryId: string; view: ContentView; entityId: string}> | undefined {
  const view = holderViewOf(item.kind);
  return view && libraryId ? {libraryId, view, entityId: item.id} : undefined;
}


/** Full title pages group genuine genres together, between length/seasons and the rating. */
export function titlePageFacts(facts: TitleFacts, env: TextEnvironment): readonly FactPart[] {
  const parts = titleFactParts({...facts, genres: undefined}, env), genres = (facts.genres ?? []).filter(value => value.trim());
  const group: FactPart[] = genres.length ? [{id: 'genres', role: 'supplement', text: genres.reduce((list, genre) => env.t('next.kit.genres.append', {list, genre}))}] : [];
  return [...parts.filter(part => part.id !== 'content-rating'), ...group, ...parts.filter(part => part.id === 'content-rating')];
}
/** A published calendar day is not shifted into the previous day by the viewer's zone. */
export function calendarDate(value: string | undefined, language: string): string | undefined {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined;
  const parsed = new Date(`${value}T12:00:00Z`);
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value ? new Intl.DateTimeFormat(language, {dateStyle: 'long', timeZone: 'UTC'}).format(parsed) : undefined;
}
export function instantDate(value: string | null | undefined, language: string): string | undefined {
  if (!value) return undefined;
  const parsed = new Date(value);
  return Number.isFinite(parsed.getTime()) ? new Intl.DateTimeFormat(language, {dateStyle: 'long'}).format(parsed) : undefined;
}

/** Same semantic TitleKind projection for Search, browse and details; no joined subtitle parsing. */
export function entrySubject(entry: ContentEntry): CardSubject {
  if (entry.kind === 'radio') return {id: entry.id, kind: 'radio', title: entry.displayTitle ?? entry.title};
  const episode = entry.kind !== 'episode' ? undefined : entry.dateBased === true
    ? {mode: 'date' as const, day: entry.airDate}
    : entry.absoluteNumber !== undefined
    ? {mode: 'absolute' as const, number: entry.absoluteNumber}
    : entry.airDate && entry.episodeNumber === undefined ? {mode: 'date' as const, day: entry.airDate}
    : entry.special === true || entry.seasonNumber === 0 ? {mode: 'special' as const}
    : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};
  return {id: entry.id, kind: entry.kind, title: entry.displayTitle ?? entry.title, year: entry.year, contentRating: entry.contentRating,
    durationSeconds: entry.duration, positionSeconds: entry.progressSeconds, genres: entry.genres, seasonCount: entry.seasonCount,
    artist: entry.artist?.name, album: entry.album?.name, author: entry.author,
    show: entry.show ? {id: entry.show.id, title: entry.show.name} : undefined, episode};
}
export function entryCard(entry: ContentEntry, env: TextEnvironment, context: 'search' | 'ordinary' | 'in-show' = 'ordinary') {
  return cardText(entrySubject(entry), context, env);
}
