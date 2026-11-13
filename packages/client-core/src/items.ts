/**
 * Browse and personal state on the server, in the shapes the screens draw.
 *
 * Every read here answers or fails; nothing is "being prepared". A list is `GET /v1/items` with a
 * cursor, a title is `GET /v1/items/{id}`, and a person's own state on a title is one `PUT`: the
 * last tap wins. Screens never see the wire: they get a card (`ContentEntry`), a page of cards,
 * and "set watched".
 */
import type {Item, ItemState, Library as WireLibrary, Person, StateReply, StateRequest} from '../../contracts/src/next/api.ts';
import type {ContentEntry, ContentEntryKind, ContentView} from './library-content.ts';
import {callServer, serverPath, type ServerRequester} from './query/server-api.ts';
import {keepReadable} from './tolerant.ts';

export type {Item, ItemState} from '../../contracts/src/next/api.ts';

/** The app's own names for a library's kind; one it does not know is shown as a plain library. */
const libraryKinds: Readonly<Record<string, string>> = {movies: 'movie', shows: 'tv', music: 'music', audiobooks: 'audiobook', books: 'book', comics: 'comic'};
export const libraryKind = (kind: string): string => libraryKinds[kind] ?? kind;

const entryKinds: Readonly<Record<string, ContentEntryKind>> = {movie: 'movie', show: 'show', season: 'season', episode: 'episode', artist: 'artist', album: 'album', track: 'song',
  audiobook: 'book', book: 'ebook', comic: 'comic', issue: 'comic', series: 'book_series', collection: 'collection'};
/** The item kind the server lists for one of the app's card kinds. */
export const itemKindOf = (kind: string): string => Object.entries(entryKinds).find(([, entry]) => entry === kind)?.[0] ?? kind;
/** The page a card that holds other titles opens; a title that plays opens its own page. */
const holderViews: Readonly<Record<string, ContentView>> = {show: 'show', season: 'season', artist: 'artist', album: 'album', audiobook: 'book', series: 'book_series', collection: 'collection'};

/** A half-star rating out of five, as the screens show it; the server keeps it out of ten. */
const stars = (rating: number | undefined): number | undefined => rating === undefined ? undefined : rating / 2;

