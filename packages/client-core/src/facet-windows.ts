import {routePath} from '../../contracts/src/route-paths.ts';
import {
  readPreparation,
  type PreparationRequestPort, type PreparationResponse, type PreparationRuntime,
} from './read-preparation.ts';
import {unreadableServerResponse} from './server-messages.ts';

export type SimpleFacetField = 'genre' | 'year' | 'decade' | 'studio' | 'collection'
  | 'contentRating' | 'audioLanguage' | 'network' | 'tag' | 'label' | 'series' | 'resolution';
export type SimpleFacetValue = Readonly<{
  value: string; label: string; count: number; countIsLowerBound: false; seriesId?: string;
}>;
export type SimpleFacetWindow = Readonly<{
  queryId: string; basis: string; limit: number; returned: number;
  next?: string; previous?: string; reload?: string;
}>;
export type SimpleFacetPage = Readonly<{
  kind: 'simple-keyset'; field: SimpleFacetField; revision: string;
  total: number; totalIsLowerBound: false;
  values: readonly SimpleFacetValue[]; window: SimpleFacetWindow;
}>;
export type SimpleFacetPreparing = Readonly<{
  kind: 'facet-preparing'; state: 'preparing'; queryId: string; retryAfterMs: number;
}>;
export type SimpleFacetFallbackValue = Readonly<{
  value: string; label: string; count: number; countIsLowerBound: true; seriesId?: string;
}>;
/** A bounded, explicitly lower-bound panel while the SAME preparation is still running.
 * It has no continuation anchors and is never an exact SimpleFacetPage. */
export type SimpleFacetFallbackPage = Readonly<{
  kind: 'facet-fallback'; field: SimpleFacetField; revision?: string;
  total: number; totalIsLowerBound: true; values: readonly SimpleFacetFallbackValue[];
  preparation: SimpleFacetPreparing;
}>;
export type SimpleFacetRead = Readonly<{
  libraryId: string; field: SimpleFacetField; q?: string; limit?: number;
  after?: string; before?: string; at?: string;
}>;

/** Same ordinary byte target as the server. An exceptional legal scalar is transient O(B). */
export const SIMPLE_FACET_PAGE_BYTES = 256 * 1024;
const fields: readonly string[] = ['genre', 'year', 'decade', 'studio', 'collection',
  'contentRating', 'audioLanguage', 'network', 'tag', 'label', 'series', 'resolution'];
const object = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const opaque = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,512}$/.test(v);
const safeCount = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const field = (v: unknown): v is SimpleFacetField => typeof v === 'string' && fields.includes(v);
const only = (v: Record<string, unknown>, keys: readonly string[]): boolean => Object.keys(v).every(key => keys.includes(key));

export class FacetWindowError extends Error {
  readonly code: 'invalid_facet_window' | 'invalid_facet_request' | 'facet_continuation_unavailable' | 'stale_continuation';
  readonly response?: PreparationResponse;
  constructor(code: FacetWindowError['code'], response?: PreparationResponse) {
    super(code === 'invalid_facet_window' ? unreadableServerResponse : code);
    this.code = code;this.response = response;
    this.name = 'FacetWindowError';
  }
}
function invalid(response?: PreparationResponse): never { throw new FacetWindowError('invalid_facet_window', response); }

/** Exact SIMPLE parser, separate from tolerant contextual/Reading FacetPage parsing.
 * No label fallback, trimming, row dropping, deduplication or scalar-length cap. */
