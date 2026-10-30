import {routePath, type RouteResponse} from '../../contracts/src/route-paths.ts';
import {decodeGetSearchGenresResponse, decodeGetGenreQueryResponse} from '../../contracts/src/decoders.ts';
import type {BrowseRequestBody} from './browse.ts';
import type {LibraryContentApi} from './library-content.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {ApiError, errorFieldPath, retryAfterSecondsFrom} from './index.ts';
import {readPreparation, type PreparationRequestPort, type PreparationResponse, type PreparationRuntime} from './read-preparation.ts';

export type SearchGenreArt = Readonly<{kind: 'item' | 'show'; id: string; path: string}>;
export type SearchGenre = Readonly<{
  value: string; label: string; count: number;
  backdrop?: SearchGenreArt; covers: readonly SearchGenreArt[];
}>;
type GenreReadyWire = Exclude<Extract<RouteResponse<'get_search_genres'>, {genres: unknown}>, {kind: unknown}>;
export type SearchGenrePage = Readonly<Omit<GenreReadyWire, 'genres'> & {genres: readonly SearchGenre[]}>;
export type SearchGenreRequest = Readonly<{limit?: number; cursor?: string; preview?: '' | 'backdrop' | 'covers'}>;
export type SearchGenrePreparing = Readonly<{kind: 'genre-preparing'; state: 'preparing'; queryId: string; retryAfterMs: number}>;
/** Cold fallback is unknown, not a definitive empty directory/preview. No anchors/HasGenres. */
export type SearchGenreFallbackPage = Readonly<{
  kind: 'genre-fallback'; queryId: string; revision: 'preparing'; genres: readonly [];
  hasGenresKnown: false; totalIsLowerBound: true; preparation: SearchGenrePreparing;
}>;
export type SearchGenreApi = LibraryContentApi & Partial<PreparationRequestPort>;
export type SearchGenreReadMetadata = Readonly<{reanchored: boolean; previousRevision?: string}>;
export type SearchGenreReadOptions = Readonly<{
  /** A continuation owner can transfer its previous page's interest after token consumption.
   * No page arrays/history are retained by this operation or by the module's lease registry. */
  previousPage?: SearchGenrePage;
  isCurrent?: () => boolean;
  runtime?: Partial<PreparationRuntime>;
  /** Optional generated adapters; defaults use get_genre_query/cancel_genre_query. */
  paths?: Readonly<{poll(queryId: string): string; cancel(queryId: string): string}>;
  errorFromResponse?: (response: PreparationResponse) => Error;
  onPreparing?: (pending: SearchGenrePreparing, response: PreparationResponse) => void;
  /** Callback-only unknown panel; the pending read signal still owns cancellation. No
   * fallback is registered as a ready page/lease or cached, and read still awaits exact. */
  onFallback?: (fallback: SearchGenreFallbackPage, response: PreparationResponse) => void;
  /** The old cursor was refused. The next read re-anchors without it; use read metadata on
   * the returned page to distinguish this accepted new root from ordinary continuation. */
  onReanchor?: (response: PreparationResponse) => void;
}>;

const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
const opaqueId = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,512}$/.test(value);
const cursor = opaqueId;
const safeCount = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
function invalid(response?: PreparationResponse): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_genre_response', ...(response ? {response} : {})});
}

function art(value: unknown, role: 'backdrop' | 'poster'): SearchGenreArt {
  if (!object(value) || (value.kind !== 'item' && value.kind !== 'show') || typeof value.id !== 'string' || !/^[a-z0-9_-]{1,256}$/i.test(value.id) || !text(value.path, 2048)) invalid();
  const expected = routePath('get_metadata_kind_id_art_role', {kind: value.kind, id: value.id, role});
  if (!value.path.startsWith(`${expected}?`)) invalid();
  // Only the selected-art route for this exact title and slot can be returned.
  // A URL, provider locator, different subject, fragment or extra query is not
  // an art reference. The query width requests a variant; it is not geometry.
  const parsed = new URL(value.path, 'https://metadata.invalid');
  if (parsed.origin !== 'https://metadata.invalid' || parsed.pathname !== expected || parsed.hash || parsed.searchParams.size !== 2 || !/^[a-f0-9]{64}$/.test(parsed.searchParams.get('v') ?? '') || parsed.searchParams.get('w') !== (role === 'backdrop' ? '1920' : '400')) invalid();
  return Object.freeze({kind: value.kind, id: value.id, path: value.path});
}