/** The card for one item. A kind this app does not know has no card. `libraryId` is the list's library when the item does not say. */
export function entryFromItem(item: Item, libraryId?: string): ContentEntry | undefined {
  const kind = entryKinds[item.kind];
  if (!kind || !item.id) return undefined;
  const state = item.state, counts = item.counts, view = holderViews[item.kind], library = item.libraryId ?? libraryId;
  const picture = item.images?.poster ?? item.images?.cover ?? item.images?.thumb;
  const numbered = item.kind === 'episode' ? {episodeNumber: item.index, seasonNumber: item.parentIndex}
    : item.kind === 'season' ? {seasonNumber: item.index}
    : item.kind === 'track' ? {trackNumber: item.index, discNumber: item.parentIndex} : {};
  const entry: Record<string, unknown> = {
    id: item.id, kind, title: item.title, libraryId: library, posterUrl: picture, backdropUrl: item.images?.backdrop,
    stillUrl: item.kind === 'episode' ? item.images?.thumb : undefined,
    overview: item.summary, year: item.year, releaseDate: item.releaseDate, airDate: item.kind === 'episode' ? item.releaseDate : undefined,
    contentRating: item.contentRating, addedAt: item.addedAt, rating: item.ratings?.community, userRating: stars(state?.rating),
    duration: item.durationMs === undefined ? undefined : Math.round(item.durationMs / 1000),
    progressSeconds: state?.positionMs ? Math.round(state.positionMs / 1000) : undefined,
    watched: view ? (counts ? counts.leaves > 0 && counts.unwatchedLeaves === 0 : undefined) : state?.watched,
    favorite: state?.favorite, watchlisted: state?.inList,
    plays: item.kind === 'track' && state?.playCount ? state.playCount : undefined,
    count: counts?.leaves, watchedCount: counts ? counts.leaves - counts.unwatchedLeaves : undefined, unwatchedCount: counts?.unwatchedLeaves,
    seasonCount: item.kind === 'show' ? counts?.children : undefined,
    available: item.available, missing: item.available === false ? true : undefined,
    genres: item.tags?.filter(tag => tag.kind === 'genre').map(tag => tag.name),
    // An episode names its show: from the title's own page (root), or on a list from rootTitle beside rootId; a show the
    // profile may not see is named by neither.
    show: item.kind === 'episode' ? item.root ? {id: item.root.id, name: item.root.title} : item.rootId && item.rootTitle ? {id: item.rootId, name: item.rootTitle} : undefined : undefined,
    ...numbered,
    navigation: {view: view ?? 'item', entityId: item.id},
    playback: view ? undefined : {itemId: item.id, ...(state?.positionMs ? {startSeconds: Math.round(state.positionMs / 1000)} : {})},
  };
  for (const key of Object.keys(entry)) if (entry[key] === undefined || entry[key] === null) delete entry[key];
  return Object.freeze(entry) as ContentEntry;
}
/** The card for a person listed in a library (an author), opening their page there. */
export function entryFromPerson(person: Person, libraryId: string, kind: 'author'): ContentEntry | undefined {
  if (!person?.id || typeof person.name !== 'string') return undefined;
  // A person's picture is their portrait.
  const picture = person.images?.portrait ?? person.images?.thumb;
  return Object.freeze({id: person.id, kind, title: person.name, libraryId, navigation: Object.freeze({view: kind, entityId: person.id}),
    ...(picture ? {posterUrl: picture} : {}), ...(person.biography ? {overview: person.biography} : {}),
    ...(typeof person.titleCount === 'number' ? {count: person.titleCount} : {})}) as ContentEntry;
}
/** The card for one item in the server's wire units (durations and positions in milliseconds), for the readers that
 * check a card the way they check the old server's documents. */
export function wireEntryFromItem(item: Item, libraryId?: string): Record<string, unknown> | undefined {
  const entry = entryFromItem(item, libraryId) as Record<string, unknown> | undefined;
  if (!entry) return undefined;
  const {duration, progressSeconds, playback, ...rest} = entry as {duration?: number; progressSeconds?: number; playback?: {itemId: string; startSeconds?: number}} & Record<string, unknown>;
  const ms = (seconds: number) => Math.round(seconds * 1000);
  return {...rest, ...(duration === undefined ? {} : {durationMs: ms(duration)}), ...(progressSeconds === undefined ? {} : {progressMs: ms(progressSeconds)}),
    ...(playback ? {playback: {itemId: playback.itemId, ...(playback.startSeconds === undefined ? {} : {startMs: ms(playback.startSeconds)})}} : {})};
}
/** The cards of a list; an item of an unknown kind is left out alone. */
export const entriesFromItems = (items: readonly Item[], libraryId?: string): ContentEntry[] =>
  keepReadable('items', 'items', [...items], item => entryFromItem(item as Item, libraryId));

/** What `GET /v1/items` lists: one of a library, a holder, a person, a tag, favourites or My List; then filters and an order. */
export type ItemListQuery = Readonly<{
  library?: string; parent?: string; person?: string; artist?: string; tag?: string; kind?: string;
  genre?: string; year?: string; resolution?: string; watched?: boolean; inProgress?: boolean; favorite?: boolean; inList?: boolean;
  sort?: string; order?: 'asc' | 'desc';
  author?: string; series?: string;
}>;
export type ItemsPage = Readonly<{items: readonly Item[]; nextCursor?: string; total?: number}>;

export const readLibrary = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<WireLibrary> => callServer(api, 'GET /v1/libraries/{id}', {id, signal});