export function parseSimpleFacetPage(
  input: unknown, expected: Readonly<{field?: SimpleFacetField; limit?: number}> = {},
  response?: PreparationResponse,
): SimpleFacetPage {
  if (!object(input) || !only(input, ['kind', 'field', 'revision', 'total', 'totalIsLowerBound', 'values', 'window'])
      || input.kind !== 'simple-keyset' || !field(input.field) || expected.field !== undefined && input.field !== expected.field
      || !opaque(input.revision) || !safeCount(input.total) || input.totalIsLowerBound !== false
      || !Array.isArray(input.values) || input.values.length > 200 || !object(input.window)) invalid(response);
  const w = input.window;
  if (!only(w, ['queryId', 'basis', 'limit', 'returned', 'next', 'previous', 'reload'])
      || !opaque(w.queryId) || !opaque(w.basis) || w.basis !== input.revision
      || !safeCount(w.limit) || w.limit < 1 || w.limit > 200
      || expected.limit !== undefined && w.limit !== expected.limit
      || !safeCount(w.returned) || w.returned !== input.values.length || w.returned > w.limit
      || input.total < w.returned) invalid(response);
  for (const key of ['next', 'previous', 'reload'] as const) {
    if (w[key] !== undefined && !opaque(w[key])) invalid(response);
  }
  if (w.returned === 0 ? w.next !== undefined || w.previous !== undefined || w.reload !== undefined : w.reload === undefined) invalid(response);
  const seen = new Set<string>();
  const values = input.values.map(value => {
    if (!object(value) || !only(value, ['value', 'label', 'count', 'countIsLowerBound', 'seriesId'])
        || typeof value.value !== 'string' || value.value.length === 0 || typeof value.label !== 'string'
        || !safeCount(value.count) || value.count < 1 || value.countIsLowerBound !== false || seen.has(value.value)
        || value.seriesId !== undefined && (typeof value.seriesId !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value.seriesId))) invalid(response);
    seen.add(value.value);
    return Object.freeze({value: value.value, label: value.label, count: value.count, countIsLowerBound: false as const,
      ...(value.seriesId !== undefined ? {seriesId: value.seriesId as string} : {})});
  });
  return Object.freeze({kind: 'simple-keyset', field: input.field, revision: input.revision,
    total: input.total, totalIsLowerBound: false, values: Object.freeze(values),
    window: Object.freeze({queryId: w.queryId, basis: w.basis, limit: w.limit, returned: w.returned,
      ...(w.next !== undefined ? {next: w.next as string} : {}),
      ...(w.previous !== undefined ? {previous: w.previous as string} : {}),
      ...(w.reload !== undefined ? {reload: w.reload as string} : {})})});
}

/** Preparing is a distinct domain envelope and never an empty ready page. */
export function parseSimpleFacetPreparing(input: unknown, response?: PreparationResponse): SimpleFacetPreparing {
  if (!object(input) || !only(input, ['kind', 'state', 'queryId', 'retryAfterMs'])
      || input.kind !== 'facet-preparing' || input.state !== 'preparing'
      || !opaque(input.queryId) || !safeCount(input.retryAfterMs) || input.retryAfterMs < 1) invalid(response);
  return Object.freeze({kind: 'facet-preparing', state: 'preparing', queryId: input.queryId, retryAfterMs: input.retryAfterMs});
}

/** Flattened Go FacetPage + preparation envelope. Every row is validated/preserved;
 * lower bounds are mandatory, never inferred. Empty is still a preparing fallback. */
export function parseSimpleFacetFallbackPage(
  input: unknown, expected: Readonly<{field?: SimpleFacetField; limit?: number}> = {},
  response?: PreparationResponse,
): SimpleFacetFallbackPage {
  if (!object(input) || !only(input, ['kind', 'field', 'revision', 'total', 'totalIsLowerBound', 'values', 'preparation'])
      || input.kind !== 'facet-fallback' || !field(input.field) || expected.field !== undefined && input.field !== expected.field
      || input.revision !== undefined && !opaque(input.revision)
      || !safeCount(input.total) || input.totalIsLowerBound !== true || !Array.isArray(input.values)
      || input.values.length > 200 || expected.limit !== undefined && input.values.length > expected.limit
      || input.total < input.values.length) invalid(response);
  const preparation = parseSimpleFacetPreparing(input.preparation, response);
  const seen = new Set<string>();
  const values = input.values.map(value => {
    if (!object(value) || !only(value, ['value', 'label', 'count', 'countIsLowerBound', 'seriesId'])
        || typeof value.value !== 'string' || value.value.length === 0 || typeof value.label !== 'string'
        || !safeCount(value.count) || value.countIsLowerBound !== true || seen.has(value.value)
        || value.seriesId !== undefined && (typeof value.seriesId !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value.seriesId))) invalid(response);
    seen.add(value.value);
    return Object.freeze({value: value.value, label: value.label, count: value.count, countIsLowerBound: true as const,
      ...(value.seriesId !== undefined ? {seriesId: value.seriesId as string} : {})});
  });
  return Object.freeze({kind: 'facet-fallback', field: input.field,
    ...(input.revision !== undefined ? {revision: input.revision as string} : {}),
    total: input.total, totalIsLowerBound: true, values: Object.freeze(values), preparation});
}

