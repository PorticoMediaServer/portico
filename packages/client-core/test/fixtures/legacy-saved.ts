// Saved tests written against the old server's documents keep pinning the same screen behaviour on the rebuilt server:
// a request the app now makes (GET /v1/items?inList, /v1/lists, ...) is asked of the fixture as the old one
// (/v1/content?view=watchlist, /v1/playlists/{id}, ...), and the fixture's answer is turned into the rebuilt server's.
// Only test code uses this.
import {createHash} from 'node:crypto';
import {libraryItemQuery, libraryQueryControls} from '../../src/library-browse.ts';

type Request = (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<any>;
const itemKind: Readonly<Record<string, string>> = {song: 'track', book: 'audiobook', ebook: 'book', comic: 'book', audiobook_file: 'audiobook'};

/** One old card as a rebuilt server's title (with its entry id in a list). */
function item(entry: any, entryId?: string): Record<string, unknown> {
  const position = entry?.playback?.startMs ?? entry?.progressMs ?? (entry?.playback?.startSeconds ? entry.playback.startSeconds * 1000 : undefined);
  return {id: entry.id, kind: itemKind[entry.kind] ?? entry.kind, title: entry.displayTitle ?? entry.title ?? '', ...(entry.libraryId ? {libraryId: entry.libraryId} : {}),
    ...(entry.available === undefined ? {} : {available: entry.available}), ...(entryId ? {entryId} : {}),
    ...(position ? {state: {positionMs: position, watched: !!entry.watched, playCount: 0, favorite: !!entry.favorite, inList: false, notInterested: false}} : {}),
    ...(entry.posterUrl ? {images: {poster: entry.posterUrl}} : {})};
}
const entriesOf = (doc: any): any[] => (doc?.sections ?? []).flatMap((section: any) => section.entries ?? []);
const totalOf = (doc: any): number => (doc?.sections ?? []).reduce((sum: number, section: any) => sum + (section.totalCount ?? (section.entries ?? []).length), 0);
const cursorOf = (doc: any): string => (doc?.sections ?? []).map((section: any) => section.nextCursor).find(Boolean) ?? '';
/** An old collection or saved view as a rebuilt server's list: a view keeps its library and the item list's query. */
function personalListOf(resource: any): Record<string, unknown> {
  const view = resource.kind === 'view', d = resource.definition;
  let query: string | undefined;
  if (view && d) {
    try {
      const {library: _, ...list} = libraryItemQuery({id: d.libraryId}, d.pivot, d.query ? ('all' in d.query ? d.query.all : [d.query]) : [], d.sort ?? []) as Record<string, unknown>;
      query = new URLSearchParams(Object.entries(list).map(([key, value]) => [key, String(value)])).toString();
    } catch {query = 'unreadable=1';}
  }
  return {id: resource.id, kind: view ? 'saved_view' : 'collection', name: resource.name, description: resource.summary ?? '', owner: {id: 'owner', name: ''},
    // The rebuilt server decides what a viewer may do from their role: a list not offered to change is someone else's.
    role: resource.actions?.includes('update') || resource.actions?.includes('entries') ? resource.role : 'viewer', count: resource.entryCount ?? 0, everyone: resource.visibility === 'server', covers: (resource.artworkPaths ?? []).map((poster: string) => ({poster})),
    ...(view ? {library: d?.libraryId ?? '', query: query ?? 'unreadable=1', dropped: [...(resource.invalidComponents ?? [])]} : {}),
    revision: String(resource.revision ?? 0), createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z'};
}
const listOf = (resource: any) => ({id: resource.id, kind: 'playlist', name: resource.name, description: resource.summary ?? '', owner: {id: 'owner', name: ''},
  role: resource.role, count: resource.entryCount ?? 0, covers: [], revision: String(resource.revision ?? 0), createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z'});

/** `personal`: the test's lists are collections and saved views, read on the old personal resource routes; without it a
 * list is a playlist. Headers an edit carries (`If-Match`) arrive in `headers`. */
export type LegacySavedOptions = Readonly<{personal?: boolean}>;
/** A test transport that speaks the old Saved documents to its fixture and the rebuilt server's to the app. */
export function legacySavedRequest(request: Request, options: LegacySavedOptions = {}): Request {
  // The saved view last opened: its titles are the old page's, read again with its id.
  let openedView = '';
  return async (path, method = 'GET', body, signal, headers?: Readonly<Record<string, string>>) => {
    const url = new URL(path, 'https://legacy.invalid'), q = url.searchParams, at = url.pathname;
    const old = (oldPath: string, oldMethod = 'GET', oldBody?: unknown) => request(oldPath, oldMethod, oldBody, signal);
    const page = (cursor: string | null) => new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})});
    if (at === '/v1/lists' && method === 'GET' && (q.get('kind') === 'collection' || q.get('kind') === 'saved_view')) {
      const kind = q.get('kind') === 'saved_view' ? 'view' : 'collection';
      const doc = await old('/v1/saved-resources?' + new URLSearchParams({limit: '40', ...(q.get('cursor') ? {cursor: q.get('cursor')!} : {}), kind}));
      return {items: (doc?.resources ?? []).map(personalListOf), ...(doc?.nextCursor ? {nextCursor: doc.nextCursor} : {})};
    }
    if (at === '/v1/lists' && method === 'POST' && ['collection', 'saved_view'].includes((body as {kind?: string})?.kind ?? '')) {
      // The old create carried the operation and, for a view, its definition; the rebuilt one names its library and query.
      const made = body as {kind: string; name: string; description?: string; everyone?: boolean; library?: string; query?: string};
      const view = made.kind === 'saved_view', controls = view ? libraryQueryControls(made.query ?? '') : undefined;
      const definition = controls && {libraryId: made.library, pivot: controls.pivot, query: controls.nodes.length === 0 ? null : controls.nodes.length === 1 ? controls.nodes[0] : {variant: 'group', all: controls.nodes},
        sort: controls.sorts.length ? controls.sorts : [{field: 'title', direction: 'asc'}], presentation: 'grid'};
      const operationId = headers?.['Idempotency-Key'] || '00000000-0000-4000-8000-' + createHash('sha256').update(JSON.stringify(body)).digest('hex').slice(0, 12);
      const raw = await old('/v1/saved-resources', 'POST', {expectedRevision: 0, kind: view ? 'view' : 'collection', name: made.name, summary: made.description ?? '',
        ...(view ? {definition} : {visibility: made.everyone ? 'server' : 'private'}), operationId});
      return {id: raw?.receipt?.resourceId ?? 'created', kind: made.kind, name: made.name, owner: {id: 'owner', name: ''}, role: 'owner', count: 0, covers: [],
        revision: String(raw?.receipt?.revision ?? 1), createdAt: '', updatedAt: '', ...(view ? {library: made.library, query: made.query} : {})};
    }
    if (options.personal) {
      const one = /^\/v1\/lists\/([^/]+)(\/.*)?$/.exec(at), resourcePath = (id: string) => `/v1/saved-resources/${encodeURIComponent(id)}`;
      const content = (id: string, cursor: string | null) => old(`${resourcePath(id)}/content?` + page(cursor));
      const revision = Number(/"(\d+)"/.exec(headers?.['If-Match'] ?? '')?.[1] ?? NaN);
      const answered = async (id: string) => {const list = personalListOf((await content(id, null))?.resource); if (list.kind === 'saved_view') openedView = id; return list;};
      if (one && !one[2] && method === 'GET') return answered(decodeURIComponent(one[1]!));
      if (at === '/v1/items' && method === 'GET' && q.has('list')) {
        const doc = await content(q.get('list')!, q.get('cursor'));
        if (q.get('order') === 'desc' && q.get('limit') === '1') return {items: doc?.order?.last ? [{id: 'last', kind: 'movie', title: '', entryId: doc.order.last}] : []};
        return {items: (doc?.entries ?? []).filter((entry: any) => !entry.hidden).map((entry: any) => item(entry.media, entry.id)), total: doc?.totalCount ?? 0, ...(doc?.nextCursor ? {nextCursor: doc.nextCursor} : {})};
      }
      if (at === '/v1/items' && method === 'GET' && q.has('library')) {
        // A saved view's titles: the old page ran its query, so they are the opened view's page.
        const doc = await content(openedView, q.get('cursor'));
        return {items: (doc?.browse?.entries ?? []).map((entry: any) => item(entry)), total: doc?.browse?.pageInfo?.total ?? 0, ...(doc?.browse?.pageInfo?.nextCursor ? {nextCursor: doc.browse.pageInfo.nextCursor} : {})};
      }
      if (one && !one[2] && (method === 'PATCH' || method === 'DELETE')) {
        const id = decodeURIComponent(one[1]!), change = (body ?? {}) as {name?: string; description?: string; everyone?: boolean};
        // The rebuilt server's edit has no operation id; the old fixture's is the request's own, so a retry sends the same.
        const operationId = '00000000-0000-4000-8000-' + createHash('sha256').update(JSON.stringify([at, method, revision, body ?? null])).digest('hex').slice(0, 12);
        const oldBody = {expectedRevision: revision, operationId,
          ...(change.name === undefined ? {} : {name: change.name}), ...(change.description === undefined ? {} : {summary: change.description}),
          ...(change.everyone === undefined ? {} : {visibility: change.everyone ? 'server' : 'private'})};
        await old(resourcePath(id), method, oldBody);
        return method === 'DELETE' ? undefined : answered(id);
      }
    }
    if (at === '/v1/items' && method === 'GET' && (q.has('inList') || q.has('favorite')) && !q.has('list')) {
      // A counted read asks with limit=1, so a test that counts its page reads can leave these out.
      const view = q.has('inList') ? 'watchlist' : 'favorites', params = new URLSearchParams({view, limit: q.get('limit') === '1' ? '1' : '40'});
      const filter = q.get('watched') === 'false' ? 'unwatched' : q.get('watched') === 'true' ? 'watched' : q.get('inProgress') ? 'inProgress' : view === 'watchlist' && q.has('favorite') ? 'favorites' : '';
      if (filter) params.set('filter', filter);
      const kind = q.get('kind'); if (kind) params.set('shape', kind.startsWith('artist') ? 'squares' : 'posters');
      for (const [from, to] of [['sort', 'sort'], ['order', 'direction'], ['cursor', 'cursor']] as const) if (q.get(from)) params.set(to, q.get(from)!);
      const doc = await old('/v1/content?' + params);
      if (q.get('limit') === '1') {
        // A counted read: the old page's own counts (by shape, or by filter), else its total.
        const shape = params.get('shape') as 'posters' | 'squares' | null, option = (doc?.filters?.[0]?.options ?? []).find((row: any) => row.id === (filter || 'all'));
        return {items: [], total: shape && doc?.shapeCounts ? doc.shapeCounts[shape] : option?.count ?? totalOf(doc)};
      }
      if (!Array.isArray(doc?.sections)) return doc; // an unreadable page stays unreadable
      return {items: entriesOf(doc).filter((entry: any) => entry.kind !== 'reading_group').map(entry => item(entry)), total: totalOf(doc), ...(cursorOf(doc) ? {nextCursor: cursorOf(doc)} : {})};
    }
    if (at === '/v1/items' && method === 'GET' && q.has('list')) {
      const id = q.get('list')!, params = new URLSearchParams({limit: '40'});
      if (q.get('cursor')) params.set('cursor', q.get('cursor')!);
      const doc = await old(`/v1/playlists/${encodeURIComponent(id)}/content?` + params);
      const rows = entriesOf(doc).filter((entry: any) => !entry.hidden).map((entry: any) => item(entry.media, entry.id));
      return {items: q.get('order') === 'desc' ? rows.slice(-1) : rows, total: totalOf(doc), ...(cursorOf(doc) ? {nextCursor: cursorOf(doc)} : {})};
    }
    if (at === '/v1/lists' && method === 'GET' && q.get('kind') === 'playlist') {
      const params = new URLSearchParams({view: 'playlists', limit: '40'}); if (q.get('cursor')) params.set('cursor', q.get('cursor')!);
      const doc = await old('/v1/content?' + params);
      return {items: entriesOf(doc).map((card: any) => ({...listOf({id: card.id, name: card.title, summary: card.subtitle, role: 'owner', entryCount: card.count, revision: 0}), ...(card.lastChanged ? {updatedAt: card.lastChanged} : {})})),
        ...(cursorOf(doc) ? {nextCursor: cursorOf(doc)} : {})};
    }
    if (at === '/v1/lists' && method === 'GET') {
      const summary = await old('/v1/me/saved/summary');
      const of = (kind: string, n: number) => Array.from({length: n ?? 0}, () => ({kind}));
      return {items: [...of('playlist', summary?.playlists), ...of('collection', summary?.collections), ...of('saved_view', summary?.savedViews)]};
    }
    const one = /^\/v1\/lists\/([^/]+)(\/.*)?$/.exec(at);
    if (one) {
      const id = decodeURIComponent(one[1]!), rest = one[2] ?? '', playlist = `/v1/playlists/${encodeURIComponent(id)}`;
      const answered = async (raw: any) => {const resource = await old(playlist); return listOf({...resource, revision: raw?.revision ?? resource?.revision});};
      if (!rest && method === 'GET') return listOf(await old(playlist));
      if (rest === '/shares' && method === 'GET') {
        const resource = await old(playlist);
        return {shares: (resource?.shares ?? []).map((share: any) => ({profileId: share.profileId, name: share.displayName, canEdit: share.role === 'editor'})), people: []};
      }
      if (!rest && method === 'PATCH') return answered(await old(playlist, 'PATCH', {...(body as object)}));
      if (!rest && method === 'DELETE') {await old(playlist, 'DELETE', {}); return undefined;}
      if (rest === '/entries' && method === 'POST') {const raw = await old(`${playlist}/entries`, 'POST', {itemId: (body as {items: string[]}).items[0]}); return {list: await answered(raw), added: 1, already: 0};}
      if (rest.startsWith('/entries/') && method === 'DELETE') return answered(await old(`${playlist}/entries/${rest.slice(9)}`, 'DELETE', {}));
    }
    if (at === '/v1/lists' && method === 'POST') {
      const raw = await old('/v1/playlists', 'POST', {name: (body as {name: string}).name});
      return {id: raw?.playlistId ?? 'created', kind: 'playlist', name: (body as {name: string}).name, revision: String(raw?.revision ?? 1), owner: {id: 'owner', name: ''}, role: 'owner', count: 0, covers: [], createdAt: '', updatedAt: ''};
    }
    return request(path, method, body, signal);
  };
}