/** The server owns counts, ordering and representatives. This parser preserves
 * them, including counted genres with missing art for the All genres list.
 * Each surface applies only its drawn art requirement and prefix length. */
export function parseSearchGenres(input: unknown): SearchGenrePage {
  // Null optional slots are rejected before the generated decoder can treat them as absent.
  if (!object(input) || !opaqueId(input.queryId) || typeof input.revision !== 'string' || input.revision.length === 0
      || input.hasGenres !== undefined && typeof input.hasGenres !== 'boolean'
      || input.kind !== undefined || input.state !== undefined || input.retryAfterMs !== undefined
      || !Array.isArray(input.genres) || input.genres.length > 50
      || input.genres.some(genre => !object(genre) || genre.backdrop === null)) invalid();
  for (const key of ['nextCursor', 'previousCursor', 'reloadCursor'] as const) if (input[key] !== undefined && !cursor(input[key])) invalid();
  if (input.hasGenres === false && input.genres.length > 0) invalid();
  let raw: GenreReadyWire;
  try { const decoded = decodeGetSearchGenresResponse(input); if (!('genres' in decoded) || 'kind' in decoded) invalid(); raw = decoded; } catch { invalid(); }
  const seen = new Set<string>();
  const genres = raw.genres.map(genre => {
    if (!object(genre) || typeof genre.value !== 'string' || genre.value.length === 0 || typeof genre.label !== 'string'
        || !safeCount(genre.count) || genre.count < 1 || seen.has(genre.value)
        || !Array.isArray(genre.covers) || genre.covers.length > 2) invalid();
    seen.add(genre.value);
    const covers = genre.covers.map(value => art(value, 'poster'));
    if (new Set(covers.map(value => `${value.kind}:${value.id}`)).size !== covers.length) invalid();
    return Object.freeze({value: genre.value, label: genre.label, count: genre.count,
      ...(genre.backdrop !== undefined ? {backdrop: art(genre.backdrop, 'backdrop')} : {}),
      covers: Object.freeze(covers)});
  });
  return Object.freeze({queryId: raw.queryId, revision: raw.revision, genres: Object.freeze(genres),
    ...(raw.hasGenres !== undefined ? {hasGenres: raw.hasGenres} : {}),
    ...(raw.nextCursor !== undefined ? {nextCursor: raw.nextCursor} : {}),
    ...(raw.previousCursor !== undefined ? {previousCursor: raw.previousCursor} : {}),
    ...(raw.reloadCursor !== undefined ? {reloadCursor: raw.reloadCursor} : {})});
}

export function parseSearchGenrePreparing(input: unknown, response?: PreparationResponse): SearchGenrePreparing {
  if (!object(input) || Object.keys(input).some(key => !['kind', 'state', 'queryId', 'retryAfterMs'].includes(key))
      || input.kind !== 'genre-preparing' || input.state !== 'preparing' || !opaqueId(input.queryId)
      || !safeCount(input.retryAfterMs) || input.retryAfterMs < 1) invalid(response);
  try { const decoded = decodeGetGenreQueryResponse(input); if (!('state' in decoded)) invalid(response); } catch { invalid(response); }
  return Object.freeze({kind: 'genre-preparing', state: 'preparing', queryId: input.queryId, retryAfterMs: input.retryAfterMs});
}

/** The new fallback DTO is intentionally stricter than a ready page: unknown presence,
 * explicit lower-bound status, empty rows, no mutable-staging continuation anchors. */
