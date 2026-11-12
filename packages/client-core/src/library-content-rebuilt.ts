/**
 * A library's content views from the rebuilt server, in the projection the screens already read
 * (`ContentProjection`). The rebuilt server has no content route: each view reads the route that
 * answers it, and this file turns the answer into the same sections, entries and scope.
 *
 * Discover is `GET /v1/libraries/{id}/landing`: the library's own Home rows, one rail each, in the
 * server's order. A row with nothing in it is not sent and so has no rail.
 *
 * An album, an artist and a collection are their own title (`GET /v1/items/{id}`, the page's
 * entity) and what they hold (`GET /v1/items`): an album's songs in disc and track order, an
 * artist's albums, a collection's titles. A section the rebuilt server has no read for (an
 * artist's popular songs, every song crediting them) is not sent, so the page leaves it out.
 */
import type {Home, Item, ItemPage, Library as WireLibrary} from '../../contracts/src/next/api.ts';
import type {ContentProjection, ContentRoute, ContentScope, ContentSection} from './library-content.ts';
import {entriesFromItems, entryFromItem, libraryKind} from './items.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {firstOfEach} from './tolerant.ts';

/** The library's kind, which the layouts read, in the app's words; "" (the default layout) when the
 * library cannot be read. That read never fails the page. */
async function kindOf(api: ServerRequester, libraryId: string, signal?: AbortSignal): Promise<{kind: string; name: string}> {
  try {
    const library = await callServer(api, 'GET /v1/libraries/{id}', {id: libraryId, signal}) as WireLibrary;
    return {kind: libraryKind(typeof library?.kind === 'string' ? library.kind : ''), name: typeof library?.name === 'string' ? library.name : ''};
  } catch (error) {
    if (signal?.aborted) throw error;
    return {kind: '', name: ''};
  }
}

/** A rail holds at most this many cards, as the old projection's generic rows did. */
const railLimit = 100;

/** The views the rebuilt server answers. */
export const rebuiltContentViews: ReadonlySet<string> = new Set(['discover', 'album', 'artist', 'collection']);

/** What a holder's page lists, and the one section it is drawn as. */
const holders: Readonly<Record<string, Readonly<{section: string; type: ContentSection['type']; query: (id: string) => Record<string, string>}>>> = {
  album: {section: 'tracks', type: 'list', query: id => ({parent: id, sort: 'order'})},
  artist: {section: 'releases', type: 'grid', query: id => ({parent: id, kind: 'album', sort: 'year', order: 'desc'})},
  collection: {section: 'items', type: 'grid', query: id => ({collection: id})},
};

/** The orders a holder's page offers: a collection its own order or by title, as before. */
const holderSorts: Readonly<Record<string, ContentProjection['sorts']>> = {
  collection: Object.freeze([Object.freeze({id: 'custom', labelKey: 'sort.custom', directions: Object.freeze(['asc'] as const)}),
    Object.freeze({id: 'title', labelKey: 'sort.title', directions: Object.freeze(['asc', 'desc'] as const)})]),
};

/** One page of a view the rebuilt server answers. `cursor` is a section's next page, from that section's `nextCursor`. */
export function rebuiltContent(api: ServerRequester, route: ContentRoute, bound: ContentScope, pageSize: number, signal?: AbortSignal, cursor?: string | null): Promise<ContentProjection> {
  if (route.view === 'discover') return discoverFromLanding(api, route, bound, pageSize, signal);
  if (holders[route.view]) return holderPage(api, route, bound, Math.min(pageSize, 200), signal, {cursor: cursor ?? undefined});
  throw new Error('This view has no rebuilt read.');
}

/** A window of a holder's grid, opened at a position (`start`), with the whole list's count: the native collection grid's reads. */
export function rebuiltContentWindow(api: ServerRequester, route: ContentRoute, bound: ContentScope, start: number, count: number, signal?: AbortSignal): Promise<ContentProjection> {
  if (!holders[route.view]) throw new Error('This view has no rebuilt read.');
  return holderPage(api, route, bound, count, signal, {at: start, window: true});
}

