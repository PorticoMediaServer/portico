// Search tests written against the old server's search document keep pinning the same screen behaviour on the
// rebuilt server: a request the app now makes is read back in the old parameters the fixture understands, and the
// fixture's answer is turned into the rebuilt server's answer (GET /v1/search). Only test code uses this.

const groupOfKind: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', episode: 'episodes', person: 'people', artist: 'artists',
  album: 'albums', track: 'songs', audiobook: 'books', book: 'reading'};
const kindOfGroup: Readonly<Record<string, string>> = Object.fromEntries(Object.entries(groupOfKind).map(([kind, group]) => [group, kind]));
const sortOf: Readonly<Record<string, string>> = {year: 'releaseYear', added: 'dateAdded', title: 'title'};
const itemKind: Readonly<Record<string, string>> = {song: 'track', book: 'audiobook', ebook: 'book', comic: 'book'};

/** The request in the old parameters: `group`, `sort`, `direction`, `record=1`. Other paths are left as they are. */
export function legacySearchPath(path: string): string {
  const absolute = /^[a-z]+:/i.test(path), url = new URL(path, 'https://legacy.invalid');
  if (url.pathname !== '/v1/search') return path;
  const params = new URLSearchParams();
  for (const [key, value] of url.searchParams) {
    if (key === 'kind') params.set('group', groupOfKind[value] ?? value);
    else if (key === 'sort') params.set('sort', sortOf[value] ?? value);
    else if (key === 'order') params.set('direction', value);
    else if (key === 'record') params.set('record', value === 'true' ? '1' : value);
    else params.set(key, value);
  }
  return (absolute ? url.origin : '') + url.pathname + '?' + params.toString();
}

/** One old card as a rebuilt server's title. */
function item(entry: Record<string, any>): Record<string, unknown> {
  const position = entry.playback?.startMs ?? entry.progressMs;
  return {id: entry.id, kind: itemKind[entry.kind] ?? entry.kind, title: entry.displayTitle ?? entry.title ?? '', ...(entry.libraryId ? {libraryId: entry.libraryId} : {}),
    ...(entry.available === undefined ? {} : {available: entry.available}), ...(position ? {state: {positionMs: position, watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}} : {}),
    ...(entry.year ? {year: entry.year} : {}), ...(entry.posterUrl ? {images: {poster: entry.posterUrl}} : {})};
}

/** The old search document as the rebuilt server's answer. A failed group, a channel or a group with no kind there is left out. */
export function fromLegacySearch(raw: any): unknown {
  if (!raw || typeof raw !== 'object' || !Array.isArray(raw.groups)) return raw;
  return {groups: raw.groups.filter((group: any) => kindOfGroup[group?.id] && group.status !== 'error').map((group: any) => {
    const kind = kindOfGroup[group.id], rows = Array.isArray(group.items) ? group.items : [];
    return {kind, ...(typeof group.totalCount === 'number' ? {total: group.totalCount} : {}), ...(group.totalCountExact === false ? {totalIsMinimum: true} : {}),
      ...(kind === 'person' ? {people: rows.map((row: any) => ({id: row.id, name: row.title ?? '', images: row.posterUrl ? {thumb: row.posterUrl} : {}}))} : {items: rows.map(item)}),
      ...(group.nextCursor ? {nextCursor: group.nextCursor} : {})};
  })};
}

/** Recent searches in the old page shape, as the rebuilt server's answer. */
export function fromLegacyHistory(raw: any): unknown {
  if (!raw || typeof raw !== 'object' || !Array.isArray(raw.entries)) return raw;
  return {items: (raw.remembered === false ? [] : raw.entries).map((entry: any) => ({query: entry.query ?? entry.q}))};
}

/** A test transport that speaks the old search document to its fixture and the rebuilt server's to the app. */
export function legacySearchRequest<Request extends (path: string, ...rest: any[]) => Promise<any>>(request: Request): Request {
  return (async (path: string, ...rest: any[]) => {
    const legacy = legacySearchPath(path), raw = await request(legacy, ...rest);
    const at = new URL(legacy, 'https://legacy.invalid').pathname;
    return at === '/v1/search' ? fromLegacySearch(raw) : at === '/v1/search/history' && (rest[0] ?? 'GET') === 'GET' ? fromLegacyHistory(raw) : raw;
  }) as Request;
}

/** The same, for a test that gives `HttpLocalApi` its own fetch. */
export function legacySearchFetch(fetcher: (input: any, init?: any) => Promise<Response>): (input: any, init?: any) => Promise<Response> {
  return async (input: any, init?: any) => {
    const href = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, legacy = legacySearchPath(href);
    const response = await fetcher(typeof input === 'string' || input instanceof URL ? legacy : new Request(legacy, input), init);
    const at = new URL(legacy, 'https://legacy.invalid').pathname, method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    if (!response.ok || at !== '/v1/search' && !(at === '/v1/search/history' && method === 'GET')) return response;
    const body = await response.json();
    return new Response(JSON.stringify(at === '/v1/search' ? fromLegacySearch(body) : fromLegacyHistory(body)), {status: response.status, headers: response.headers});
  };
}

/** The same, for a native harness hook that answers `{path, method}` with `{status, body}`. */
export function legacySearchResponse<Hook extends (request: any, ...rest: any[]) => Promise<any>>(hook: Hook): Hook {
  return (async (request: any, ...rest: any[]) => {
    const legacy = legacySearchPath(request.path), out = await hook({...request, path: legacy}, ...rest);
    const at = new URL(legacy, 'https://legacy.invalid').pathname;
    if (!out || typeof out !== 'object' || !('body' in out) || out.status >= 400) return out;
    if (at === '/v1/search') return {...out, body: fromLegacySearch(out.body)};
    if (at === '/v1/search/history' && (request.method ?? 'GET') === 'GET') return {...out, body: fromLegacyHistory(out.body)};
    return out;
  }) as Hook;
}