export function parseSearchGenreFallback(input: unknown, response?: PreparationResponse): SearchGenreFallbackPage {
  if (!object(input) || Object.keys(input).some(key => ![
    'kind', 'queryId', 'revision', 'genres', 'hasGenresKnown', 'totalIsLowerBound', 'preparation',
  ].includes(key)) || input.kind !== 'genre-fallback' || !opaqueId(input.queryId) || input.revision !== 'preparing'
      || !Array.isArray(input.genres) || input.genres.length !== 0 || input.hasGenresKnown !== false || input.totalIsLowerBound !== true) invalid(response);
  const preparation = parseSearchGenrePreparing(input.preparation, response);
  if (preparation.queryId !== input.queryId) invalid(response);
  return Object.freeze({kind: 'genre-fallback', queryId: input.queryId, revision: 'preparing',
    genres: Object.freeze([]) as readonly [], hasGenresKnown: false, totalIsLowerBound: true, preparation});
}

export function searchGenresPath(request: SearchGenreRequest = {}): string {
  if (!object(request) || Object.keys(request).some(key => !['limit', 'cursor', 'preview'].includes(key))) throw new Error('Invalid genre read window.');
  const limit = request.limit === undefined ? 50 : request.limit;
  if (!safeCount(limit) || limit < 1 || limit > 50 || request.cursor !== undefined && request.cursor !== '' && !cursor(request.cursor)) throw new Error('Invalid genre read window.');
  if (request.preview !== undefined && request.preview !== '' && request.preview !== 'backdrop' && request.preview !== 'covers' || !!request.preview && !!request.cursor) throw new Error('Invalid genre preview.');
  const query = new URLSearchParams();
  // An omitted continuation limit inherits its authenticated descriptor on the server.
  if (request.limit !== undefined || !request.cursor) query.set('limit', String(limit));
  if (request.preview) query.set('preview', request.preview);
  if (request.cursor) query.set('cursor', request.cursor);
  return `${routePath('get_search_genres')}?${query}`;
}

function current(signal?: AbortSignal, isCurrent: () => boolean = () => true): void {
  if (signal?.aborted || !isCurrent()) throw Object.assign(new Error('Genre read cancelled.'), {name: 'AbortError'});
}

type GenreLease = {active: boolean; api: SearchGenreApi; limit: number; release(): void; metadata: SearchGenreReadMetadata};
const leases = new WeakMap<SearchGenrePage, GenreLease>();
/** Release before dropping a page, after its continuation is consumed, and on retirement.
 * The registry is weak; lease closures/abort listeners never capture page rows or history. */
export function releaseSearchGenres(page: SearchGenrePage | undefined): void { if (page) leases.get(page)?.release(); }
export function searchGenresReadMetadata(page: SearchGenrePage): SearchGenreReadMetadata | undefined { return leases.get(page)?.metadata; }

function makeLease(api: SearchGenreApi, limit: number, metadata: SearchGenreReadMetadata, dispose: () => void,
  controller: AbortController, signal: AbortSignal | undefined, retain: boolean): GenreLease {
  const lease: GenreLease = {active: retain, api, limit, metadata, release() {
    if (!lease.active) return;
    lease.active = false;
    signal?.removeEventListener('abort', onAbort);
    controller.abort(); dispose();
  }};
  const onAbort = () => lease.release();
  if (retain) {
    signal?.addEventListener('abort', onAbort, {once: true});
    if (signal?.aborted) lease.release();
  } else dispose();
  return lease;
}