type CapturedRead = Readonly<SimpleFacetRead & {q: string; limit: number}>;
function capture(input: SimpleFacetRead): CapturedRead {
  if (!object(input) || !only(input, ['libraryId', 'field', 'q', 'limit', 'after', 'before', 'at'])
      || typeof input.libraryId !== 'string' || input.libraryId.length === 0 || input.libraryId === '*'
      || !field(input.field)) throw new FacetWindowError('invalid_facet_request');
  const q = input.q === undefined ? '' : input.q, limit = input.limit === undefined ? 200 : input.limit;
  if (typeof q !== 'string' || q.length > 100 || utf8Bytes(q, true) > 100
      || !safeCount(limit) || limit < 1 || limit > 200) throw new FacetWindowError('invalid_facet_request');
  const anchors = (['after', 'before', 'at'] as const).filter(key => input[key] !== undefined);
  if (anchors.length > 1 || anchors.some(key => !opaque(input[key]))) throw new FacetWindowError('invalid_facet_request');
  return Object.freeze({libraryId: input.libraryId, field: input.field, q, limit,
    ...Object.fromEntries(anchors.map(key => [key, input[key]]))});
}
function unanchored(read: CapturedRead): CapturedRead {
  return Object.freeze({libraryId: read.libraryId, field: read.field, q: read.q, limit: read.limit});
}

/** The generated SIMPLE library route only. No pivot/query/offset or across-library branch.
 * q is preserved exactly and bounded by UTF-8 bytes, not JS code units. */
export function simpleFacetPath(input: SimpleFacetRead): string {
  const read = capture(input);
  const query = new URLSearchParams({field: read.field, q: read.q, limit: String(read.limit)});
  for (const key of ['after', 'before', 'at'] as const) if (read[key] !== undefined) query.set(key, read[key]!);
  return `${routePath('get_library_facets', {id: read.libraryId})}?${query}`;
}

/** Avoid allocating another full UTF-8 copy just to decide whether a legal row is cacheable. */
function utf8Bytes(text: string, requireWellFormed = false): number {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    const unit = text.charCodeAt(i);
    if (unit < 0x80) bytes++;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xd800 && unit <= 0xdbff && i + 1 < text.length
      && text.charCodeAt(i + 1) >= 0xdc00 && text.charCodeAt(i + 1) <= 0xdfff) { bytes += 4; i++; }
    else {
      if (requireWellFormed && unit >= 0xd800 && unit <= 0xdfff) throw new FacetWindowError('invalid_facet_request');
      bytes += 3;
    }
  }
  return bytes;
}
function cacheable(page: SimpleFacetPage): boolean {
  // Conservative JSON-escaping/JS text charge, matching the server row estimate.
  let bytes = 1024 + 6 * (page.window.queryId.length + page.window.basis.length + page.revision.length
    + (page.window.next?.length ?? 0) + (page.window.previous?.length ?? 0) + (page.window.reload?.length ?? 0));
  for (const value of page.values) {
    bytes += 128 + 6 * (utf8Bytes(value.value) + utf8Bytes(value.label)) + (value.seriesId?.length ?? 0);
    if (bytes > SIMPLE_FACET_PAGE_BYTES) return false;
  }
  return bytes <= SIMPLE_FACET_PAGE_BYTES;
}

export type FacetReadSessionOptions = Readonly<{
  api: PreparationRequestPort;
  /** Optional generated adapters; defaults use get_facet_query/cancel_facet_query. During
   * root's pending generation callers can still supply these explicitly. They must return
   * exactly /v1/facet-queries/{queryId}; no raw Location navigation or route fallback. */
  paths?: Readonly<{poll(queryId: string): string; cancel(queryId: string): string}>;
  /** Explicit choice: true for a continuation owner; false for a one-shot count/preview. */
  retainReadyHandle: boolean;
  errorFromResponse(response: PreparationResponse): Error;
  isCurrent(): boolean;
  signal?: AbortSignal;
  runtime?: Partial<PreparationRuntime>;
  onPreparing?(pending: SimpleFacetPreparing, response: PreparationResponse): void;
  /** Transient bounded lower-bound rows, including legal oversized scalars. The session
   * never caches these rows or exposes fallback anchors. read() still awaits final exact. */
  onFallback?(fallback: SimpleFacetFallbackPage, response: PreparationResponse): void;
}>;
// These registered route IDs are added by root's pending apigen run. Keep the temporary
// type bridge here (not in generated files); routePath still fails closed if absent.
const facetQueryPath = routePath as unknown as (
  operation: 'get_facet_query' | 'cancel_facet_query', parameters: {id: string},
) => string;
const generatedQueryPaths = {
  poll: (queryId: string) => facetQueryPath('get_facet_query', {id: queryId}),
  cancel: (queryId: string) => facetQueryPath('cancel_facet_query', {id: queryId}),
};
type Operation = {controller: AbortController; detach(): void; release?: () => void; predecessor?: Operation};
const cancelled = () => Object.assign(new Error('facet-read-cancelled'), {name: 'AbortError'});

