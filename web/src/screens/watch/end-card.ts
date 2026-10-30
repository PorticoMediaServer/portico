import type {DetailProjection} from '@core/detail.ts';
import type {ArtworkSource} from '../../kit/artwork/types';
import type {WatchCollectionNext} from './collection-next';

/** Facts read from the next member's own retained detail projection. Absent when
 * the member's read has no real genre or allowed watchlist state. */
export type WatchNextFacts = Readonly<{
  genres?: readonly string[];
  myList?: Readonly<{listed: boolean; pending: boolean; set(value: boolean): Promise<void>}>;
}>;

/** desktop-player f4b: the end of a film. The retained title read supplies the viewer's own
 * rating permission, the rating itself and the server's related row. Nothing is inferred. */
export type WatchEndCard = Readonly<{
  title: string;
  /** The completed title's own year and real genres, as the phone card draws them. */
  year?: number;
  genres?: readonly string[];
  /** The title's own backdrop, as the drawn finished card's ambient layer. */
  backdrop?: ArtworkSource;
  related: readonly Readonly<{id: string; title: string; year?: number; poster?: ArtworkSource}>[];
  rating?: Readonly<{value: number | null; set(value: number | null): Promise<void>}>;
  /** The next member of the collection in its own order, with the facts its own detail read supplied. */
  collection?: WatchCollectionNext & WatchNextFacts;
  back: () => void | Promise<void>;
  openRelated: (id: string) => void | Promise<void>;
}>;

type RelatedEntry = Readonly<{id: string; title: string; year?: number; posterUrl?: string}>;

export function watchEndCard(data: DetailProjection | undefined, poster: (entry: RelatedEntry) => ArtworkSource | undefined,
  navigate: (itemId: string) => void | Promise<void>, setRating: (value: number | null) => Promise<void>,
  collection?: WatchCollectionNext & WatchNextFacts, backdrop?: (url: string) => ArtworkSource | undefined): WatchEndCard | undefined {
  if (!data || data.item.kind !== 'movie') return undefined;
  const seen = new Set<string>(), related: {id: string; title: string; year?: number; poster?: ArtworkSource}[] = [];
  for (const entry of data.related?.rows.flatMap(row => row.entries) ?? []) {
    if (seen.has(entry.id)) continue;
    seen.add(entry.id);
    const source = poster(entry);
    related.push({id: entry.id, title: entry.title, ...(entry.year === undefined ? {} : {year: entry.year}), ...(source ? {poster: source} : {})});
    if (related.length === 12) break;
  }
  const canRate = data.actions.some(action => action.enabled && action.id === 'rating'), genres = data.metadata.genres.map(genre => genre.name).filter(Boolean);
  const backdropArt = data.item.backdropUrl && backdrop ? backdrop(data.item.backdropUrl) : undefined;
  return Object.freeze({
    title: data.item.title,
    ...(typeof data.item.year === 'number' ? {year: data.item.year} : {}),
    ...(genres.length ? {genres: Object.freeze(genres)} : {}),
    ...(backdropArt ? {backdrop: backdropArt} : {}),
    related: Object.freeze(related.map(entry => Object.freeze(entry))),
    ...(canRate ? {rating: Object.freeze({value: data.personal.rating, set: setRating})} : {}),
    ...(collection ? {collection: Object.freeze({...collection})} : {}),
    back: () => navigate(data.item.id),
    openRelated: (id: string) => navigate(id),
  });
}