const genreQueryPaths = {
  poll: (id: string) => routePath('get_genre_query', {id}),
  cancel: (id: string) => routePath('cancel_genre_query', {id}),
};
function header(raw: PreparationResponse, name: string): string | undefined {
  for (const [key, value] of Object.entries(raw.headers)) if (key.toLowerCase() === name) return value;
  return undefined;
}
function genreError(raw: PreparationResponse, now: number): ApiError {
  const envelope = object(raw.body) ? raw.body : undefined;
  const e = envelope && object(envelope.error) ? envelope.error : envelope;
  const retryable = e?.retryable !== undefined ? e.retryable === true : e?.retry !== undefined ? e.retry === 'same_request' : raw.status >= 500;
  const error = new ApiError(raw.status, typeof e?.code === 'string' ? e.code : 'request_failed',
    typeof e?.message === 'string' ? e.message : `Request failed (${raw.status}).`, retryable,
    retryAfterSecondsFrom(header(raw, 'retry-after') ?? null, now), typeof e?.serverTime === 'string' ? e.serverTime : undefined);
  const path = errorFieldPath(e?.path); if (path) error.path = path;
  return error;
}
type Predecessor = {lease: GenreLease; revision: string};

/** One page only, using the selected server's raw auth/deadline transport. Directory pages
 * with anchors own a lease: releaseSearchGenres(page) is mandatory on retirement/replacement;
 * the supplied signal also owns release. No signal means the caller must release explicitly.
 * Pass previousPage for automatic release AFTER consuming its cursor, never before GET.
 * Previews and pages without anchors release immediately. Returned objects keep wire fields
 * only; metadata/leases live in weak maps. Parent controllers retain only bounded windows. */
export async function readSearchGenres(api: SearchGenreApi, request: SearchGenreRequest = {}, signal?: AbortSignal,
  options: SearchGenreReadOptions = {}): Promise<SearchGenrePage> {
  current(signal, options.isCurrent);
  searchGenresPath(request);
  // Copy primitive descriptor fields. A caller cannot change a query under an in-flight read.
  const captured: SearchGenreRequest = Object.freeze({...request});
  const previous = options.previousPage;
  let predecessor: Predecessor | undefined;
  if (previous) {
    const lease = leases.get(previous);
    if (!captured.cursor || !lease?.active || lease.api !== api
        || ![previous.nextCursor, previous.previousCursor, previous.reloadCursor].includes(captured.cursor)) {
      throw Object.assign(new Error('Genre continuation unavailable.'), {code: 'stale_continuation'});
    }
    predecessor = {lease, revision: previous.revision};
  }
  // The worker receives compact predecessor metadata/disposer only, never prior page arrays.
  const {isCurrent, runtime, paths, errorFromResponse, onPreparing, onReanchor, onFallback} = options;
  return executeGenreRead(api, Object.freeze({...captured,
    ...(predecessor && captured.limit === undefined ? {limit: predecessor.lease.limit} : {})}), signal,
  {isCurrent, runtime, paths, errorFromResponse, onPreparing, onReanchor, onFallback}, predecessor);
}