/** One SIMPLE domain/viewer session, one operation and at most one cached ordinary page.
 * Navigation is explicit, never a page-draining loop. Oversized pages are returned losslessly
 * but only their compact window/lease is retained. Call cancel on background, retire on scope
 * disposal. Fallback panels are callback-only and never retained. Parent renderers must also
 * bound their pages; this session retains no history. */
export class FacetReadSession {
  private readonly options: FacetReadSessionOptions;
  private operation?: Operation;
  private original?: CapturedRead;
  private heldPage?: SimpleFacetPage;
  private heldWindow?: SimpleFacetWindow;
  private heldTotal?: number;
  private closed = false;
  private readonly onExternalAbort = () => this.retire();

  constructor(options: FacetReadSessionOptions) {
    if (typeof options.retainReadyHandle !== 'boolean') throw new FacetWindowError('invalid_facet_request');
    this.options = options;
    options.signal?.addEventListener('abort', this.onExternalAbort, {once: true});
    if (options.signal?.aborted) this.retire();
  }
  private live(): boolean { return !this.closed && !this.options.signal?.aborted && this.options.isCurrent(); }
  private check(): void {
    if (!this.live()) { this.retire(); throw cancelled(); }
  }
  get page(): SimpleFacetPage | undefined { if (!this.live()) { this.retire(); return undefined; } return this.heldPage; }
  get window(): SimpleFacetWindow | undefined { if (!this.live()) { this.retire(); return undefined; } return this.heldWindow; }
  private clear(): void {
    const old = this.operation;
    this.operation = undefined;
    this.heldPage = undefined;
    this.heldWindow = undefined;
    this.heldTotal = undefined;
    this.dispose(old);
  }
  private dispose(operation: Operation | undefined): void {
    if (!operation) return;
    const predecessor = operation.predecessor;
    operation.predecessor = undefined;
    operation.detach(); operation.controller.abort(); operation.release?.();
    this.dispose(predecessor);
  }
  private releasePredecessor(operation: Operation): void {
    const predecessor = operation.predecessor;
    operation.predecessor = undefined;
    this.dispose(predecessor);
  }
  /** Cancel current interest and drop all ready rows/tokens; a later read may reuse this scope. */
  cancel(): void { this.clear(); }
  /** Retire permanently on server/profile/library-family/opening disposal. */
  retire(): void {
    this.closed = true;
    this.clear();
    this.original = undefined;
    this.options.signal?.removeEventListener('abort', this.onExternalAbort);
  }
  replace(input: SimpleFacetRead, signal?: AbortSignal): Promise<SimpleFacetPage> { return this.read(input, signal); }