async function holderPage(api: ServerRequester, route: ContentRoute, bound: ContentScope, limit: number, signal: AbortSignal | undefined,
  page: Readonly<{cursor?: string; at?: number; window?: boolean}>): Promise<ContentProjection> {
  const holder = holders[route.view]!, id = route.entityId ?? '';
  const sorts = holderSorts[route.view] ?? Object.freeze([]);
  const sort = sorts.some(offered => offered.id === route.sort) ? route.sort! : sorts[0]?.id ?? '', direction = route.direction ?? 'asc';
  // `custom` is the holder's own order, the server's default for it.
  const order = sort && sort !== 'custom' ? {sort, order: direction} : {};
  const [title, list, library] = await Promise.all([
    callServer(api, 'GET /v1/items/{id}', {id, signal}) as Promise<Item>,
    callServer(api, 'GET /v1/items', {query: {...holder.query(id), ...order, limit, ...(page.cursor ? {cursor: page.cursor} : {at: page.at || undefined}), total: true}, signal}) as Promise<ItemPage>,
    kindOf(api, route.libraryId, signal),
  ]);
  // The page's own title never plays from its header. An album names its artist (its parent), for the "by" link.
  const found = title ? entryFromItem(title, route.libraryId) : undefined;
  const artist = route.view === 'album' && title?.parent?.kind === 'artist' && title.parent.id ? {artist: Object.freeze({id: title.parent.id, name: title.parent.title})} : {};
  const entity = found ? (({playback: _ignored, ...rest}) => Object.freeze({...rest, ...artist}))(found as Record<string, unknown> & {playback?: unknown}) as typeof found : undefined;
  const entries = firstOfEach('content', 'entries', entriesFromItems(Array.isArray(list?.items) ? list.items : [], route.libraryId), entry => entry.id);
  const start = page.cursor ? undefined : page.at ?? 0;
  // A window's grid is always the section `items` (libraryContentPages reads that one).
  const section: ContentSection = Object.freeze({id: page.window ? 'items' : holder.section, type: page.window ? 'grid' : holder.type, heading: Object.freeze({key: `library.content.${holder.section}`, fallback: ''}),
    entries: Object.freeze(entries), totalCount: Math.max(typeof list?.total === 'number' ? list.total : 0, entries.length), nextCursor: list?.nextCursor ?? '',
    ...(start === undefined ? {} : {start})});
  return Object.freeze({
    heading: Object.freeze({key: `library.content.${route.view}`, fallback: typeof title?.title === 'string' ? title.title : ''}),
    ...(entity && entity.kind === route.view ? {entity} : {}),
    scope: Object.freeze({serverId: bound.serverId, libraryId: route.libraryId, libraryKind: library.kind, view: route.view, entityId: id}),
    navigation: Object.freeze([]),
    query: Object.freeze({sort, direction: sort ? direction : '', category: '', q: '', limit, searchMode: 'none'}),
    sorts, filters: Object.freeze([]),
    sections: Object.freeze(entries.length || !page.cursor ? [section] : []),
  }) as ContentProjection;
}

async function discoverFromLanding(api: ServerRequester, route: ContentRoute, bound: ContentScope, pageSize: number, signal?: AbortSignal): Promise<ContentProjection> {
  // The landing answer does not say the library's kind, which the Discover layout reads (square
  // cards for music and audiobooks): it is read beside it.
  const [landing, library] = await Promise.all([
    callServer(api, 'GET /v1/libraries/{id}/landing', {id: route.libraryId, signal}) as Promise<Home>,
    kindOf(api, route.libraryId, signal),
  ]);
  const rows = Array.isArray(landing?.rows) ? landing.rows : [];
  const sections: ContentSection[] = [];
  for (const row of firstOfEach('content', 'landing.rows', rows.filter(row => typeof row?.id === 'string' && row.id && Array.isArray(row.items)), row => row.id)) {
    const entries = firstOfEach('content', 'entries', entriesFromItems(row.items, route.libraryId), entry => entry.id).slice(0, railLimit);
    if (!entries.length) continue;
    // An audiobook library's Continue is the screen's resume shelf, which it knows by this id.
    const id = row.id === 'continue' && library.kind === 'audiobook' ? 'continue_listening' : row.id;
    // A heading code the app does not ship shows the server's own title as written.
    sections.push(Object.freeze({id, type: 'rail', heading: Object.freeze({key: `library.landing.${row.id}`, fallback: typeof row.title === 'string' ? row.title : ''}),
      entries: Object.freeze(entries), totalCount: entries.length, nextCursor: ''}));
  }
  return Object.freeze({
    heading: Object.freeze({key: 'library.tab.discover', fallback: library.name}),
    scope: Object.freeze({serverId: bound.serverId, libraryId: route.libraryId, libraryKind: library.kind, view: 'discover', entityId: ''}),
    navigation: Object.freeze([]),
    query: Object.freeze({sort: '', direction: '', category: '', q: '', limit: pageSize, searchMode: 'none'}),
    sorts: Object.freeze([]), filters: Object.freeze([]), sections: Object.freeze(sections),
  }) as ContentProjection;
}
