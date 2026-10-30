import {validReadingCover} from './reading-cover.ts';
import {decodeGetReadingSearchResponse, routePath, routePattern, routes, type GetReadingSearchResponse} from '../../contracts/src/index.ts';

/** The published global Reading-index anchor; never a session page or search group. */
export type ReadingIndexedSearchAnchor = Readonly<{normalization: 'xhtml-body-blockspace-fields-v1'; offsetUnit: 'utf16'; matchUnit: 'utf16'}>;
export type ReadingIndexedSearchHit = Readonly<{
  libraryId: string; itemId: string; sourceId: string; sourceRevision: string;
  title?: string; coverUrl?: string; sectionPath: string; sectionIndex: number;
  utf16Offset: number; excerpt: string; matchStart: number; matchLength: number;
}>;
export type ReadingIndexedSearchPage = Readonly<{
  indexedOnly: true; indexRevision: number; anchor: ReadingIndexedSearchAnchor;
  hits: readonly ReadingIndexedSearchHit[]; next?: string;
}>;
export type ReadingIndexedSearchRequest = Readonly<{query: string; libraryId?: string; cursor?: string; limit?: number}>;
export type ReadingIndexedSearchLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type ReadingIndexedSearchApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

const publishedRoutes: readonly Readonly<{id: string; method: string; path: string}>[] = routes;
const publicId = /^[A-Za-z0-9_-]{1,128}$/;
const cursorToken = /^[A-Za-z0-9_-]{1,512}$/;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length <= max;
const integer = (value: unknown, low: number, high = Number.MAX_SAFE_INTEGER): value is number => Number.isSafeInteger(value) && (value as number) >= low && (value as number) <= high;
const wellFormed = (value: string): boolean => {
  for (let index = 0; index < value.length; index++) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      if (++index >= value.length) return false;
      const low = value.charCodeAt(index);
      if (!(low >= 0xdc00 && low <= 0xdfff)) return false;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return false;
  }
  return true;
};
const splitSurrogate = (value: string, at: number): boolean => at > 0 && at < value.length
  && (value.charCodeAt(at - 1) & 0xfc00) === 0xd800 && (value.charCodeAt(at) & 0xfc00) === 0xdc00;
/** textIndexWordRune: unicode.IsLetter || IsNumber || IsMark. */
const wordRune = /[\p{L}\p{N}\p{M}]/u;
const searchable = (value: string): boolean => {
  for (const character of value) if (wordRune.test(character)) return true;
  return false;
};
const bad = (): never => {throw new Error('invalid_reading_indexed_search');};
const unavailable = (): never => {throw Object.assign(new Error('reading_indexed_search_unavailable'), {code: 'reading_indexed_search_unavailable'});};
const cancelled = (): never => {throw Object.assign(new Error('reading_indexed_search_cancelled'), {name: 'AbortError', code: 'cancelled'});};

/** The official path only; an older contract withholds the action. */
export function readingIndexedSearchPath(): string | undefined {
  const descriptor = publishedRoutes.find(route => route.id === 'get_reading_search');
  if (!descriptor || descriptor.method !== 'GET' || descriptor.path !== routePattern('get_reading_search')) return undefined;
  return routePath('get_reading_search');
}

