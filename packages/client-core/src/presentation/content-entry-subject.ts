import type {ContentEntry} from '../library-content.ts';
import type {CardSubject} from './kit-model/facts.ts';

/** Fields stay semantic. In particular, the old joined subtitle is never parsed. */
export function contentEntrySubject(entry: ContentEntry): CardSubject {
  if (entry.kind === 'radio') return {id: entry.id, kind: 'radio', title: entry.displayTitle ?? entry.title};
  const episode = entry.kind !== 'episode' ? undefined : entry.dateBased === true
    ? {mode: 'date' as const, day: entry.airDate}
    : entry.absoluteNumber !== undefined
    ? {mode: 'absolute' as const, number: entry.absoluteNumber}
    : entry.airDate && entry.episodeNumber === undefined ? {mode: 'date' as const, day: entry.airDate}
    : entry.special === true || entry.seasonNumber === 0 ? {mode: 'special' as const}
    : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};
  return {id: entry.id, kind: entry.kind, title: entry.displayTitle ?? entry.title,
    seasonNumber: entry.seasonNumber, year: entry.year, contentRating: entry.contentRating, durationSeconds: entry.duration,
    positionSeconds: entry.progressSeconds, genres: entry.genres, seasonCount: entry.seasonCount,
    artist: entry.artist?.name, album: entry.album?.name, author: entry.author,
    show: entry.show ? {id: entry.show.id, title: entry.show.name} : undefined, episode};
}
