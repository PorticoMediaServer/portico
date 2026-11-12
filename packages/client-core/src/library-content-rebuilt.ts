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

/**
 * The pages the rebuilt server answers besides Discover. Each page is its own title and a list of
 * sections, each with its own read. A section that pages hands out a cursor
 * naming it (`sectionCursor`), so its next page reaches its own read; a fixed rail is a section
 * that does not page. A section whose read the server does not have yet is not in the list, so
 * the page draws without it: when the read lands, it is one more entry here.
 */
type HolderSection = Readonly<{id: string; type: ContentSection['type']; pages: boolean;
  query: (entityId: string) => Record<string, string>; /** A fixed rail's own length. */ limit?: number}>;
type Holder = Readonly<{sections: readonly HolderSection[]}>;
const holders: Readonly<Record<string, Holder>> = {
  album: {sections: [{id: 'tracks', type: 'list', pages: true, query: id => ({parent: id, sort: 'order'})}]},
  // An artist's releases, newest first; its ten most played songs (none played: an empty rail, so no row);
  // and every song crediting the artist, by name.
  artist: {sections: [
    {id: 'releases', type: 'grid', pages: true, query: id => ({parent: id, kind: 'album', sort: 'year', order: 'desc'})},
    {id: 'popularTracks', type: 'list', pages: false, limit: 10, query: id => ({artist: id, kind: 'track', sort: 'popular'})},
    {id: 'songs', type: 'list', pages: true, query: id => ({artist: id, kind: 'track', sort: 'title', order: 'asc'})},
  ]},
  collection: {sections: [{id: 'items', type: 'grid', pages: true, query: id => ({collection: id})}]},
};

/** The views the rebuilt server answers. */
export const rebuiltContentViews: ReadonlySet<string> = new Set(['discover', ...Object.keys(holders)]);

/** A section's cursor names its section, so its next page reaches its own read. */
const sectionCursor = (section: string, cursor: string): string => cursor ? `${section}~${cursor}` : '';
function readCursor(cursor: string | null | undefined): Readonly<{section: string; cursor: string}> | undefined {
  if (!cursor) return undefined;
  const at = cursor.indexOf('~');
  return at > 0 ? {section: cursor.slice(0, at), cursor: cursor.slice(at + 1)} : undefined;
}

/** The orders a holder's page offers: a collection its own order or by title, as before. */
const holderSorts: Readonly<Record<string, ContentProjection['sorts']>> = {
  collection: Object.freeze([Object.freeze({id: 'custom', labelKey: 'sort.custom', directions: Object.freeze(['asc'] as const)}),
    Object.freeze({id: 'title', labelKey: 'sort.title', directions: Object.freeze(['asc', 'desc'] as const)})]),
};

/** One page of a view the rebuilt server answers. `cursor` is a section's next page, from that section's `nextCursor`. */
export function rebuiltContent(api: ServerRequester, route: ContentRoute, bound: ContentScope, pageSize: number, signal?: AbortSignal, cursor?: string | null): Promise<ContentProjection> {
  if (route.view === 'discover') return discoverFromLanding(api, route, bound, pageSize, signal);
  if (holders[route.view]) return holderPage(api, route, bound, Math.min(pageSize, 200), signal, {next: readCursor(cursor)});
  throw new Error('This view has no rebuilt read.');
}

/** A window of a holder's grid, opened at a position (`start`), with the whole list's count: the native collection grid's reads. */
export function rebuiltContentWindow(api: ServerRequester, route: ContentRoute, bound: ContentScope, start: number, count: number, signal?: AbortSignal): Promise<ContentProjection> {
  if (!holders[route.view]) throw new Error('This view has no rebuilt read.');
  return holderPage(api, route, bound, count, signal, {at: start, window: true});
}