/** The same member grammar as reading.ts: one relative archive path, no scheme/question/fragment. */
function member(value: unknown): value is string {
  return text(value, 1024) && value.length > 0 && wellFormed(value) && !/[\\\x00-\x1f\x7f?#]/.test(value)
    && !/^[a-z][a-z0-9+.-]*:/i.test(value) && value.split('/').every(part => part !== '' && part !== '.' && part !== '..');
}

/**
 * The shared literal-query shape sent as `q` and enforced by
 * textIndexValidateQuery: trimmed non-empty, well-formed UTF-16, at most 1024
 * UTF-16 units and 4096 encoded bytes, and at least one Letter, Number or Mark
 * rune (textIndexSearchable/textIndexWordRune). These are client bounds, not a
 * completeness or admission claim: the server still trims, folds, tokenizes and
 * applies its ready-membership predicate. Nothing is lowercased, expanded,
 * operator-parsed or rewritten here, so `artist: Miles` and `"quoted words"`
 * stay literal and punctuation- or emoji-only text with no L/N/M is refused.
 */
export function validReadingIndexedSearchQuery(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.trim().length > 0 && wellFormed(value)
    && value.length <= 1024 && new TextEncoder().encode(value).length <= 4096 && searchable(value);
}

/** A selected Reading cover is the versioned local route for this exact item. */

function sameAnchor(value: unknown): ReadingIndexedSearchAnchor | undefined {
  if (!record(value)) return undefined;
  const keys = Object.keys(value);
  if (keys.length !== 3 || !keys.includes('normalization') || !keys.includes('offsetUnit') || !keys.includes('matchUnit')) return undefined;
  if (value.normalization !== 'xhtml-body-blockspace-fields-v1' || value.offsetUnit !== 'utf16' || value.matchUnit !== 'utf16') return undefined;
  return Object.freeze({normalization: 'xhtml-body-blockspace-fields-v1', offsetUnit: 'utf16', matchUnit: 'utf16'});
}

function hit(value: GetReadingSearchResponse['hits'][number]): ReadingIndexedSearchHit {
  if (!publicId.test(value.libraryId) || !publicId.test(value.itemId) || !publicId.test(value.sourceId) || !/^[a-f0-9]{64}$/.test(value.sourceRevision)) return bad();
  if (!member(value.sectionPath) || !integer(value.sectionIndex, 0, 4095) || !integer(value.utf16Offset, 0, 8 << 20)) return bad(); // text_index_query.go scan budget / reading.ts
  if (!text(value.excerpt, 8192) || !wellFormed(value.excerpt) || !integer(value.matchStart, 0) || !integer(value.matchLength, 1)) return bad();
  if (value.matchStart + value.matchLength > value.excerpt.length) return bad();
  if (splitSurrogate(value.excerpt, value.matchStart) || splitSurrogate(value.excerpt, value.matchStart + value.matchLength)) return bad();
  if (value.title !== undefined && (!text(value.title, 4096) || !wellFormed(value.title) || /[\x00-\x1f\x7f]/.test(value.title))) return bad();
  if (value.coverUrl !== undefined && !validReadingCover(value.itemId, value.coverUrl)) return bad();
  return Object.freeze({libraryId: value.libraryId, itemId: value.itemId, sourceId: value.sourceId, sourceRevision: value.sourceRevision,
    sectionPath: value.sectionPath, sectionIndex: value.sectionIndex, utf16Offset: value.utf16Offset, excerpt: value.excerpt,
    matchStart: value.matchStart, matchLength: value.matchLength, ...(value.title === undefined ? {} : {title: value.title}),
    ...(value.coverUrl === undefined ? {} : {coverUrl: value.coverUrl})});
}

/** Parse one global indexed page. Options describe the request that produced it. */
export function parseReadingIndexedSearchPage(value: unknown, options: Readonly<{limit?: number; libraryId?: string}> = {}): ReadingIndexedSearchPage {
  if (!record(options) || Object.keys(options).some(key => key !== 'limit' && key !== 'libraryId')) return bad();
  if (options.limit !== undefined && !integer(options.limit, 1, 50)) return bad();
  if (options.libraryId !== undefined && (typeof options.libraryId !== 'string' || !publicId.test(options.libraryId))) return bad();
  let decoded: GetReadingSearchResponse;
  try { decoded = decodeGetReadingSearchResponse(value); } catch { return bad(); }
  if (decoded.indexedOnly !== true || !integer(decoded.indexRevision, 0) || !Array.isArray(decoded.hits) || decoded.hits.length > 50) return bad();
  const limit = options.limit ?? 20;
  if (decoded.hits.length > limit) return bad();
  const anchor = sameAnchor(decoded.anchor);
  if (!anchor) return bad();
  const hits: ReadingIndexedSearchHit[] = [];
  const positions = new Set<string>();
  for (const raw of decoded.hits) {
    const row = hit(raw);
    if (options.libraryId !== undefined && row.libraryId !== options.libraryId) return bad();
    const position = JSON.stringify([row.libraryId, row.itemId, row.sourceId, row.sourceRevision, row.sectionPath, row.sectionIndex, row.utf16Offset]);
    if (positions.has(position)) return bad();
    positions.add(position);
    hits.push(row);
  }
  if (decoded.next !== undefined && (typeof decoded.next !== 'string' || !cursorToken.test(decoded.next))) return bad();
  return Object.freeze({indexedOnly: true, indexRevision: decoded.indexRevision, anchor, hits: Object.freeze(hits),
    ...(decoded.next === undefined ? {} : {next: decoded.next})});
}

/** Copy only the accepted request fields; unknown keys are ignored and never
 * forwarded, and the caller's object is never mutated or referenced again. */
function requestOf(input: ReadingIndexedSearchRequest): ReadingIndexedSearchRequest {
  if (!record(input) || !validReadingIndexedSearchQuery(input.query)) return bad();
  if (input.libraryId !== undefined && (typeof input.libraryId !== 'string' || !publicId.test(input.libraryId))) return bad();
  if (input.cursor !== undefined && input.cursor !== '' && (typeof input.cursor !== 'string' || !cursorToken.test(input.cursor))) return bad();
  if (input.limit !== undefined && !integer(input.limit, 1, 50)) return bad();
  return Object.freeze({query: input.query, ...(input.libraryId === undefined ? {} : {libraryId: input.libraryId}),
    ...(input.cursor === undefined || input.cursor === '' ? {} : {cursor: input.cursor}), ...(input.limit === undefined ? {} : {limit: input.limit})});
}

/** Read the official global Reading-index page. No session/controller state is
 * owned here; the caller keeps the index-revision/page witness. A retiring or
 * throwing lifetime guard is never treated as authorization: it is cancelled. */
export async function readReadingIndexedSearch(api: ReadingIndexedSearchApi, request: ReadingIndexedSearchRequest, lifetime: ReadingIndexedSearchLifetime): Promise<ReadingIndexedSearchPage> {
  const input = requestOf(request), signal = lifetime.signal, isCurrent = lifetime.isCurrent;
  const check = () => {
    let retired = signal.aborted;
    if (!retired) {
      try { retired = !isCurrent(); } catch { retired = true; }
    }
    if (retired) throw cancelled();
  };
  check();
  const path = readingIndexedSearchPath();
  if (!path) throw unavailable();
  const params = new URLSearchParams({q: input.query, limit: String(input.limit ?? 20)});
  if (input.libraryId !== undefined) params.set('libraryId', input.libraryId);
  if (input.cursor !== undefined) params.set('cursor', input.cursor);
  const send = api.request.bind(api);
  let raw: unknown;
  try { raw = await send<unknown>(path + '?' + params, 'GET', undefined, signal); }
  catch (error) { check(); throw error; }
  check();
  const parsed = (): ReadingIndexedSearchPage => {
    try { return parseReadingIndexedSearchPage(raw, {limit: input.limit ?? 20, ...(input.libraryId === undefined ? {} : {libraryId: input.libraryId})}); }
    catch (error) { check(); throw error; }
  };
  const page = parsed();
  check();
  return page;
}