/** One page, from a cursor or opened at a position (`at`, counting from 0). A page never asks for the count: that is `countItems`. */
export function readItems(api: ServerRequester, query: ItemListQuery, page: Readonly<{cursor?: string; at?: number; limit?: number; total?: boolean}> = {}, signal?: AbortSignal): Promise<ItemsPage> {
  return callServer(api, 'GET /v1/items', {query: {...query, cursor: page.cursor, at: page.cursor ? undefined : page.at || undefined, limit: page.limit, total: page.total || undefined}, signal});
}
/** How many titles the list holds. Its own request, because a count takes longer than a page as a list grows. */
export async function countItems(api: ServerRequester, query: ItemListQuery, signal?: AbortSignal): Promise<number | undefined> {
  return (await readItems(api, query, {limit: 1, total: true}, signal)).total ?? undefined;
}
/** Every item of a short list (a show's seasons, an album's tracks): its pages read one after another, up to `most`. */
export async function readAllItems(api: ServerRequester, query: ItemListQuery, signal?: AbortSignal, most = 2000): Promise<Item[]> {
  const out: Item[] = [];
  for (let cursor: string | undefined; out.length < most;) {
    const page = await readItems(api, query, {cursor, limit: 200}, signal);
    out.push(...page.items);
    if (!page.nextCursor || !page.items.length) break;
    cursor = page.nextCursor;
  }
  return out;
}
export const readItem = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<Item> => callServer(api, 'GET /v1/items/{id}', {id, signal});
export const readItemCredits = (api: ServerRequester, id: string, page: Readonly<{cursor?: string; limit?: number}> = {}, signal?: AbortSignal) =>
  callServer(api, 'GET /v1/items/{id}/credits', {id, query: page, signal});
export const readRelatedItems = (api: ServerRequester, id: string, signal?: AbortSignal) => callServer(api, 'GET /v1/items/{id}/related', {id, signal});
export const readPersonFacts = (api: ServerRequester, id: string, signal?: AbortSignal) => callServer(api, 'GET /v1/people/{id}', {id, signal});
/** The choices of one filter over a list, each with how many of the list's titles it would leave. */
export const readFacet = (api: ServerRequester, facet: string, query: ItemListQuery, signal?: AbortSignal) => callServer(api, 'GET /v1/facets', {query: {...query, sort: undefined, order: undefined, facet}, signal});
export const readHomeRows = (api: ServerRequester, signal?: AbortSignal) => callServer(api, 'GET /v1/home', {signal});

/** What a person can change about a title for themselves. A rating is in half stars out of five, or `null` for none. */
/** `notInterested` takes the title out of recommendation rows and hides nothing else; `continueDismissed` takes it off
 * Continue Watching until it is played again (false puts it back). */
export type ItemStateChange = Readonly<{watched?: boolean; favorite?: boolean; inList?: boolean; hidden?: boolean; rating?: number | null; positionMs?: number;
  notInterested?: boolean; continueDismissed?: boolean}>;
/** The title's state after a change. `undo` is offered for a short time after changing everything under a show, season or album. */
export type ItemStateAnswer = Readonly<{state: ItemState; undo?: Readonly<{token: string; expiresAt: string}>}>;

export const itemStatePath = (id: string): string => serverPath('PUT /v1/items/{id}/state', {id});
export function itemStateBody(change: ItemStateChange): StateRequest {
  const {rating, ...rest} = change;
  return {...rest, ...(rating === undefined ? {} : {rating: rating === null ? null : Math.round(rating * 2)})} as StateRequest;
}
export function itemStateAnswer(reply: StateReply): ItemStateAnswer {
  const {undo, ...state} = reply;
  return Object.freeze({state, ...(undo ? {undo} : {})});
}
/** Sets it now. A tap that should be kept while the server is out of reach goes through `HttpLocalApi.request`, which queues this route. */
export async function setItemState(api: ServerRequester, id: string, change: ItemStateChange, signal?: AbortSignal): Promise<ItemStateAnswer> {
  return itemStateAnswer(await callServer(api, 'PUT /v1/items/{id}/state', {id, body: itemStateBody(change), signal}));
}
/** Takes back a change to a whole show, season or album. After about 30 seconds, or a server restart, the server answers `undo_expired`. */
export const undoItemState = (api: ServerRequester, token: string, signal?: AbortSignal): Promise<void> =>
  callServer(api, 'POST /v1/items/state:undo', {body: {token}, signal}).then(() => undefined);
