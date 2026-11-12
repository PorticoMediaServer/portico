/**
 * A library's content views from the rebuilt server, in the projection the screens already read
 * (`ContentProjection`). The rebuilt server has no content route: each view reads the route that
 * answers it, and this file turns the answer into the same sections, entries and scope.
 *
 * Discover is `GET /v1/libraries/{id}/landing`: the library's own Home rows, one rail each, in the
 * server's order. A row with nothing in it is not sent and so has no rail.
 */
import type {Home, Library as WireLibrary} from '../../contracts/src/next/api.ts';
import type {ContentProjection, ContentRoute, ContentScope, ContentSection} from './library-content.ts';
import {entriesFromItems, libraryKind} from './items.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {firstOfEach} from './tolerant.ts';

/** A rail holds at most this many cards, as the old projection's generic rows did. */
const railLimit = 100;

/** The views the rebuilt server answers, and the read for each. */
export const rebuiltContentViews: ReadonlySet<string> = new Set(['discover']);

export function rebuiltContent(api: ServerRequester, route: ContentRoute, bound: ContentScope, pageSize: number, signal?: AbortSignal): Promise<ContentProjection> {
  if (route.view !== 'discover') throw new Error('This view has no rebuilt read.');
  return discoverFromLanding(api, route, bound, pageSize, signal);
}

async function discoverFromLanding(api: ServerRequester, route: ContentRoute, bound: ContentScope, pageSize: number, signal?: AbortSignal): Promise<ContentProjection> {
  // The landing answer does not say the library's kind, which the Discover layout reads (square
  // cards for music and audiobooks): it is read beside it.
  const [landing, library] = await Promise.all([
    callServer(api, 'GET /v1/libraries/{id}/landing', {id: route.libraryId, signal}) as Promise<Home>,
    callServer(api, 'GET /v1/libraries/{id}', {id: route.libraryId, signal}) as Promise<WireLibrary>,
  ]);
  const rows = Array.isArray(landing?.rows) ? landing.rows : [];
  const sections: ContentSection[] = [];
  for (const row of firstOfEach('content', 'landing.rows', rows.filter(row => typeof row?.id === 'string' && row.id && Array.isArray(row.items)), row => row.id)) {
    const entries = firstOfEach('content', 'entries', entriesFromItems(row.items, route.libraryId), entry => entry.id).slice(0, railLimit);
    if (!entries.length) continue;
    // A heading code the app does not ship shows the server's own title as written.
    sections.push(Object.freeze({id: row.id, type: 'rail', heading: Object.freeze({key: `library.landing.${row.id}`, fallback: typeof row.title === 'string' ? row.title : ''}),
      entries: Object.freeze(entries), totalCount: entries.length, nextCursor: ''}));
  }
  return Object.freeze({
    heading: Object.freeze({key: 'library.tab.discover', fallback: typeof library?.name === 'string' ? library.name : ''}),
    scope: Object.freeze({serverId: bound.serverId, libraryId: route.libraryId, libraryKind: libraryKind(typeof library?.kind === 'string' ? library.kind : ''), view: 'discover', entityId: ''}),
    navigation: Object.freeze([]),
    query: Object.freeze({sort: '', direction: '', category: '', q: '', limit: pageSize, searchMode: 'none'}),
    sorts: Object.freeze([]), filters: Object.freeze([]), sections: Object.freeze(sections),
  }) as ContentProjection;
}
