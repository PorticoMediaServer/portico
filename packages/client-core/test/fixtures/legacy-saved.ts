// Saved tests written against the old server's documents keep pinning the same screen behaviour on the rebuilt server:
// a request the app now makes (GET /v1/items?inList, /v1/lists, ...) is asked of the fixture as the old one
// (/v1/content?view=watchlist, /v1/playlists/{id}, ...), and the fixture's answer is turned into the rebuilt server's.
// Only test code uses this.

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
const listOf = (resource: any) => ({id: resource.id, kind: 'playlist', name: resource.name, description: resource.summary ?? '', owner: {id: 'owner', name: ''},
  role: resource.role, count: resource.entryCount ?? 0, covers: [], revision: String(resource.revision ?? 0), createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z'});

/** A test transport that speaks the old Saved documents to its fixture and the rebuilt server's to the app. */
export function legacySavedRequest(request: Request): Request {
  return async (path, method = 'GET', body, signal) => {
    const url = new URL(path, 'https://legacy.invalid'), q = url.searchParams, at = url.pathname;
    const old = (oldPath: string, oldMethod = 'GET', oldBody?: unknown) => request(oldPath, oldMethod, oldBody, signal);
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
export function legacySavedFetch(fetcher: (input: any, init?: any) => Promise<Response>): (input: any, init?: any) => Promise<Response> {
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
      const answer = await legacySavedRequest(ask)(href.slice(origin.length), method, body);
      return new Response(answer === undefined ? null : JSON.stringify(answer), {status: answer === undefined ? 204 : 200, headers: {'content-type': 'application/json'}});
    } catch (error) {if (error instanceof Refused) return error.response; throw error;}
  };
}