async function executeGenreRead(api: SearchGenreApi, descriptor: SearchGenreRequest, signal: AbortSignal | undefined,
  options: Omit<SearchGenreReadOptions, 'previousPage'>, predecessor?: Predecessor): Promise<SearchGenrePage> {
  const {isCurrent = () => true, runtime, paths = genreQueryPaths, onPreparing, onReanchor, onFallback, errorFromResponse} = options;
  const controller = new AbortController(), onAbort = () => controller.abort();
  signal?.addEventListener('abort', onAbort, {once: true});
  const previousRevision = predecessor?.revision;
  let previousLease = predecessor?.lease;
  predecessor = undefined;
  const releasePredecessor = () => {
    const old = previousLease; previousLease = undefined;
    old?.release();
  };
  let reanchored = false, completed = false;
  let staleResponse: PreparationResponse | undefined;
  const path = (method: 'poll' | 'cancel', id: string) => {
    const value = paths[method](id);
    if (!opaqueId(id) || value !== genreQueryPaths[method](id)) throw new Error('Invalid genre preparation path.');
    return value;
  };
  const raw: PreparationRequestPort = {requestRaw: (url, method, transportOptions = {}) => {
    if (api.requestRaw) return api.requestRaw(url, method, transportOptions).then(response => {
      if (method === 'GET' && response.status === 409) staleResponse = response;
      return response;
    });
    // Read-only test ports predate the raw seam. They may supply one ready body, never
    // manufacture statuses, poll hidden 202 JSON, or send cancellation through mutations.
    if (method !== 'GET') return Promise.resolve({status: 204, headers: {}});
    return api.request<unknown>(url, 'GET', undefined, transportOptions.signal).then(body => ({status: 200, headers: {}, body}));
  }};
  try {
    current(signal, isCurrent);
    const result = await readPreparation<SearchGenreRequest, SearchGenrePage, SearchGenreFallbackPage>({api: raw, descriptor,
      paths: {
        initial: searchGenresPath,
        refresh: read => {
          reanchored = true; releasePredecessor();
          current(signal, isCurrent);
          const stale = staleResponse; staleResponse = undefined;
          if (stale) onReanchor?.(stale);
          current(signal, isCurrent);
          return searchGenresPath({...read, cursor: undefined});
        },
        poll: id => path('poll', id), cancel: id => path('cancel', id),
      },
      readyQueryId: (body, response) => {
        if (!object(body) || !opaqueId(body.queryId)) invalid(response);
        releasePredecessor(); return body.queryId;
      },
      parsePreparing: (body, response) => { const pending = parseSearchGenrePreparing(body, response); releasePredecessor(); return pending; },
      parseIntermediateReady: (body, response) => {
        if (!object(body) || body.kind !== 'genre-fallback') return undefined;
        const fallback = parseSearchGenreFallback(body, response);
        return {pending: fallback.preparation, value: fallback};
      },
      onIntermediateReady: (fallback, response) => {
        current(signal, isCurrent);
        onFallback?.(fallback, response);
      },
      parseReady: (body, response) => {
        let page: SearchGenrePage;
        try { page = parseSearchGenres(body); } catch { invalid(response); }
        if (page.genres.length > (descriptor.limit ?? 50)) invalid(response);
        if (descriptor.preview && (page.hasGenres === undefined || page.nextCursor !== undefined || page.previousCursor !== undefined || page.reloadCursor !== undefined
            || page.genres.some(genre => descriptor.preview === 'backdrop' ? !genre.backdrop : genre.covers.length !== 2))) invalid(response);
        if (previousRevision !== undefined && !reanchored && page.revision !== previousRevision) {
          throw Object.assign(new Error('Genre revision changed.'), {code: 'stale_continuation', response});
        }
        return page;
      },
      retainOnReady: !descriptor.preview, signal: controller.signal, isCurrent,
      runtime, onPreparing: (pending, response) => onPreparing?.(Object.freeze({kind: 'genre-preparing', state: 'preparing', ...pending}), response),
      errorFromResponse: errorFromResponse ?? (response => genreError(response, runtime?.now?.() ?? Date.now())),
    });
    try { current(signal, isCurrent); } catch (error) { result.release(); throw error; }
    const page = result.value;
    const retain = !descriptor.preview && !!(page.nextCursor || page.previousCursor || page.reloadCursor);
    const metadata = Object.freeze({reanchored, ...(previousRevision !== undefined ? {previousRevision} : {})});
    const lease = makeLease(api, descriptor.limit ?? 50, metadata, result.release, controller, signal, retain);
    try { current(signal, isCurrent); } catch (error) { lease.release(); throw error; }
    leases.set(page, lease); completed = true;
    return page;
  } finally {
    signal?.removeEventListener('abort', onAbort);
    if (!completed) { controller.abort(); releasePredecessor(); }
  }
}

/** A tile opens the exact movie/show domain its count describes through the
 * existing across-library browse read. Use this pivot for its capabilities
 * and context facets as well; the parent retains viewer/cursor authority. */
export const searchGenreBrowsePivot = 'video-titles';
export function searchGenreBrowseRequest(value: string): BrowseRequestBody {
  if (typeof value !== 'string' || value.length === 0) throw new Error('Invalid genre value.');
  return Object.freeze({pivot: searchGenreBrowsePivot,
    query: Object.freeze({field: 'genre', operator: 'equals', value}),
    sort: Object.freeze([Object.freeze({field: 'title', direction: 'asc' as const})])});
}
