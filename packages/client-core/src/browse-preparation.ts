import {validSelectionBasis} from './selection-basis.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {ApiError} from './index.ts';
import {browsePath, parseBrowseFacets, parseBrowseResult, parseWholeSelection, type BrowseFacetPage, type BrowseResult, type BrowseWholeSelection, type WholeSelectionPage} from './browse.ts';
import type {LibraryContentApi} from './library-content.ts';
import {refuseUnknownVariant} from './tolerant.ts';
import {readPreparation, PreparationProtocolError, type PreparationResponse, type PreparingRead, type PreparationRuntime} from './read-preparation.ts';

const preparationVariants = ['ready', 'page', 'preparing', 'reading-preparing'] as const;
type Domain = 'reading-facet' | 'reading-browse' | 'browse-selection';
function capture<T>(value: T): T {
  const freeze = (v: unknown): void => {if (object(v) || Array.isArray(v)) {Object.values(v).forEach(freeze); Object.freeze(v);}};
  const result = JSON.parse(JSON.stringify(value)) as T; freeze(result); return result;
}
type Lifetime = Readonly<{signal?: AbortSignal; isCurrent: () => boolean; runtime?: Partial<PreparationRuntime>}>;
const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);

function current(lifetime: Lifetime): void {
  if (lifetime.signal?.aborted || !lifetime.isCurrent()) throw Object.assign(new Error('Browse read retired'), {name: 'AbortError'});
}
function legacyReady(body: unknown): unknown {
  // Body-only ports cannot expose status. An explicit pending body is never a 200.
  if (object(body) && (body.state === 'preparing' || typeof body.kind === 'string' && body.kind.endsWith('-preparing'))) {
    throw new PreparationProtocolError('invalid_preparing');
  }
  return body;
}

/** A handle from another domain is never usable. */
export function parseBrowsePreparing(body: unknown, domain: Domain, response?: PreparationResponse): PreparingRead {
  refuseUnknownVariant(body, preparationVariants);
  if (!object(body) || body.variant !== (domain === 'reading-facet' ? 'reading-preparing' : 'preparing') || body.kind !== `${domain}-preparing` || body.state !== 'preparing'
    || typeof body.queryId !== 'string' || !/^[A-Za-z0-9._~-]{1,512}$/.test(body.queryId) || ['.', '..'].includes(body.queryId)
    || !Number.isSafeInteger(body.retryAfterMs) || (body.retryAfterMs as number) < 0) {
    throw new PreparationProtocolError('invalid_preparing', response);
  }
  return Object.freeze({queryId: body.queryId, retryAfterMs: body.retryAfterMs as number});
}

function queryPath(domain: Domain, libraryId: string, queryId: string, cancel = false): string {
  const params = {id: libraryId, queryId};
  if (domain === 'reading-facet') return cancel ? routePath('delete_library_reading_facet_query', params) : routePath('get_library_reading_facet_query', params);
  if (domain === 'reading-browse') return cancel ? routePath('delete_library_reading_browse_query', params) : routePath('get_library_reading_browse_query', params);
  return cancel ? routePath('delete_library_browse_selection_query', params) : routePath('get_library_browse_selection_query', params);
}

export function browsePreparationError(response: PreparationResponse): Error {
  const body = object(response.body) ? response.body : {}, detail = object(body.error) ? body.error : body;
  return Object.assign(new ApiError(response.status, typeof detail.code === 'string' ? detail.code : 'request_failed',
    typeof detail.message === 'string' ? detail.message : `Request failed (${response.status}).`,
    detail.retryable === true || detail.retry === 'same_request', undefined), {response,
    ...(typeof detail.retry === 'string' ? {retry: detail.retry} : {})});
}

/** Ordinary page handles are a different domain from its optional aggregate. */
export function releaseBrowseSelection(api: LibraryContentApi, libraryId: string, response?: PreparationResponse): void {
  if (!api.requestRaw || !response || response.status !== 200 || !object(response.body) || response.body.wholeSelectionPreparing === undefined) return;
  try {
    const pending = parseBrowsePreparing(response.body.wholeSelectionPreparing, 'browse-selection', response);
    void api.requestRaw(queryPath('browse-selection', libraryId, pending.queryId, true), 'DELETE', {singleAttempt: true}).catch(() => {});
  } catch { /* An unvalidated handle cannot authorize cleanup. */ }
}

/** Only the initial/re-anchor GET seam becomes the captured POST. The shared reader
 * owns pacing, 409 recovery and cancellation; poll stays GET and cancel stays DELETE. */
export async function readBrowsePage(api: LibraryContentApi, libraryId: string, body: unknown, lifetime: Lifetime): Promise<Readonly<{page: BrowseResult; response?: PreparationResponse}>> {
  const captured = capture(body);
  let reanchor = false;
  const refreshed = object(captured) ? capture(Object.fromEntries(Object.entries(captured).filter(([key]) => key !== 'cursor').map(([key, value]) =>
    [key, key === 'range' && object(value) ? {start: value.start} : value]))) : captured;
  const path = browsePath(libraryId);
  if (!api.requestRaw) {
    current(lifetime); const value = await api.request(path, 'POST', captured, lifetime.signal); current(lifetime);
    refuseUnknownVariant(value, preparationVariants);
    return {page: parseBrowseResult(legacyReady(value))};
  }
  const raw = api.requestRaw.bind(api);
  const result = await readPreparation({api: {requestRaw: (target, method, options) => raw(target, target === path && method === 'GET' ? 'POST' : method,
    {...options, ...(target === path && method === 'GET' ? {body: reanchor ? refreshed : captured} : {})}).then(response => {
      if (lifetime.signal?.aborted || !lifetime.isCurrent()) releaseBrowseSelection(api, libraryId, response);
      return response;
    })}, descriptor: libraryId,
    paths: {initial: () => path, refresh: () => {reanchor = true; return path;},
      poll: id => queryPath('reading-browse', libraryId, id), cancel: id => queryPath('reading-browse', libraryId, id, true)},
    parseReady: value => {refuseUnknownVariant(value, preparationVariants); return parseBrowseResult(value);}, parsePreparing: (value, response) => parseBrowsePreparing(value, 'reading-browse', response),
    errorFromResponse: browsePreparationError, ...lifetime});
  return {page: result.value, response: result.response};
}