/** The same, for a test that gives `HttpLocalApi` its own fetch. A refusal from the fixture passes through as it is. */
export function legacySavedFetch(fetcher: (input: any, init?: any) => Promise<Response>, options: LegacySavedOptions = {}): (input: any, init?: any) => Promise<Response> {
  class Refused {constructor(readonly response: Response) {}}
  return async (input: any, init: any = {}) => {
    const href = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, origin = new URL(href).origin;
    const method = (init.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    const body = typeof init.body === 'string' && init.body ? JSON.parse(init.body) : undefined;
    const ask = async (oldPath: string, oldMethod = 'GET', oldBody?: unknown) => {
      const response = await fetcher(origin + oldPath, {...init, method: oldMethod, ...(oldBody === undefined ? {body: undefined} : {body: JSON.stringify(oldBody)})});
      if (!response.ok) throw new Refused(response);
      const text = await response.text(); return text ? JSON.parse(text) : undefined;
    };
    try {
      const headers = Object.fromEntries(new Headers(init.headers ?? {}).entries()), named = {'If-Match': headers['if-match'] ?? ''};
      const answer = await (legacySavedRequest(ask, options) as (...args: unknown[]) => Promise<unknown>)(href.slice(origin.length), method, body, undefined, named);
      return new Response(answer === undefined ? null : JSON.stringify(answer), {status: answer === undefined ? 204 : 200, headers: {'content-type': 'application/json'}});
    } catch (error) {if (error instanceof Refused) return error.response; throw error;}
  };
}

/** The same, around a test's own `{request}` api. */
export function legacySavedApi<A extends {request: (...args: any[]) => Promise<any>}>(api: A, options: LegacySavedOptions = {}): A {
  return {...api, request: legacySavedRequest(api.request.bind(api) as Request, options)} as A;
}