  read(input: SimpleFacetRead, signal?: AbortSignal): Promise<SimpleFacetPage> { return this.start(input, signal); }
  private async start(input: SimpleFacetRead, signal?: AbortSignal, predecessor?: Operation, basis?: string, total?: number): Promise<SimpleFacetPage> {
    this.check();
    const descriptor = capture(input);
    // A replacement always retires old rows, tokens and handle before its request starts.
    if (predecessor) {
      // A continuation token names the old handle. Keep that one lease until the server
      // has consumed the token, then release it; deleting it before GET would force stale.
      this.operation = undefined; this.heldPage = undefined; this.heldWindow = undefined; this.heldTotal = undefined;
    } else this.clear();
    this.original = unanchored(descriptor);
    if (signal?.aborted) { this.dispose(predecessor); throw cancelled(); }
    const controller = new AbortController();
    const operation: Operation = {controller, predecessor, detach: () => signal?.removeEventListener('abort', onAbort)};
    const onAbort = () => { if (this.operation === operation) this.cancel(); else controller.abort(); };
    this.operation = operation;
    signal?.addEventListener('abort', onAbort, {once: true});
    const current = () => this.live() && this.operation === operation && !controller.signal.aborted;
    let reanchored = false;
    try {
      const result = await readPreparation<CapturedRead, SimpleFacetPage, SimpleFacetFallbackPage>({
        api: this.options.api, descriptor,
        paths: {
          initial: simpleFacetPath,
          refresh: read => {
            // Automatic stale re-anchor never retains the failed page's interest.
            this.heldPage = undefined; this.heldWindow = undefined; this.heldTotal = undefined;
            reanchored = true;
            this.releasePredecessor(operation);
            return simpleFacetPath(unanchored(read));
          },
          poll: id => this.domainPath('poll', id), cancel: id => this.domainPath('cancel', id),
        },
        parseReady: (body, response) => {
          const page = parseSimpleFacetPage(body, {field: descriptor.field, limit: descriptor.limit}, response);
          if (!reanchored && (basis !== undefined && page.window.basis !== basis || total !== undefined && page.total !== total)) {
            throw new FacetWindowError('stale_continuation', response);
          }
          return page;
        },
        parseIntermediateReady: (body, response) => {
          if (!object(body) || body.kind !== 'facet-fallback') return undefined;
          const fallback = parseSimpleFacetFallbackPage(body, {field: descriptor.field, limit: descriptor.limit}, response);
          this.releasePredecessor(operation);
          return {pending: fallback.preparation, value: fallback};
        },
        onIntermediateReady: (fallback, response) => {
          if (!current()) throw cancelled();
          this.options.onFallback?.(fallback, response);
        },
        readyQueryId: (body, response) => {
          // Minimal ready-handle validation also permits safe late-200 cleanup, without rows.
          if (!object(body) || body.field !== descriptor.field) invalid(response);
          if (body.kind === 'facet-fallback') {
            const preparation = parseSimpleFacetPreparing(body.preparation, response);
            this.releasePredecessor(operation);
            return preparation.queryId;
          }
          if (body.kind !== 'simple-keyset' || !object(body.window) || !opaque(body.window.queryId)) invalid(response);
          this.releasePredecessor(operation);
          return body.window.queryId;
        },
        parsePreparing: (body, response) => {
          const pending = parseSimpleFacetPreparing(body, response);
          this.releasePredecessor(operation);
          return pending;
        },
        retainOnReady: this.options.retainReadyHandle,
        errorFromResponse: this.options.errorFromResponse,
        signal: controller.signal, isCurrent: current, runtime: this.options.runtime,
        onPreparing: (pending, response) => {
          this.options.onPreparing?.(Object.freeze({kind: 'facet-preparing', state: 'preparing', ...pending}), response);
        },
      });
      if (!current()) { result.release(); throw cancelled(); }
      operation.release = result.release;
      this.heldPage = cacheable(result.value) ? result.value : undefined;
      this.heldWindow = this.options.retainReadyHandle ? result.value.window : undefined;
      this.heldTotal = this.options.retainReadyHandle ? result.value.total : undefined;
      if (!current()) { result.release(); throw cancelled(); }
      return result.value;
    } catch (error) {
      if (this.operation === operation) this.clear();
      if (controller.signal.aborted && !current()) {
        // Preserve actual current-read parser/HTTP errors; clear() above also aborts locally.
        if ((error as {name?: string} | null)?.name === 'AbortError') throw cancelled();
      }
      throw error;
    }
  }
  private domainPath(method: 'poll' | 'cancel', queryId: string): string {
    const path = (this.options.paths ?? generatedQueryPaths)[method](queryId);
    if (!opaque(queryId) || path !== generatedQueryPaths[method](queryId)) throw new FacetWindowError('invalid_facet_request');
    return path;
  }
  private async navigate(direction: 'next' | 'previous' | 'reload', signal?: AbortSignal): Promise<SimpleFacetPage> {
    this.check();
    const token = this.heldWindow?.[direction];
    if (!this.options.retainReadyHandle || !this.original || !token) return Promise.reject(new FacetWindowError('facet_continuation_unavailable'));
    const key = direction === 'next' ? 'after' : direction === 'previous' ? 'before' : 'at';
    return this.start({...this.original, [key]: token}, signal, this.operation, this.heldWindow?.basis, this.heldTotal);
  }
  next(signal?: AbortSignal): Promise<SimpleFacetPage> { return this.navigate('next', signal); }
  previous(signal?: AbortSignal): Promise<SimpleFacetPage> { return this.navigate('previous', signal); }
  reload(signal?: AbortSignal): Promise<SimpleFacetPage> { return this.navigate('reload', signal); }
  async reanchor(signal?: AbortSignal): Promise<SimpleFacetPage> {
    this.check();
    if (!this.original) return Promise.reject(new FacetWindowError('facet_continuation_unavailable'));
    return this.read(this.original, signal);
  }
}