async function holderPage(api: ServerRequester, route: ContentRoute, bound: ContentScope, limit: number, signal: AbortSignal | undefined,
  page: Readonly<{next?: Readonly<{section: string; cursor: string}>; at?: number; window?: boolean}>): Promise<ContentProjection> {
  const holder = holders[route.view]!, id = route.entityId ?? '';
  const sorts = holderSorts[route.view] ?? Object.freeze([]);
  const sort = sorts.some(offered => offered.id === route.sort) ? route.sort! : sorts[0]?.id ?? '', direction = route.direction ?? 'asc';
  // `custom` is the holder's own order, the server's default for it.
  const order = sort && sort !== 'custom' ? {sort, order: direction} : {};
  // A later page reads only its own section; a window reads the page's first section, as a grid.
  const wanted = page.next ? holder.sections.filter(section => section.id === page.next!.section) : page.window ? holder.sections.slice(0, 1) : holder.sections;
  if (page.next && !wanted.length) throw new Error('This cursor names no section of the page.');
  const [title, library, ...lists] = await Promise.all([
    callServer(api, 'GET /v1/items/{id}', {id, signal}) as Promise<Item>,
    kindOf(api, route.libraryId, signal),
    // A fixed rail (an artist's Popular) is optional: when it cannot be read, the page draws without it.
    ...wanted.map(section => {
      const read = callServer(api, 'GET /v1/items', {query: {...section.query(id), ...order, limit: section.limit ?? limit,
        ...(page.next ? {cursor: page.next.cursor} : {at: page.at || undefined}), total: true}, signal}) as Promise<ItemPage>;
      return section.pages ? read : read.catch(() => undefined);
    }),
  ]);
  // The page's own title never plays from its header. An album names its artist (its parent), for the "by" link.
  const found = title ? entryFromItem(title, route.libraryId) : undefined;
  const artist = route.view === 'album' && title?.parent?.kind === 'artist' && title.parent.id ? {artist: Object.freeze({id: title.parent.id, name: title.parent.title})} : {};
  const entity = found ? (({playback: _ignored, ...rest}) => Object.freeze({...rest, ...artist}))(found as Record<string, unknown> & {playback?: unknown}) as typeof found : undefined;
  const start = page.next ? undefined : page.at ?? 0;
  const sections = wanted.flatMap((holderSection, n) => {
    const list = lists[n];
    // A fixed rail with nothing in it is not a section, as the server leaves out an empty row.
    if (!holderSection.pages && !(Array.isArray(list?.items) && list.items.length)) return [];
    const entries = firstOfEach('content', 'entries', entriesFromItems(Array.isArray(list?.items) ? list.items : [], route.libraryId), entry => entry.id);
    // A window's grid is always the section `items` (libraryContentPages reads that one). A
    // later page that comes back empty is still its section, with no next page: the titles
    // already shown stay.
    const sectionId = page.window ? 'items' : holderSection.id;
    return Object.freeze({id: sectionId, type: page.window ? 'grid' : holderSection.type, heading: Object.freeze({key: `library.content.${holderSection.id}`, fallback: ''}),
      entries: Object.freeze(entries), totalCount: Math.max(typeof list?.total === 'number' ? list.total : 0, entries.length),
      nextCursor: holderSection.pages ? sectionCursor(sectionId, list?.nextCursor ?? '') : '',
      ...(start === undefined ? {} : {start})}) as ContentSection;
  });
  return Object.freeze({
    heading: Object.freeze({key: `library.content.${route.view}`, fallback: typeof title?.title === 'string' ? title.title : ''}),
    ...(entity && entity.kind === route.view ? {entity} : {}),
    scope: Object.freeze({serverId: bound.serverId, libraryId: route.libraryId, libraryKind: library.kind, view: route.view, entityId: id}),
    navigation: Object.freeze([]),
    query: Object.freeze({sort, direction: sort ? direction : '', category: '', q: '', limit, searchMode: 'none'}),
    sorts, filters: Object.freeze([]), sections: Object.freeze(sections),
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