/** Reading offset facets retain the exact ready DTO. SIMPLE has its own adapter. */
export async function readReadingFacet(api: LibraryContentApi, libraryId: string, path: string, field: string, lifetime: Lifetime): Promise<BrowseFacetPage> {
  if (!api.requestRaw) {
    current(lifetime); const value = await api.request(path, 'GET', undefined, lifetime.signal); current(lifetime);
    refuseUnknownVariant(value, preparationVariants);
    return parseBrowseFacets(legacyReady(value), field, 'page');
  }
  const result = await readPreparation({api: {requestRaw: api.requestRaw.bind(api)}, descriptor: Object.freeze({libraryId, path, field}),
    paths: {initial: d => d.path, refresh: d => d.path,
      poll: (id, d) => queryPath('reading-facet', d.libraryId, id), cancel: (id, d) => queryPath('reading-facet', d.libraryId, id, true)},
    parseReady: value => {refuseUnknownVariant(value, preparationVariants); return parseBrowseFacets(value, field, 'page');}, parsePreparing: (value, response) => parseBrowsePreparing(value, 'reading-facet', response),
    errorFromResponse: browsePreparationError, ...lifetime});
  return result.value;
}

/** Prepared aggregates require the complete witness even when the ordinary page
 * revision stayed unchanged. Legacy ordinary-200 descriptors remain optional. */
export function parsePreparedBrowseSelection(value: unknown, libraryId: string, basis: WholeSelectionPage, response?: PreparationResponse): BrowseWholeSelection {
  const whole = parseWholeSelection(value, basis);
  if (!whole || whole.libraryId !== libraryId || !validSelectionBasis(whole.selectionBasis)) {
    throw Object.assign(new Error('browse-selection-basis-changed'), {code: 'stale_continuation', response});
  }
  return whole;
}

/** Seed the shared reader with the explicit pending aggregate beside the ordinary
 * successful page, including all original headers, before making any poll request. */
export async function readBrowseSelection(api: LibraryContentApi, libraryId: string, page: WholeSelectionPage, response: PreparationResponse, lifetime: Lifetime, originalBody: unknown): Promise<BrowseWholeSelection> {
  if (!api.requestRaw || !object(response.body)) throw new PreparationProtocolError('invalid_preparing', response);
  const pending = response.body.wholeSelectionPreparing;
  const handle = parseBrowsePreparing(pending, 'browse-selection', response);
  const initial = queryPath('browse-selection', libraryId, handle.queryId);
  let seeded = true;
  const raw = api.requestRaw.bind(api);
  const basis = capture(page), body = capture(originalBody);
  const refreshPath = browsePath(libraryId);
  let result: Awaited<ReturnType<typeof readPreparation<WholeSelectionPage, BrowseWholeSelection>>>;
  try {result = await readPreparation({api: {requestRaw: (path, method, options) => {
    if (seeded && method === 'GET') {seeded = false; return Promise.resolve({status: 202, headers: response.headers, body: pending});}
    if (path === refreshPath && method === 'GET') return raw(path, 'POST', {...options, body}).then(reply => {
      if (reply.status !== 200 || !object(reply.body)) return reply;
      if (reply.body.wholeSelection !== undefined) return {...reply, body: reply.body.wholeSelection};
      if (reply.body.wholeSelectionPreparing !== undefined) return {...reply, status: 202, body: reply.body.wholeSelectionPreparing};
      throw Object.assign(new Error('browse-selection-basis-changed'), {code: 'stale_continuation', response: reply});
    });
    return raw(path, method, options);
  }}, descriptor: basis,
    paths: {initial: () => initial,
      // Re-read the captured page once; its exact original basis must still match.
      refresh: () => refreshPath, poll: id => queryPath('browse-selection', libraryId, id), cancel: id => queryPath('browse-selection', libraryId, id, true)},
    parseReady: (value, metadata) => {
      refuseUnknownVariant(value, preparationVariants);
      const whole = parsePreparedBrowseSelection(value, libraryId, basis, metadata);
      if (!object(body) || JSON.stringify(body.query ?? null) !== JSON.stringify(basis.query) || JSON.stringify(body.sort ?? []) !== JSON.stringify(basis.sort)) {
        throw Object.assign(new Error('browse-selection-query-changed'), {code: 'stale_continuation', response: metadata});
      }
      return whole;
    }, parsePreparing: (value, metadata) => parseBrowsePreparing(value, 'browse-selection', metadata),
    errorFromResponse: browsePreparationError, ...lifetime});}
  catch (error) {if (seeded) releaseBrowseSelection(api, libraryId, response); throw error;}
  return result.value;
}
