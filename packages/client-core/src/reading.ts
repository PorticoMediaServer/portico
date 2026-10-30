import {validReadingCover} from './reading-cover.ts';
import {readingSavedGroupPath, parseReadingSavedGroupMembership, validReadingSavedGroupCommand, type ReadingSavedGroupTarget, type ReadingSavedGroupCommand} from './reading-saved-group.ts';
import {readingFavoritePath, parseReadingFavorite, validReadingFavoriteCommand, type ReadingFavoriteCommand} from './reading-favorite.ts';
export type {ReadingFavoriteFlag, ReadingFavoriteCommand} from './reading-favorite.ts';
import {readingSavedPath, parseReadingSavedMembership, validReadingSavedCommand, type ReadingListCommand} from './reading-saved.ts';
export type {ReadingListMembership, ReadingListCommand} from './reading-saved.ts';
import {withReadingConversion, withReadingContinueConversions, type ReadingConversion} from './reading-conversion.ts';
import {withReadingDiscoverMarks, type ReadingPreviewMarks} from './reading-preview-mark.ts';
import {readingMarkPath, parseReadingMark, validReadingMarkCommand, type ReadingReadMarkCommand} from './reading-mark.ts';
export type {ReadingReadMark, ReadingReadMarkCommand} from './reading-mark.ts';
import {decodeOpen} from '../../contracts/src/runtime.ts';
/** O-159 open tier (R-09) for the Reading browse reads: a book, series or issue carrying a format,
 * publication type or source this client does not know is left out; the rest of the page shows. */
const openRead = <T>(name: Parameters<typeof decodeOpen>[0]) => (value: unknown): T => decodeOpen(name, value) as T;
import {withReadingManifestFacts, type ReadingManifestFacts} from './reading-manifest-facts.ts';
import {captureReadingAnnotationMutation, parseReadingAnnotations, parseReadingAnnotationSaved, readingAnnotationId, type ReadingAnnotationMutation, type ReadingAnnotationsPage} from './reading-annotations.ts';
import {
  decodeGetReadingPeriodicalGroupsResponse, decodeGetReadingPeriodicalIssuesResponse,
  type GetReadingPeriodicalGroupsResponse, type GetReadingPeriodicalIssuesResponse,
  routePath, errors, decodeGetReadingSeriesItemsResponse, decodeGetReadingDiscoverResponse, decodeGetReadingContinueResponse, decodeGetReadingItemsResponse, decodeGetItemsItemIdReadingVersionsResponse,
  decodePostReadingSessionsResponse, decodeGetReadingSessionsSessionIdProgressResponse,
  decodePutReadingSessionsSessionIdProgressResponse, decodeGetReadingSessionsSessionIdSearchResponse,
  type GetReadingSeriesItemsResponse, type GetReadingDiscoverResponse, type GetReadingContinueResponse, type GetReadingItemsResponse, type GetItemsItemIdReadingVersionsResponse,
  type PostReadingSessionsRequest, type PostReadingSessionsResponse,
  type GetReadingSessionsSessionIdProgressResponse, type GetReadingSessionsSessionIdSearchResponse,
  type PutReadingSessionsSessionIdProgressRequest, type PutReadingSessionsSessionIdProgressResponse,
} from '../../contracts/src/index.ts';
import {schemas} from '../../contracts/src/schemas.ts';
import type {Viewer} from './index.ts';

export type ReadingScope = Readonly<{viewer: Readonly<Viewer>; isCurrent: () => boolean; signal?: AbortSignal}>;
type Immutable<T> = T extends readonly (infer V)[] ? readonly Immutable<V>[] : T extends object ? {readonly [K in keyof T]: Immutable<T[K]>} : T;
export type ReadingManifest = Immutable<PostReadingSessionsResponse & {canAnnotate?: true; canSaveFile?: true; conversion?:ReadingConversion; metadata: PostReadingSessionsResponse['metadata'] & ReadingManifestFacts}>;
/** Optional source capability is withheld unless the actual response says literal true. */
export function parseReadingManifest(raw: unknown): ReadingManifest {
  const {canAnnotate: decodedCapability, canSaveFile: decodedSaveCapability, ...value} = withReadingConversion(raw,value=>withReadingManifestFacts(value, decodePostReadingSessionsResponse)) as PostReadingSessionsResponse & {metadata: PostReadingSessionsResponse['metadata'] & ReadingManifestFacts; canAnnotate?: unknown; canSaveFile?: unknown; conversion?:ReadingConversion};
  void decodedCapability; void decodedSaveCapability;
  const offered = typeof raw === 'object' && raw !== null && !Array.isArray(raw) && (raw as Record<string, unknown>).canAnnotate === true;
  const saveOffered = typeof raw === 'object' && raw !== null && !Array.isArray(raw) && (raw as Record<string, unknown>).canSaveFile === true;
  return frozen({...value, ...(offered ? {canAnnotate: true as const} : {}), ...(saveOffered ? {canSaveFile: true as const} : {})});
}

export type ReadingSearchStatus = 'indexed' | 'pending' | 'unsupported' | 'evicted' | 'excluded';
export type ReadingSearchCoverage = Readonly<{status: ReadingSearchStatus; reason?: string}>;
/** One exact hit: utf16Offset is in the section's normalized text; matchStart/
 * matchLength are UTF-16 indices INSIDE excerpt. Neither is a CFI or DOM offset. */
export type ReadingSearchHit = Readonly<{sectionPath: string; sectionIndex: number; utf16Offset: number; excerpt: string; matchStart: number; matchLength: number}>;
export type ReadingSearchPage = Readonly<{sessionId: string; sourceId: string; sourceRevision: string; indexRevision: number; coverage: ReadingSearchCoverage; hits: readonly ReadingSearchHit[]; next?: string}>;
/** Retained original witness the response must match. Converted engine sessions
 * must supply the original source revision, not the converted output revision. */
export type ReadingSearchWitness = Readonly<{sessionId: string; sourceId: string; expectedRevision: string; format: string; converted: boolean; spine: readonly string[]; xhtml: (path: string) => boolean}>;

const readingSearchStatuses: readonly ReadingSearchStatus[] = ['indexed', 'pending', 'unsupported', 'evicted', 'excluded'];
const readingSearchCursor = /^[A-Za-z0-9_-]{1,512}$/;
const readingSearchStatus = (value: unknown): value is ReadingSearchStatus => typeof value === 'string' && readingSearchStatuses.includes(value as ReadingSearchStatus);
const splitSurrogate = (value: string, index: number) => index > 0 && index < value.length
  && (value.charCodeAt(index - 1) & 0xFC00) === 0xD800 && (value.charCodeAt(index) & 0xFC00) === 0xDC00;
function wellFormed(value: string): boolean {
  for (let index = 0; index < value.length; index++) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (++index >= value.length) return false;
      const low = value.charCodeAt(index);
      if (!(low >= 0xDC00 && low <= 0xDFFF)) return false;
    } else if (unit >= 0xDC00 && unit <= 0xDFFF) return false;
  }
  return true;
}

const boundedText = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}]/u.test(value) && wellFormed(value);
function readingSearchWitness(value: ReadingSearchWitness): boolean {
  return typeof value === 'object' && value !== null
    && id(value.sessionId) && boundedText(value.sourceId, 256) && revision(value.expectedRevision)
    && engineFormat(value.format) && typeof value.converted === 'boolean'
    && Array.isArray(value.spine) && value.spine.length <= 4096 && value.spine.every(path => typeof path === 'string' && member(path) && wellFormed(path))
    && typeof value.xhtml === 'function';
}

/** Public strict entry for an untrusted body: witness and limit first, then the
 * official decoder, then the cross-field checks. Unknown keys and malformed
 * shapes therefore fail as invalid_document rather than an incidental error. */
export function parseReadingSearchPage(raw: unknown, witness: ReadingSearchWitness, limit: number): ReadingSearchPage {
  if (!readingSearchWitness(witness)) fail('invalid_request');
  if (!integer(limit, 1, 50)) fail('invalid_request');
  let decoded: GetReadingSessionsSessionIdSearchResponse;
  try { decoded = decodeGetReadingSessionsSessionIdSearchResponse(raw); }
  catch { return fail('invalid_document'); }
  return parseDecodedReadingSearchPage(decoded, witness, limit);
}

/** Bounded decoded-only parser. IDs/revision must equal the retained witness;
 * nonindexed coverage never carries hits or a continuation; indexed hits must be
 * exact spine XHTML with safe UTF-16 ranges and one hit per section position. */
function parseDecodedReadingSearchPage(raw: GetReadingSessionsSessionIdSearchResponse, witness: ReadingSearchWitness, limit: number): ReadingSearchPage {
  if (!id(raw.sessionId) || !boundedText(raw.sourceId, 256) || !revision(raw.sourceRevision)) fail('invalid_document');
  if (raw.sessionId !== witness.sessionId || raw.sourceId !== witness.sourceId || raw.sourceRevision !== witness.expectedRevision) fail('invalid_document');
  if (!integer(raw.indexRevision, 0, Number.MAX_SAFE_INTEGER)) fail('invalid_document');
  const coverage = raw.coverage, status = coverage.status;
  if (!readingSearchStatus(status)) fail('invalid_document');
  if (coverage.reason !== undefined && (typeof coverage.reason !== 'string' || coverage.reason.length > 64 || !wellFormed(coverage.reason) || /[\p{Cc}]/u.test(coverage.reason))) fail('invalid_document');
  if (!Array.isArray(raw.hits) || raw.hits.length > limit) fail('invalid_document');
  const indexed = status === 'indexed';
  if (!indexed && raw.hits.length !== 0) fail('invalid_document');
  if (!indexed && raw.next !== undefined) fail('invalid_document');
  if (raw.next !== undefined && (typeof raw.next !== 'string' || !readingSearchCursor.test(raw.next))) fail('invalid_document');
  // A converted engine session or a non-EPUB source has no EPUB index
  // coordinates; only the explicit unsupported fallback is truthful.
  if ((witness.format !== 'epub' || witness.converted) && status !== 'unsupported') fail('invalid_document');
  const hits: ReadingSearchHit[] = [];
  const keys = new Set<string>();
  for (const hit of raw.hits) {
    if (typeof hit.sectionPath !== 'string' || !member(hit.sectionPath) || !wellFormed(hit.sectionPath) || !integer(hit.sectionIndex, 0, 4095)) fail('invalid_document');
    if (indexed && (witness.spine[hit.sectionIndex] !== hit.sectionPath || !witness.xhtml(hit.sectionPath))) fail('invalid_document');
    if (!integer(hit.utf16Offset, 0, 8 << 20)) fail('invalid_document');
    if (typeof hit.excerpt !== 'string' || hit.excerpt.length > 8192 || !wellFormed(hit.excerpt)) fail('invalid_document');
    if (!integer(hit.matchStart, 0, hit.excerpt.length) || !integer(hit.matchLength, 1, hit.excerpt.length) || hit.matchStart + hit.matchLength > hit.excerpt.length) fail('invalid_document');
    if (splitSurrogate(hit.excerpt, hit.matchStart) || splitSurrogate(hit.excerpt, hit.matchStart + hit.matchLength)) fail('invalid_document');
    // One literal query at one section position yields at most one hit; a
    // different excerpt range at the same path/index/offset is still a duplicate.
    const key = [hit.sectionPath, hit.sectionIndex, hit.utf16Offset].join('\u0000');
    if (keys.has(key)) fail('invalid_document');
    keys.add(key);
    hits.push(Object.freeze({sectionPath: hit.sectionPath, sectionIndex: hit.sectionIndex, utf16Offset: hit.utf16Offset, excerpt: hit.excerpt, matchStart: hit.matchStart, matchLength: hit.matchLength}));
  }
  return frozen({
    sessionId: raw.sessionId, sourceId: raw.sourceId, sourceRevision: raw.sourceRevision, indexRevision: raw.indexRevision,
    coverage: Object.freeze({status, ...(coverage.reason !== undefined ? {reason: coverage.reason} : {})}),
    hits: Object.freeze(hits), ...(raw.next !== undefined ? {next: raw.next} : {}),
  });
}

/** Derived from the generated official response, never a hand-written mirror. */
export type ReadingItem = GetReadingItemsResponse['items'][number];
export type ReadingContinueItem = GetReadingContinueResponse['items'][number] & Readonly<{conversion?:ReadingConversion}>;
export type ReadingDiscover = GetReadingDiscoverResponse;
export type ReadingShortRead = NonNullable<ReadingDiscover['rows'][number]['shortReads']>[number];
export type ReadingSeriesPage = GetReadingSeriesItemsResponse;
/** Checked presenter summary, distinct from the generated wire envelope. */
export type ReadingSeriesOverview = Readonly<{id: string; title: string; total: number; completed: number; author?: ReadingAuthor}>;
export type ReadingPeriodicalGroupsPage = GetReadingPeriodicalGroupsResponse;
export type ReadingPeriodicalIssuesPage = GetReadingPeriodicalIssuesResponse;
export type ReadingPeriodicalType = (typeof schemas)['GetReadingPeriodicalGroupsResponse']['properties']['publicationType']['enum'][number];
export type ReadingPeriodicalOrder = 'newest' | 'oldest';
export type ReadingPeriodicalGroup = ReadingPeriodicalGroupsPage['groups'][number];
/** Checked presenter choices projected from server year buckets, not a wire mirror. */
export type ReadingDayChoice = Readonly<{value: string; count: ReadingPeriodicalGroup['issueCount']}>;
export type ReadingYearChoice = Readonly<{value: string; count: ReadingPeriodicalGroup['issueCount']}>;
export type ReadingPeriodicalIssue = ReadingPeriodicalIssuesPage['items'][number];
export type ReadingAuthor = NonNullable<ReadingItem['author']>;
export type ReadingSeries = NonNullable<ReadingItem['series']>;
export type ReadingAttributionSource = ReadingAuthor['source'];
export type ReadingPage = Readonly<{index: number; name: string; bytes: Uint8Array; mime: 'image/jpeg' | 'image/png' | 'image/webp'}>;
export type ReadingLocation = PutReadingSessionsSessionIdProgressRequest['location'];
export type ReadingProgress = GetReadingSessionsSessionIdProgressResponse;
export type ReadingSavedProgress = PutReadingSessionsSessionIdProgressResponse;
export type ReadingByteReader = Pick<ReadableStreamDefaultReader<Uint8Array>, 'read' | 'cancel' | 'releaseLock'>;
/** Pull/cancel only; native hosts need no global Web Streams constructor.
 * Callers keep one read pending and release the reader after EOF/cancellation. */
export type ReadingByteStream = Readonly<{getReader(): ReadingByteReader; cancel(reason?: unknown): Promise<void>}>;

/** Errors contain a machine code/status, never an origin URL or response body. */
export class ReadingError extends Error {
  readonly code: string;
  readonly status?: number;
  constructor(code: string, status?: number) { super(code); this.name = 'ReadingError'; this.code = code; this.status = status; }
}
type Sender = (path: string, init: RequestInit) => Promise<Response>;
type Capture = Readonly<{acknowledge?: (key: string) => Promise<void>; send: Sender; isCurrent: () => boolean; subscribe: (retire: () => void) => () => void}>;
function fail(code: string): never { throw new ReadingError(code); }
const serverCodes = new Set<string>(errors.map(error => error.code));
const cancellation = (signal: AbortSignal) => signal.reason instanceof ReadingError ? signal.reason : new ReadingError('cancelled');
const integer = (n: number, low: number, high: number) => Number.isSafeInteger(n) && n >= low && n <= high;
const id = (s: string) => typeof s === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(s);
const revision = (s: string) => typeof s === 'string' && /^[0-9a-f]{64}$/.test(s);
const periodicalType = (s: string) => s === 'magazine' || s === 'newspaper';
const seriesID = (s: string) => /^[A-Za-z0-9_-]{43}$/.test(s);
const seriesCursor = (s: string) => /^[A-Za-z0-9_-]{171}\.[A-Za-z0-9_-]{43}$/.test(s);
function issueDate(value: string, kind: ReadingPeriodicalType): boolean {
  if (typeof value !== 'string' || !/^[0-9]{4}-[0-9]{2}(-[0-9]{2})?$/.test(value) || value.startsWith('0000') || kind === 'newspaper' && value.length !== 10) return false;
  const parsed = new Date((value.length === 7 ? value + '-01' : value) + 'T12:00:00Z');
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, value.length) === value;
}
function validateIssue(value: ReadingPeriodicalIssue, library: string, kind: ReadingPeriodicalType, group: string) {
  const item = value.item;
  if (!validReadingCover(item.id, item.coverUrl) || !id(item.id) || item.libraryId !== library || !id(item.sourceId) || item.format !== 'epub' && item.format !== 'pdf' || item.publicationType?.value !== kind || item.series?.id !== group || !issueDate(value.issueDate, kind)) fail('invalid_document');
}

/** The Discover rows that continue on their own. */
export type ReadingDiscoverRowKey = 'recent' | 'series' | 'short-reads' | 'new-issues';
const discoverRows: readonly string[] = ['recent', 'series', 'short-reads', 'new-issues'];
const discoverCursor = (s: string) => typeof s === 'string' && (/^-?[0-9]{1,19}\.[0-9]{1,19}$/.test(s) || seriesCursor(s));
/** Checks one paged Discover row and returns its issue cards. A row asked for
 * alone is always answered, so it may be empty; in a whole Discover answer only
 * Recent may be. A row with a continuation is never empty. */
function validateDiscoverRow(row: ReadingDiscover['rows'][number], libraryId: string | undefined, limit: number, alone = false): ReadingPeriodicalIssue[] {
  const checkItem = (item: ReadingItem, library?: string) => {
    if (!validReadingCover(item.id, item.coverUrl) || !id(item.id) || !id(item.libraryId) || !id(item.sourceId) || !sourceFormat(item.format) || (library !== undefined && item.libraryId !== library)) fail('invalid_document');
  };
  if ('newspaper' in row || row.nextCursor !== undefined && !discoverCursor(row.nextCursor)) fail('invalid_document');
  const ids = new Set<string>();
  const once = (value: string) => {if (ids.has(value)) fail('invalid_document'); ids.add(value);};
  const cards: ReadingPeriodicalIssue[] = [];
  let size: number;
  if (row.key === 'recent') {
    if (row.series !== undefined || row.shortReads !== undefined || 'newIssues' in row) fail('invalid_document');
    for (const item of row.items) {checkItem(item, libraryId); once(item.id);}
    size = row.items.length;
    if (row.nextCursor !== undefined && !size) fail('invalid_document');
  } else {
    if (libraryId === undefined || row.items.length !== 0) fail('invalid_document');
    if (row.key === 'new-issues') {
      const issues = 'newIssues' in row ? row.newIssues : alone ? [] : undefined;
      if (row.series !== undefined || row.shortReads !== undefined || !Array.isArray(issues)) fail('invalid_document');
      for (const raw of issues) {
        if (typeof raw !== 'object' || raw === null || typeof raw.issueDate !== 'string' || !issueDate(raw.issueDate, 'magazine')) fail('invalid_document');
        let item: ReadingItem;
        try { item = decodeGetReadingItemsResponse({items: [raw.item]}).items[0]; } catch { fail('invalid_document'); }
        checkItem(item, libraryId);
        if (!item.series || !seriesID(item.series.id) || !item.series.name.trim()) fail('invalid_document');
        const issue = {item, issueDate: raw.issueDate};
        validateIssue(issue, libraryId, 'magazine', item.series.id);
        once(item.id); cards.push(issue);
      }
      size = issues.length;
    } else if (row.key === 'short-reads') {
      const short = row.shortReads ?? (alone ? [] : fail('invalid_document'));
      if (row.series !== undefined || 'newIssues' in row) fail('invalid_document');
      for (const entry of short) {
        checkItem(entry.item, libraryId);
        if (entry.item.format !== 'epub' || !integer(entry.wordCount, 1, 44999) || !integer(entry.estimatedSeconds, 1, 10800) || entry.estimatedSeconds !== Math.ceil(entry.wordCount * 60 / 250)) fail('invalid_document');
        once(entry.item.id);
      }
      size = short.length;
    } else if (row.key === 'series') {
      const series = row.series ?? (alone ? [] : fail('invalid_document'));
      if (row.shortReads !== undefined || 'newIssues' in row) fail('invalid_document');
      const previews = new Set<string>();
      for (const summary of series) {
        if (!/^[A-Za-z0-9_-]{43}$/.test(summary.id) || summary.libraryId !== libraryId || typeof summary.name !== 'string' || summary.name.length < 1 || !integer(summary.count, 1, Number.MAX_SAFE_INTEGER) || !integer(summary.readCount, 0, summary.count) || summary.preview.length < 1 || summary.preview.length > Math.min(3, summary.count)) fail('invalid_document');
        once(summary.id);
        for (const item of summary.preview) {
          checkItem(item, summary.libraryId);
          if (item.series?.id !== summary.id || previews.has(item.id)) fail('invalid_document');
          previews.add(item.id);
        }
      }
      size = series.length;
    } else fail('invalid_document');
    if (!alone && size < 1 || row.nextCursor !== undefined && size < 1) fail('invalid_document');
  }
  if (size > limit) fail('invalid_document');
  return cards;
}

function isReadingObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function readingSeriesOverview(value: unknown, series: string, pageLength: number): ReadingSeriesOverview {
  if (!isReadingObject(value) || value.id !== series || typeof value.name !== 'string' || !value.name.trim() || [...value.name].length > 4096
    || typeof value.count !== 'number' || !integer(value.count, 1, Number.MAX_SAFE_INTEGER) || value.count < pageLength
    || typeof value.readCount !== 'number' || !integer(value.readCount, 0, value.count)) fail('invalid_document');
  let author: ReadingAuthor | undefined;
  if (value.author !== undefined) {
    const raw = value.author;
    if (!isReadingObject(raw) || typeof raw.value !== 'string' || !raw.value.trim() || [...raw.value].length > 4096 || typeof raw.source !== 'string') fail('invalid_document');
    const source = schemas.GetReadingItemsResponse.properties.items.items.properties.author.properties.source.enum.find(source => source === raw.source);
    if (source === undefined) fail('invalid_document');
    author = {value: raw.value, source};
  }
  return Object.freeze({id: series, title: value.name, total: value.count, completed: value.readCount, ...(author ? {author: Object.freeze(author)} : {})});
}

const comicFormat = (s: string) => s === 'cbz' || s === 'cbr';
const engineFormat = (s: string) => s === 'epub' || s === 'pdf' || comicFormat(s);
const convertedSource = (s:string) => s==='mobi'||s==='azw3';
const sourceFormat = (s:string) => engineFormat(s)||convertedSource(s);
const mime = (s: string | null) => s?.split(';', 1)[0].trim().toLowerCase();
const member = (s: string) => typeof s === 'string' && s.length > 0 && s.length <= 1024 && !/[\\\x00-\x1f\x7f?#]/.test(s) && !/^[a-z][a-z0-9+.-]*:/i.test(s) && s.split('/').every(p => p !== '' && p !== '.' && p !== '..');
const frozen = <T>(value: T): T => {
  if (typeof value === 'object' && value !== null && !Object.isFrozen(value)) { Object.values(value).forEach(frozen); Object.freeze(value); }
  return value;
};
const query = (path: string, values: Readonly<Record<string, string | number | undefined>>) => {
  const pairs = Object.entries(values).filter(([, v]) => v !== undefined).map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(String(v)));
  return path + (pairs.length ? '?' + pairs.join('&') : '');
};

// Local signal composition also works on iOS 15.1; no AbortSignal.any/timeout
// dependency, global polyfill or platform-specific bridge is introduced.
function operation(signals: readonly (AbortSignal | undefined)[]) {
  const controller = new AbortController();
  const abort = () => controller.abort(signals.find(signal => signal?.aborted)?.reason);
  for (const signal of signals) { signal?.addEventListener('abort', abort, {once: true}); if (signal?.aborted) abort(); }
  return {signal: controller.signal, release() { for (const signal of signals) signal?.removeEventListener('abort', abort); }};
}
function discard(response: Response) { try { void response.body?.cancel().catch(() => {}); } catch { /* already retired */ } }
async function received(pending: Promise<Response>, signal: AbortSignal): Promise<Response> {
  return new Promise<Response>((resolve, reject) => {
    const abort = () => reject(cancellation(signal));
    signal.addEventListener('abort', abort, {once: true});
    if (signal.aborted) abort();
    pending.then(response => {
      signal.removeEventListener('abort', abort);
      if (signal.aborted) { discard(response); reject(cancellation(signal)); }
      else resolve(response);
    }, error => { signal.removeEventListener('abort', abort); reject(error); });
  });
}

/** Bounded streaming consumption; cancellation also works with a stalled body. */
async function bytes(response: Response, max: number, signal: AbortSignal, check: () => void, exact?: number): Promise<Uint8Array> {
  const declared = response.headers.get('Content-Length');
  if (declared !== null && (!/^\d+$/.test(declared) || !integer(Number(declared), 0, max) || (exact !== undefined && Number(declared) !== exact))) {
    await discard(response); return fail('response_size');
  }
  if (!response.body) { if (exact === 0) { check(); return new Uint8Array(); } return fail('stream_unsupported'); }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0, rejectAbort!: (e: ReadingError) => void;
  const aborted = new Promise<never>((_, reject) => { rejectAbort = reject; });
  void aborted.catch(() => {});
  const abort = () => { void reader.cancel().catch(() => {}); rejectAbort(cancellation(signal)); };
  signal.addEventListener('abort', abort, {once: true});
  try {
    if (signal.aborted) abort();
    for (;;) {
      check();
      const next = await Promise.race([reader.read(), aborted]);
      check();
      if (next.done) break;
      if (!(next.value instanceof Uint8Array) || total + next.value.byteLength > max) return fail('response_size');
      total += next.value.byteLength; chunks.push(next.value.slice());
    }
    if ((declared !== null && total !== Number(declared)) || (exact !== undefined && total !== exact)) return fail('response_size');
    const out = new Uint8Array(total); let offset = 0;
    for (const chunk of chunks) { out.set(chunk, offset); offset += chunk.byteLength; }
    check(); return out;
  } finally { signal.removeEventListener('abort', abort); void reader.cancel().catch(() => {}); try { reader.releaseLock(); } catch { /* stream already ended */ } }
}

type ByteController = Pick<ReadableStreamDefaultController<Uint8Array>, 'enqueue' | 'close' | 'error'>;
type ByteSource = {start(controller: ByteController): void; pull(): Promise<void>; cancel(): void};

// Keep the fallback scoped to this body. It is a small pull protocol, not a
// global polyfill or an object cast to the complete DOM ReadableStream API.
function scopedByteStream(source: ByteSource): ReadingByteStream {
  if (typeof globalThis.ReadableStream === 'function') return new globalThis.ReadableStream<Uint8Array>(source, {highWaterMark: 0});
  type Result = ReadableStreamReadResult<Uint8Array>;
  type Pending = {resolve(value: Result): void; reject(error: unknown): void};
  let state: 'open' | 'closed' | 'errored' = 'open', failure: unknown, locked = false, pulling = false, pending: Pending | undefined;
  const finish = (error?: unknown, errored = false) => {
    if (state !== 'open') return;
    state = errored ? 'errored' : 'closed'; failure = error;
    const read = pending; pending = undefined;
    if (errored) read?.reject(error); else read?.resolve({done: true, value: undefined});
  };
  const controller: ByteController = {
    enqueue(value) {
      if (state !== 'open' || !pending || value === undefined) throw new TypeError('No pending reading pull');
      const read = pending; pending = undefined; read.resolve({done: false, value});
    },
    close() { finish(); },
    error(error) { finish(error, true); },
  };
  const pump = () => {
    if (pulling || !pending || state !== 'open') return;
    pulling = true;
    void Promise.resolve().then(() => source.pull()).catch(error => controller.error(error)).finally(() => { pulling = false; pump(); });
  };
  const cancel = (): Promise<void> => {
    if (state === 'errored') return Promise.reject(failure);
    if (state === 'closed') return Promise.resolve();
    finish();
    try { source.cancel(); return Promise.resolve(); } catch (error) { return Promise.reject(error); }
  };
  try { source.start(controller); } catch (error) { controller.error(error); }
  return Object.freeze({
    getReader(): ReadingByteReader {
      if (locked) throw new TypeError('Reading stream already locked');
      locked = true;
      let released = false;
      return Object.freeze({
        read(): Promise<Result> {
          if (released) return Promise.reject(new TypeError('Reading reader lock released'));
          if (state === 'errored') return Promise.reject(failure);
          if (state === 'closed') return Promise.resolve({done: true, value: undefined});
          if (pending) return Promise.reject(new ReadingError('stream_busy'));
          const result = new Promise<Result>((resolve, reject) => { pending = {resolve, reject}; });
          pump(); return result;
        },
        cancel() { return released ? Promise.reject(new TypeError('Reading reader lock released')) : cancel(); },
        releaseLock() {
          if (released) return;
          if (pending) throw new TypeError('Reading reader has a pending pull');
          released = true; locked = false;
        },
      });
    },
    cancel() { return locked ? Promise.reject(new TypeError('Reading stream already locked')) : cancel(); },
  });
}


function validateManifest(m: ReadingManifest, request: PostReadingSessionsRequest) {
  if (!id(m.sessionId) || m.itemId !== request.itemId || m.sourceId !== request.sourceId || !revision(m.revision) || !engineFormat(m.format) || !integer(m.size, 1, 2 ** 31)) fail('invalid_manifest');
  if (!validReadingCover(m.itemId, m.metadata.coverUrl)) fail('invalid_manifest');
  if (m.sequential === true && m.format !== 'pdf') fail('invalid_manifest');
  if (m.metadata.direction !== undefined && !['ltr', 'rtl', 'vertical'].includes(m.metadata.direction)) fail('invalid_manifest');
  const resources = m.resources ?? [], pages = m.pageNames ?? [], spine = m.spine ?? [];
  if (resources.length > 4096 || pages.length > 4096 || spine.length > 4096) fail('invalid_manifest');
  const paths = new Set<string>();
  for (const entry of resources) {
    if (!member(entry.path) || paths.has(entry.path) || !integer(entry.size, 0, 32 << 20) || !/^[a-z0-9][a-z0-9.+-]*\/[a-z0-9][a-z0-9.+-]*$/i.test(entry.mime)) fail('invalid_manifest');
    paths.add(entry.path);
  }
  if (spine.some(path => !paths.has(path)) || pages.some(path => !member(path)) || new Set(pages).size !== pages.length) fail('invalid_manifest');
  if (m.format === 'epub' ? resources.length === 0 || spine.length === 0 || pages.length !== 0 : comicFormat(m.format) ? pages.length === 0 || resources.length !== 0 || spine.length !== 0 : resources.length !== 0 || spine.length !== 0 || pages.length !== 0) fail('invalid_manifest');
  if (m.contents !== undefined) {
    if (m.format !== 'epub' || m.contents.length > 512) fail('invalid_manifest');
    for (const entry of m.contents) {
      if (!entry.title.trim() || entry.title.length > 1024 || /[\p{Cc}]/u.test(entry.title) || !member(entry.section) || !spine.includes(entry.section) || !integer(entry.depth, 0, 16)
        || entry.fragment !== undefined && (entry.fragment.length > 1024 || /[\p{Cc}]/u.test(entry.fragment))) fail('invalid_manifest');
    }
  }
}

/** Shared bridge validation delegates to the canonical current-manifest location policy. */
export function validateReadingLocation(location: ReadingLocation, manifest: ReadingManifest) {validateLocation(location, manifest);}
function validateLocation(location: ReadingLocation, manifest: ReadingManifest, stale = false) {
  if (location.format !== manifest.format || !Number.isFinite(location.progression) || location.progression < 0 || location.progression > 1) fail('invalid_location');
  if (location.format === 'epub') {
    if (location.pageIndex !== undefined || !location.cfi?.startsWith('epubcfi(/') || !location.cfi.endsWith(')') || new TextEncoder().encode(location.cfi).length > 4096 || /[\p{Cc}<>]/u.test(location.cfi)) fail('invalid_location');
    if (location.section !== undefined && (!member(location.section) || (!stale && !manifest.spine?.includes(location.section)))) fail('invalid_location');
  } else if (location.cfi !== undefined || location.section !== undefined || !integer(location.pageIndex ?? -1, 0, 2147483647) || (!stale && comicFormat(location.format) && location.pageIndex! >= manifest.pageNames!.length)) fail('invalid_location');
}
function validateProgress(value: ReadingProgress, manifest: ReadingManifest) {
  const p = value.progress;
  if (p !== null) {
    if (p.sourceId !== manifest.sourceId || !revision(p.sourceRevision) || p.stale !== (p.sourceRevision !== manifest.revision) || !integer(p.revision, 1, Number.MAX_SAFE_INTEGER) || !integer(p.observedAt, 1, Number.MAX_SAFE_INTEGER)) fail('invalid_progress');
    validateLocation(p.location, manifest, p.stale);
  }
}

/** A selected-server/viewer capture. Construction performs no requests. */
export class ReadingClient {
  readonly viewer: Readonly<Viewer>;
  readonly signal: AbortSignal;
  #capture: Capture;
  #scope: ReadingScope;
  #life = new AbortController();
  #sessions = new Set<ReadingSession>();
  #unsubscribe = () => {};
  #abort: () => void;
  constructor(capture: Capture, scope: ReadingScope) {
    this.#capture = Object.freeze({...capture}); this.viewer = Object.freeze({...scope.viewer}); this.#scope = Object.freeze({...scope, viewer: this.viewer}); this.signal = this.#life.signal;
    this.#abort = () => this.dispose(scope.signal?.reason); this.#unsubscribe = capture.subscribe(this.#abort);
    scope.signal?.addEventListener('abort', this.#abort, {once: true});
    if (scope.signal?.aborted || !capture.isCurrent() || !scope.isCurrent()) this.dispose();
    Object.freeze(this);
  }
  check(signal?: AbortSignal) {
    if (!this.#capture.isCurrent() || !this.#scope.isCurrent()) this.dispose();
    if (this.signal.aborted) throw cancellation(this.signal);
    if (signal?.aborted) throw cancellation(signal);
  }
  dispose(reason: unknown = new ReadingError('cancelled')) {
    if (this.signal.aborted) return;
    this.#life.abort(reason); this.#unsubscribe(); this.#scope.signal?.removeEventListener('abort', this.#abort);
    for (const session of this.#sessions) session.retire(); this.#sessions.clear();
  }
  forget(session: ReadingSession) { this.#sessions.delete(session); }
  async response<T>(path: string, init: RequestInit, signal: AbortSignal | undefined, consume: (r: Response, signal: AbortSignal, check: () => void) => Promise<T>, sessionSignal?: AbortSignal): Promise<T> {
    const op = operation([this.signal, sessionSignal, signal]);
    const check = () => this.check(op.signal);
    try {
      check(); const response = await received(this.#capture.send(path, {...init, signal: op.signal}), op.signal);
      try { check(); return await consume(response, op.signal, check); }
      catch (error) { await discard(response); throw error; }
    } catch (error) {
      if (op.signal.aborted) throw cancellation(op.signal);
      if (error instanceof ReadingError) throw error;
      throw new ReadingError('network_error');
    } finally { op.release(); }
  }
  async json<T>(path: string, method: string, body: unknown, signal: AbortSignal | undefined, decode: (value: unknown, recovered: boolean) => T, max: number, headers?: Record<string, string>, sessionSignal?: AbortSignal, sourceRevision?: (value: T) => string): Promise<T> {
    const payload = body === undefined ? undefined : JSON.stringify(body);
    if (payload !== undefined && new TextEncoder().encode(payload).length > 8192) fail('request_size');
    return this.response(path, {method, headers: {...headers, Accept: 'application/json', ...(payload === undefined ? {} : {'Content-Type': 'application/json'})}, ...(payload === undefined ? {} : {body: payload})}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.error(r, active, check);
      if (mime(r.headers.get('Content-Type')) !== 'application/json') return fail('invalid_response');
      const raw = await bytes(r, max, active, check);
      let value: T;
      try { value = frozen(decode(JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(raw)), r.headers.get('Portico-Mutation-Recovered') === 'true')); }
      catch { return fail('invalid_document'); }
      const tag = sourceRevision ? '"' + sourceRevision(value) + '"' : headers?.['If-Match'];
      if (tag !== undefined && r.headers.get('ETag') !== tag) fail('source_changed');
      const key = r.headers.get('Portico-Mutation-Key');
      if (key) await this.#capture.acknowledge?.(key);
      return value;
    }, sessionSignal);
  }
  async error(response: Response, signal: AbortSignal, check: () => void): Promise<never> {
    let code = 'request_failed';
    if (mime(response.headers.get('Content-Type')) === 'application/json') {
      try {
        const raw = JSON.parse(new TextDecoder().decode(await bytes(response, 64 << 10, signal, check)));
        if (typeof raw?.error?.code === 'string' && serverCodes.has(raw.error.code)) code = raw.error.code;
      } catch (error) { check(); /* Never retain an untrusted error body. */ }
    }
    await discard(response); throw new ReadingError(code, response.status);
  }
  canFavorite(itemId: string): boolean {
    const viewer = this.viewer;
    return !!viewer.authority && !!viewer.accountId && !!viewer.profileId && ['owner', 'admin', 'member'].includes(viewer.role)
      && readingFavoritePath('GET', itemId) !== undefined && readingFavoritePath('PUT', itemId) !== undefined;
  }
  async getFavorite(itemId: string, signal?: AbortSignal) {
    const path = readingFavoritePath('GET', itemId);
    if (!path) fail('unsupported');
    return this.json(path, 'GET', undefined, signal, (value, recovered) => parseReadingFavorite(value, this.viewer.serverId, itemId), 4096);
  }
  async setFavorite(itemId: string, command: ReadingFavoriteCommand, signal?: AbortSignal) {
    const path = readingFavoritePath('PUT', itemId);
    if (!path || !validReadingFavoriteCommand(command)) fail('invalid_request');
    const captured = Object.freeze({...command});
    return this.json(path, 'PUT', captured, signal, (value, recovered) => parseReadingFavorite(value, this.viewer.serverId, itemId, recovered ? undefined : captured.operationId), 4096);
  }
  canUseGroupMyList(target: ReadingSavedGroupTarget): boolean {
    const viewer = this.viewer;
    return !!viewer.authority && !!viewer.accountId && !!viewer.profileId && ['owner', 'admin', 'member'].includes(viewer.role)
      && readingSavedGroupPath('GET', target) !== undefined && readingSavedGroupPath('PUT', target) !== undefined;
  }
  async getGroupMyList(target: ReadingSavedGroupTarget, signal?: AbortSignal) {
    const captured = Object.freeze({...target}), path = readingSavedGroupPath('GET', captured);
    if (!path) fail('unsupported');
    return this.json(path, 'GET', undefined, signal, (value, recovered) => parseReadingSavedGroupMembership(value, this.viewer.serverId, captured), 4096);
  }
  async setGroupMyList(target: ReadingSavedGroupTarget, command: ReadingSavedGroupCommand, signal?: AbortSignal) {
    const capturedTarget = Object.freeze({...target}), captured = Object.freeze({...command}), path = readingSavedGroupPath('PUT', capturedTarget);
    if (!path || !validReadingSavedGroupCommand(captured)) fail('invalid_request');
    return this.json(path, 'PUT', captured, signal, (value, recovered) => parseReadingSavedGroupMembership(value, this.viewer.serverId, capturedTarget, recovered ? undefined : captured.operationId), 4096);
  }
  canUseMyList(itemId: string): boolean {
    const viewer = this.viewer;
    return !!viewer.authority && !!viewer.accountId && !!viewer.profileId && ['owner', 'admin', 'member'].includes(viewer.role)
      && readingSavedPath('GET', itemId) !== undefined && readingSavedPath('PUT', itemId) !== undefined;
  }
  async getMyList(itemId: string, signal?: AbortSignal) {
    const path = readingSavedPath('GET', itemId);
    if (!path) fail('unsupported');
    return this.json(path, 'GET', undefined, signal, (value, recovered) => parseReadingSavedMembership(value, this.viewer.serverId, itemId), 4096);
  }
  async setMyList(itemId: string, command: ReadingListCommand, signal?: AbortSignal) {
    const path = readingSavedPath('PUT', itemId);
    if (!path || !validReadingSavedCommand(command)) fail('invalid_request');
    const captured = Object.freeze({...command});
    return this.json(path, 'PUT', captured, signal, (value, recovered) => parseReadingSavedMembership(value, this.viewer.serverId, itemId, recovered ? undefined : captured.operationId), 4096);
  }
  canMarkRead(itemId: string): boolean {
    const viewer = this.viewer;
    return !!viewer.authority && !!viewer.accountId && !!viewer.profileId && ['owner', 'admin', 'member'].includes(viewer.role)
      && readingMarkPath('GET', itemId) !== undefined && readingMarkPath('PUT', itemId) !== undefined;
  }
  async readMark(itemId: string, signal?: AbortSignal) {
    const path = readingMarkPath('GET', itemId);
    if (!path) fail('unsupported');
    return this.json(path, 'GET', undefined, signal, (value, recovered) => parseReadingMark(value, this.viewer.serverId, itemId), 4096);
  }
  async setReadMark(itemId: string, command: ReadingReadMarkCommand, signal?: AbortSignal) {
    const path = readingMarkPath('PUT', itemId);
    if (!path || !validReadingMarkCommand(command)) fail('invalid_request');
    const captured = Object.freeze({...command});
    return this.json(path, 'PUT', captured, signal, (value, recovered) => parseReadingMark(value, this.viewer.serverId, itemId, recovered ? undefined : captured.operationId), 4096);
  }
  async list(options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<GetReadingItemsResponse> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 50, 1, 100) || (options.cursor !== undefined && !/^\d{1,20}$/.test(options.cursor))) fail('invalid_request');
    const out = await this.json(query(routePath('get_reading_items'), options), 'GET', undefined, signal, openRead<GetReadingItemsResponse>('GetReadingItemsResponse'), 256 << 10);
    if (out.items.length > 100 || out.items.some(row => !id(row.id) || !id(row.sourceId) || !sourceFormat(row.format) || !validReadingCover(row.id, row.coverUrl))) fail('invalid_document');
    return out;
  }
  async discover(options: Readonly<{libraryId?: string; limit?: number; seriesLimit?: number; shortReadsLimit?: number; newIssuesLimit?: number; newspaperLimit?: number}> = {}, signal?: AbortSignal): Promise<ReadingDiscover & {newIssueCards: readonly ReadingPeriodicalIssue[]; newspaperCard?: ReadingPeriodicalGroup; previewMarks: ReadingPreviewMarks}> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 20, 1, 100) || options.seriesLimit !== undefined && (options.libraryId === undefined || !id(options.libraryId) || !integer(options.seriesLimit,1,20))) fail('invalid_request');
    if (options.shortReadsLimit !== undefined && (options.libraryId === undefined || !id(options.libraryId) || !integer(options.shortReadsLimit, 1, 20))) fail('invalid_request');
    if (options.newIssuesLimit !== undefined && (options.libraryId === undefined || !id(options.libraryId) || !integer(options.newIssuesLimit, 1, 20))) fail('invalid_request');
    if (options.newspaperLimit !== undefined && (options.libraryId === undefined || !id(options.libraryId) || options.newspaperLimit !== 1)) fail('invalid_request');
    const decoded = await this.json(query(routePath('get_reading_discover'), options), 'GET', undefined, signal,
      raw => withReadingDiscoverMarks(raw, openRead<GetReadingDiscoverResponse>('GetReadingDiscoverResponse')), 2 << 20), out = decoded.value;
    if (out.rows.length < 1 || out.rows.length > 5 || out.rows[0].key !== 'recent') fail('invalid_document');
    validateDiscoverRow(out.rows[0], options.libraryId, options.limit ?? 20);
    let newspaperCard: ReadingPeriodicalGroup | undefined;
    let newIssueCards: ReadingPeriodicalIssue[] = [];
    // Rows keep the server's fixed order; each appears once and only when asked for.
    const order = ['series', 'short-reads', 'new-issues', 'newspaper'] as const;
    const limits = {'series': options.seriesLimit, 'short-reads': options.shortReadsLimit, 'new-issues': options.newIssuesLimit, 'newspaper': options.newspaperLimit};
    let at = 0;
    for (const row of out.rows.slice(1)) {
      const index = order.indexOf(row.key as typeof order[number]);
      const limit = index < 0 ? undefined : limits[order[index]];
      if (index < at || limit === undefined) fail('invalid_document');
      at = index + 1;
      if (row.key === 'newspaper') {
        if (row.series !== undefined || row.shortReads !== undefined || 'newIssues' in row || row.items.length !== 0 || !('newspaper' in row) || row.nextCursor !== undefined) fail('invalid_document');
        let group: ReadingPeriodicalGroup;
        try { group = decodeGetReadingPeriodicalGroupsResponse({libraryId: options.libraryId, publicationType: 'newspaper', groups: [row.newspaper]}).groups[0]; } catch { fail('invalid_document'); }
        if (!seriesID(group.id) || !group.name.trim() || !integer(group.issueCount, 1, Number.MAX_SAFE_INTEGER)) fail('invalid_document');
        validateIssue(group.latest, options.libraryId ?? fail('invalid_request'), 'newspaper', group.id);
        newspaperCard = group;
        continue;
      }
      const cards = validateDiscoverRow(row, options.libraryId, limit);
      if (row.key === 'new-issues') newIssueCards = cards;
    }
    return {...out, newIssueCards, newspaperCard, previewMarks: decoded.marks};
  }
  /** One Discover row alone: its first page, or its next page from that row's
   * own `nextCursor`. A page may be shorter than its limit when its entries are
   * large; a `nextCursor` still means there is more. */
  async discoverRow(row: ReadingDiscoverRowKey, options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<Readonly<{row: ReadingDiscover['rows'][number]; newIssueCards: readonly ReadingPeriodicalIssue[]; previewMarks: ReadingPreviewMarks}>> {
    const limit = options.limit ?? 20;
    if (!discoverRows.includes(row) || options.libraryId !== undefined && !id(options.libraryId) || row !== 'recent' && options.libraryId === undefined
      || !integer(limit, 1, row === 'recent' ? 100 : 20) || options.cursor !== undefined && !discoverCursor(options.cursor)) fail('invalid_request');
    const decoded = await this.json(query(routePath('get_reading_discover'), {...options, row}), 'GET', undefined, signal,
      raw => withReadingDiscoverMarks(raw, openRead<GetReadingDiscoverResponse>('GetReadingDiscoverResponse')), 2 << 20), out = decoded.value;
    if (out.rows.length !== 1 || out.rows[0].key !== row) fail('invalid_document');
    const newIssueCards = validateDiscoverRow(out.rows[0], options.libraryId, limit, true);
    return Object.freeze({row: out.rows[0], newIssueCards, previewMarks: decoded.marks});
  }
  async periodicalGroups(libraryId: string, publicationType: ReadingPeriodicalType, options: Readonly<{cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<ReadingPeriodicalGroupsPage> {
    const limit = options.limit ?? 24;
    if (!id(libraryId) || !periodicalType(publicationType) || !integer(limit, 1, 100) || options.cursor !== undefined && !seriesCursor(options.cursor)) fail('invalid_request');
    const out = await this.json(query(routePath('get_reading_periodical_groups', {libraryId}), {...options, publicationType}), 'GET', undefined, signal, decodeGetReadingPeriodicalGroupsResponse, 2 << 20);
    if (out.libraryId !== libraryId || out.publicationType !== publicationType || out.groups.length > limit || out.nextCursor !== undefined && (!seriesCursor(out.nextCursor) || out.groups.length < 1)) fail('invalid_document');
    const groups = new Set<string>(), latest = new Set<string>();
    for (const group of out.groups) {
      if (!seriesID(group.id) || groups.has(group.id) || typeof group.name !== 'string' || !group.name.trim() || group.name.length > 4096 || !integer(group.issueCount, 1, Number.MAX_SAFE_INTEGER) || latest.has(group.latest.item.id)) fail('invalid_document');
      validateIssue(group.latest, libraryId, publicationType, group.id);
      groups.add(group.id); latest.add(group.latest.item.id);
    }
    return out;
  }
  /** Issues in the archive's actual date order: `newest` first (the default) or
   * `oldest` first. The order is bound into the cursor, so a cursor continues
   * only the order it came from. */
  async periodicalIssues(libraryId: string, seriesId: string, publicationType: ReadingPeriodicalType, options: Readonly<{cursor?: string; limit?: number; issueDate?: string; year?: string; month?: string; order?: ReadingPeriodicalOrder}> = {}, signal?: AbortSignal): Promise<ReadingPeriodicalIssuesPage & {yearChoices: readonly ReadingYearChoice[]; dayChoices: readonly ReadingDayChoice[]; calendarMonth?: string}> {
    const limit = options.limit ?? 50;
    if (options.order !== undefined && options.order !== 'newest' && options.order !== 'oldest') fail('invalid_request');
    const oldest = options.order === 'oldest';
    if (options.month !== undefined && (publicationType !== 'newspaper' || options.month.length !== 7 || !issueDate(options.month, 'magazine') || options.year !== undefined && !options.month.startsWith(options.year + '-') || options.issueDate !== undefined && !options.issueDate.startsWith(options.month + '-'))) fail('invalid_request');
    if (options.year !== undefined && (!/^[0-9]{4}$/.test(options.year) || !issueDate(options.year + '-01', 'magazine') || options.issueDate !== undefined && !options.issueDate.startsWith(options.year + '-')) || options.issueDate !== undefined && !issueDate(options.issueDate, publicationType) || !id(libraryId) || !seriesID(seriesId) || !periodicalType(publicationType) || !integer(limit, 1, 100) || options.cursor !== undefined && !seriesCursor(options.cursor)) fail('invalid_request');
    const out = await this.json(query(routePath('get_reading_periodical_issues', {libraryId, seriesId}), {...options, publicationType}), 'GET', undefined, signal, decodeGetReadingPeriodicalIssuesResponse, 2 << 20);
    if ((options.issueDate === undefined ? 'issueDate' in out : !('issueDate' in out) || out.issueDate !== options.issueDate) || out.libraryId !== libraryId || out.seriesId !== seriesId || out.publicationType !== publicationType || out.items.length > limit || out.nextCursor !== undefined && (!seriesCursor(out.nextCursor) || out.items.length < 1)) fail('invalid_document');
    if (options.year === undefined ? 'year' in out : !('year' in out) || out.year !== options.year) fail('invalid_document');
    if (options.month === undefined ? 'month' in out : !('month' in out) || out.month !== options.month) fail('invalid_document');
    const dayChoices: ReadingDayChoice[] = [];
    const days = 'days' in out ? out.days : [];
    if (!Array.isArray(days) || days.length > 31 || options.month === undefined && days.length !== 0) fail('invalid_document');
    for (const day of days) {
      if (typeof day !== 'object' || day === null || typeof day.date !== 'string' || !issueDate(day.date, 'newspaper') || !day.date.startsWith(options.month + '-') || !integer(day.issueCount, 1, Number.MAX_SAFE_INTEGER) || dayChoices.length && dayChoices[dayChoices.length - 1].value >= day.date) fail('invalid_document');
      dayChoices.push({value: day.date, count: day.issueCount});
    }
    const yearChoices: ReadingYearChoice[] = [];
    const buckets = 'years' in out ? out.years : [];
    if (!Array.isArray(buckets) || buckets.length > 9999) fail('invalid_document');
    for (const bucket of buckets) {
      if (typeof bucket !== 'object' || bucket === null || typeof bucket.year !== 'string' || !/^[0-9]{4}$/.test(bucket.year) || !issueDate(bucket.year + '-01', 'magazine') || !integer(bucket.issueCount, 1, Number.MAX_SAFE_INTEGER) || yearChoices.length && yearChoices[yearChoices.length - 1].value <= bucket.year) fail('invalid_document');
      yearChoices.push({value: bucket.year, count: bucket.issueCount});
    }
    const seen = new Set<string>(); let previous: string | undefined;
    for (const entry of out.items) {
      validateIssue(entry, libraryId, publicationType, seriesId);
      if (options.month !== undefined && !entry.issueDate.startsWith(options.month + '-')) fail('invalid_document');
      if (options.year !== undefined && !entry.issueDate.startsWith(options.year + '-')) fail('invalid_document');
      if (options.issueDate !== undefined && entry.issueDate !== options.issueDate) fail('invalid_document');
      if (seen.has(entry.item.id) || previous !== undefined && (oldest ? previous > entry.issueDate : previous < entry.issueDate)) fail('invalid_document');
      seen.add(entry.item.id); previous = entry.issueDate;
    }
    return {...out, yearChoices, dayChoices, calendarMonth: options.month};
  }
  async series(libraryId: string, seriesId: string, options: Readonly<{cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<ReadingSeriesPage & {overview?: ReadingSeriesOverview}> {
    const cursor = options.cursor;
    if (!id(libraryId) || !/^[A-Za-z0-9_-]{43}$/.test(seriesId) || !integer(options.limit ?? 50, 1, 100) || (cursor !== undefined && !/^[A-Za-z0-9_-]{171}\.[A-Za-z0-9_-]{43}$/.test(cursor))) fail('invalid_request');
    const out = await this.json(query(routePath('get_reading_series_items', {libraryId, seriesId}), options), 'GET', undefined, signal, value => {
      const page = openRead<GetReadingSeriesItemsResponse>('GetReadingSeriesItemsResponse')(value);
      // Older official decoders omit unknown optional fields. Preserve only the
      // separately checked presenter projection, never an unchecked wire object.
      const overview = isReadingObject(value) && value.summary !== undefined ? readingSeriesOverview(value.summary, seriesId, page.items.length) : undefined;
      return {...page, ...(overview ? {overview} : {})};
    }, 2 << 20);
    if (out.items.length > (options.limit ?? 50) || (out.nextCursor !== undefined && !/^[A-Za-z0-9_-]{171}\.[A-Za-z0-9_-]{43}$/.test(out.nextCursor))) fail('invalid_document');
    const seen = new Set<string>();
    for (const item of out.items) {
      if (!validReadingCover(item.id, item.coverUrl) || !id(item.id) || item.libraryId !== libraryId || !id(item.sourceId) || !sourceFormat(item.format) || item.series?.id !== seriesId || seen.has(item.id)) fail('invalid_document');
      seen.add(item.id);
    }
    const overview = out.overview;
    if (overview && out.nextCursor && overview.total <= out.items.length) fail('invalid_document');
    return {...out, ...(overview ? {overview} : {})};
  }
  async continue(options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<Omit<GetReadingContinueResponse,'items'> & {items:ReadingContinueItem[]}> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 50, 1, 100) || (options.cursor !== undefined && !/^[A-Za-z0-9_-]{1,128}$/.test(options.cursor))) fail('invalid_request');
    const out = await this.json(query(routePath('get_reading_continue'), options), 'GET', undefined, signal, raw=>withReadingContinueConversions(raw,openRead<GetReadingContinueResponse>('GetReadingContinueResponse')), 2 << 20);
    if (out.items.length > (options.limit ?? 50) || (out.nextCursor !== undefined && !/^[A-Za-z0-9_-]{1,128}$/.test(out.nextCursor))) fail('invalid_document');
    const seen = new Set<string>();
    for (const {item, progress, conversion} of out.items) {
      const location = progress.location;
      if (!validReadingCover(item.id, item.coverUrl) || !id(item.id) || !id(item.libraryId) || !id(item.sourceId) || !sourceFormat(item.format) || seen.has(item.id) || progress.sourceId !== item.sourceId || !revision(progress.sourceRevision) || !integer(progress.revision, 1, Number.MAX_SAFE_INTEGER) || !integer(progress.observedAt, 1, Number.MAX_SAFE_INTEGER) || !engineFormat(location.format) || (!progress.stale && location.format !== item.format && !(convertedSource(item.format)&&location.format==='epub'&&conversion?.revision===progress.sourceRevision)) || !Number.isFinite(location.progression) || location.progression < 0 || location.progression >= 1) fail('invalid_progress');
      if (location.format === 'epub') {
        if (location.pageIndex !== undefined || !location.cfi?.startsWith('epubcfi(/') || !location.cfi.endsWith(')') || new TextEncoder().encode(location.cfi).length > 4096 || /[\p{Cc}<>]/u.test(location.cfi) || (location.section !== undefined && !member(location.section))) fail('invalid_progress');
      } else if (location.cfi !== undefined || location.section !== undefined || !integer(location.pageIndex ?? -1, 0, 2147483647)) fail('invalid_progress');
      seen.add(item.id);
    }
    return out;
  }
  async versions(itemId: string, signal?: AbortSignal): Promise<GetItemsItemIdReadingVersionsResponse> {
    if (!id(itemId)) fail('invalid_request');
    const out = await this.json(routePath('get_items_itemId_reading_versions', {itemId}), 'GET', undefined, signal, decodeGetItemsItemIdReadingVersionsResponse, 64 << 10);
    if (out.versions.length > 128 || out.versions.some(v => !id(v.sourceId) || !sourceFormat(v.format) || !integer(v.size, 0, 2 ** 31))) fail('invalid_document');
    // The server's chosen version must be one of the versions it listed.
    if (out.chosenSourceId !== undefined && !out.versions.some(v => v.sourceId === out.chosenSourceId) || out.chosenSourceId === undefined && out.versions.length) fail('invalid_document');
    return out;
  }
  async open(request: PostReadingSessionsRequest, signal?: AbortSignal): Promise<ReadingSession> {
    if (!id(request.itemId) || !id(request.sourceId)) fail('invalid_request');
    const captured = Object.freeze({itemId: request.itemId, sourceId: request.sourceId});
    const manifest = await this.json(routePath('post_reading_sessions'), 'POST', captured, signal, parseReadingManifest, 8 << 20, undefined, undefined, m => m.revision);
    validateManifest(manifest, captured); this.check(signal);
    const session = new ReadingSession(this, manifest); this.#sessions.add(session); return session;
  }
}

/** Native byte callbacks carry no credentials or origin URLs into an engine. */
export class ReadingSession {
  readonly manifest: ReadingManifest;
  readonly signal: AbortSignal;
  #client: ReadingClient;
  #life = new AbortController();
  #activeRanges = 0;
  #activeSequential = false;
  #savingFile = false;
  #retire: () => void;
  constructor(client: ReadingClient, manifest: ReadingManifest) {
    this.#client = client; this.manifest = frozen(manifest); this.signal = this.#life.signal;
    this.#retire = () => this.retire(client.signal.reason); client.signal.addEventListener('abort', this.#retire, {once: true});
    Object.freeze(this);
  }
  retire(reason: unknown = new ReadingError('cancelled')) { this.#life.abort(reason); this.#client.signal.removeEventListener('abort', this.#retire); this.#client.forget(this); }
  #check = () => { this.#client.check(this.signal); };
  #headers() { return {'If-Match': '"' + this.manifest.revision + '"'}; }
  #etag(response: Response) { if (response.headers.get('ETag') !== '"' + this.manifest.revision + '"') fail('source_changed'); }
  async #control<T>(run: () => Promise<T>, cas = false): Promise<T> {
    this.#check();
    try { return await run(); }
    catch (error) {
      if (error instanceof ReadingError && (['source_changed', 'invalid_document', 'invalid_progress'].includes(error.code) || [401, 403, 404].includes(error.status ?? 0) || (!cas && error.status === 412))) this.retire(error);
      throw error;
    }
  }
  async #binary<T>(path: string, init: RequestInit, signal: AbortSignal | undefined, consume: (r: Response, active: AbortSignal, check: () => void) => Promise<T>) {
    this.#check();
    try { return await this.#client.response(path, {...init, headers: {...this.#headers(), ...init.headers}}, signal, consume, this.signal); }
    catch (error) {
      if (error instanceof ReadingError && (['source_changed', 'response_size', 'invalid_response'].includes(error.code) || [401, 403, 404, 412].includes(error.status ?? 0))) this.retire(error);
      throw error;
    }
  }
  headPDF = async (signal?: AbortSignal): Promise<void> => {
    if (this.manifest.format !== 'pdf') fail('invalid_format');
    return this.#binary(routePath('head_reading_sessions_sessionId_source', {sessionId: this.manifest.sessionId}), {method: 'HEAD'}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      this.#etag(r);
      if (r.headers.get('Content-Length') !== String(this.manifest.size) || r.headers.get('Accept-Ranges') !== (this.manifest.sequential ? 'none' : 'bytes') || mime(r.headers.get('Content-Type')) !== 'application/pdf') fail('source_changed');
      check(); await discard(r);
    });
  };
  readRange = async (range: Readonly<{start: number; endExclusive: number}>, signal?: AbortSignal): Promise<Uint8Array> => {
    if (this.manifest.format !== 'pdf' || this.manifest.sequential) fail('invalid_format');
    const {start, endExclusive} = range, length = endExclusive - start;
    if (!integer(start, 0, this.manifest.size - 1) || !integer(endExclusive, start + 1, this.manifest.size) || length > 64 << 10) fail('invalid_range');
    if (this.#activeRanges >= 2) fail('range_busy');
    this.#activeRanges++;
    try {
      return await this.#binary(routePath('get_reading_sessions_sessionId_source', {sessionId: this.manifest.sessionId}), {method: 'GET', headers: {Range: `bytes=${start}-${endExclusive - 1}`}}, signal, async (r, active, check) => {
        if (r.status !== 206) { if (r.status === 200) return fail('invalid_response'); return this.#client.error(r, active, check); }
        this.#etag(r);
        if (r.headers.get('Content-Range') !== `bytes ${start}-${endExclusive - 1}/${this.manifest.size}` || r.headers.get('Content-Length') !== String(length) || mime(r.headers.get('Content-Type')) !== 'application/pdf') fail('source_changed');
        return bytes(r, length, active, check, length);
      });
    } finally { this.#activeRanges--; }
  };
  /** One genuine from-start200 stream; it is never a ranged or buffered source. */
  openSequential = async (signal?: AbortSignal): Promise<ReadingByteStream> => {
    if (this.manifest.format !== 'pdf' || !this.manifest.sequential) fail('invalid_format');
    this.#check();
    if (this.#activeSequential) fail('sequential_busy');
    this.#activeSequential = true;
    const op = operation([this.#client.signal, this.signal, signal]);
    let handedOff = false;
    const release = () => { this.#activeSequential = false; op.release(); };
    try {
      return await this.#binary(routePath('get_reading_sessions_sessionId_source', {sessionId: this.manifest.sessionId}), {method: 'GET', headers: {Accept: 'application/pdf'}}, op.signal, async (r, active, check) => {
        if (r.status !== 200) { if (r.status === 206) return fail('invalid_response'); return this.#client.error(r, active, check); }
        this.#etag(r);
        if (r.headers.get('Content-Length') !== String(this.manifest.size) || r.headers.has('Content-Range') || r.headers.get('Accept-Ranges') !== 'none' || mime(r.headers.get('Content-Type')) !== 'application/pdf') fail('source_changed');
        if (!r.body) return fail('stream_unsupported');
        const reader = r.body.getReader();
        let controller: ByteController, ended = false, total = 0;
        const unlock = () => { try { reader.releaseLock(); } catch { /* pending read releases after cancellation */ } };
        const end = (error?: ReadingError, consumerCancel = false) => {
          if (ended) return;
          ended = true; op.signal.removeEventListener('abort', abort); release();
          if (error && ['source_changed', 'response_size', 'invalid_response'].includes(error.code)) this.retire(error);
          if (!consumerCancel) { if (error) controller.error(error); else controller.close(); }
          if (error || consumerCancel) void reader.cancel().catch(() => {});
          unlock();
        };
        const abort = () => end(cancellation(op.signal));
        const stream = scopedByteStream({
          start(c) { controller = c; op.signal.addEventListener('abort', abort, {once: true}); if (op.signal.aborted) abort(); },
          pull: async () => {
            if (ended) return;
            try {
              this.#client.check(op.signal);
              const next = await reader.read();
              this.#client.check(op.signal);
              if (ended) return;
              if (next.done) { if (total !== this.manifest.size) fail('response_size'); end(); return; }
              if (!(next.value instanceof Uint8Array) || next.value.byteLength > 1 << 20 || total + next.value.byteLength > this.manifest.size) fail('response_size');
              total += next.value.byteLength;
              controller.enqueue(next.value.slice());
            } catch (error) { end(op.signal.aborted ? cancellation(op.signal) : error instanceof ReadingError ? error : new ReadingError('network_error')); }
            finally { if (ended) unlock(); }
          },
          cancel() { end(undefined, true); },
        });
        handedOff = true;
        return stream;
      });
    } finally { if (!handedOff) release(); }
  };
  readAsset = async (path: string, signal?: AbortSignal): Promise<Uint8Array> => {
    if (this.manifest.format !== 'epub') fail('invalid_format');
    const entry = this.manifest.resources?.find(resource => resource.path === path) ?? fail('invalid_asset');
    if (!member(path)) fail('invalid_asset');
    return this.#binary(query(routePath('get_reading_sessions_sessionId_assets', {sessionId: this.manifest.sessionId}), {path}), {method: 'GET'}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      this.#etag(r);
      if (mime(r.headers.get('Content-Type')) !== entry.mime.toLowerCase()) fail('invalid_response');
      return bytes(r, entry.size, active, check, entry.size);
    });
  };
  /** Authenticated from-start original bytes; no public URL or offline receipt.
   * The session and caller must remain current until all bytes are received. */
  saveFile = async (operationId: string, signal?: AbortSignal): Promise<Uint8Array> => {
    this.#check();
    if (this.manifest.canSaveFile !== true) fail('not_permitted');
    if (!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(operationId)) fail('invalid_request');
    const originalSize=this.manifest.conversion?.originalSize??this.manifest.size, originalRevision=this.manifest.conversion?.originalRevision??this.manifest.revision;
    if (originalSize > 64 << 20) fail('response_size');
    if (this.#savingFile) fail('request_in_progress');
    this.#savingFile = true;
    try {
      // The canonical session route supplies the captured identity; the new
      // source-declared subresource descriptor is checkpoint-generated.
      const path = routePath('delete_reading_sessions_sessionId', {sessionId: this.manifest.sessionId}) + '/file-save';
      return await this.#binary(path, {method: 'POST', headers: {'Content-Type': 'application/json','If-Match':'"'+originalRevision+'"'}, body: JSON.stringify({operationId})}, signal, async (r, active, check) => {
        if (r.status !== 200) return this.#client.error(r, active, check);
        if(r.headers.get('ETag')!=='"'+originalRevision+'"')fail('source_changed');
        if (r.headers.get('X-Reading-Item') !== this.manifest.itemId || r.headers.get('X-Reading-Source') !== this.manifest.sourceId
          || r.headers.get('Content-Length') !== String(originalSize) || r.headers.has('Content-Range')
          || r.headers.get('Accept-Ranges') !== 'none' || mime(r.headers.get('Content-Type')) !== 'application/octet-stream'
          || r.headers.get('Content-Disposition') !== 'attachment; filename="publication"') fail('source_changed');
        return bytes(r, 64 << 20, active, check, originalSize);
      });
    } finally { this.#savingFile = false; }
  };
  page = async (index: number, options: Readonly<{width: number; signal?: AbortSignal}>): Promise<ReadingPage> => {
    if (!comicFormat(this.manifest.format)) fail('invalid_format');
    if (!integer(index, 0, this.manifest.pageNames!.length - 1) || !integer(options.width, 32, 2048)) fail('invalid_page');
    return this.#binary(query(routePath('get_reading_sessions_sessionId_pages_pageIndex', {sessionId: this.manifest.sessionId, pageIndex: index}), {width: options.width}), {method: 'GET'}, options.signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      this.#etag(r); const observed = mime(r.headers.get('Content-Type'));
      if (observed !== 'image/jpeg' && observed !== 'image/png' && observed !== 'image/webp') return fail('invalid_response');
      const body = await bytes(r, 16 << 20, active, check);
      return Object.freeze({index, name: this.manifest.pageNames![index], bytes: body, mime: observed});
    });
  };
  /** Bounded literal search over this retained session's persisted index.
   * Returns an immutable page; nonindexed coverage is fallback evidence, never
   * a "no matches" claim. Never returns or fabricates a CFI. */
  search = async (queryText: string, options: Readonly<{limit?: number; cursor?: string; signal?: AbortSignal}> = {}): Promise<ReadingSearchPage> => {
    this.#check();
    const q = typeof queryText === 'string' ? queryText.trim() : '';
    if (options.cursor !== undefined && typeof options.cursor !== 'string') fail('invalid_request');
    const limit = options.limit ?? 20, cursor = options.cursor ?? '';
    // Reject a lone surrogate before URL encoding can replace it with U+FFFD and
    // search different text.
    if (!wellFormed(q) || !q || q.length > 1024 || !integer(limit, 1, 50) || cursor !== '' && !readingSearchCursor.test(cursor)) fail('invalid_request');
    const witness: ReadingSearchWitness = {
      sessionId: this.manifest.sessionId, sourceId: this.manifest.sourceId,
      expectedRevision: this.manifest.conversion?.originalRevision ?? this.manifest.revision,
      format: this.manifest.format, converted: this.manifest.conversion !== undefined,
      spine: this.manifest.spine ?? [],
      xhtml: path => this.manifest.resources?.some(resource => resource.path === path && resource.mime.toLowerCase() === 'application/xhtml+xml') === true,
    };
    return this.#control(async () => {
      const out = await this.#client.json(
        query(routePath('get_reading_sessions_sessionId_search', {sessionId: this.manifest.sessionId}), {q, ...(cursor ? {cursor} : {}), limit}),
        'GET', undefined, options.signal, decodeGetReadingSessionsSessionIdSearchResponse, 2 << 20,
        undefined, this.signal, value => value.sourceRevision);
      this.#check();
      return parseDecodedReadingSearchPage(out, witness, limit);
    });
  };
  progress = async (signal?: AbortSignal): Promise<ReadingProgress> => {
    return this.#control(async () => {
      const out = await this.#client.json(routePath('get_reading_sessions_sessionId_progress', {sessionId: this.manifest.sessionId}), 'GET', undefined, signal, decodeGetReadingSessionsSessionIdProgressResponse, 16 << 10, this.#headers(), this.signal);
      validateProgress(out, this.manifest); return out;
    });
  };
  saveProgress = async (request: PutReadingSessionsSessionIdProgressRequest, signal?: AbortSignal): Promise<ReadingSavedProgress> => {
    this.#check(); const expectedRevision = request.expectedRevision, location = {...request.location};
    if (!integer(expectedRevision, 0, Number.MAX_SAFE_INTEGER)) fail('invalid_revision'); validateLocation(location, this.manifest);
    return this.#control(async () => {
      const out = await this.#client.json(routePath('put_reading_sessions_sessionId_progress', {sessionId: this.manifest.sessionId}), 'PUT', {expectedRevision, location}, signal, decodePutReadingSessionsSessionIdProgressResponse, 16 << 10, this.#headers(), this.signal);
      validateProgress(out, this.manifest);
      if (out.saved && (out.progress === null || out.progress.stale || out.progress.revision !== expectedRevision + 1)) fail('invalid_progress');
      return out;
    }, true);
  };
  /** The existing generated session identity plus the declared annotations subresource.
   * Official new operation descriptors are produced at the Release checkpoint. */
  annotations = async (cursor = '', revision?: number, signal?: AbortSignal): Promise<ReadingAnnotationsPage> => {
    this.#check();
    if (this.manifest.canAnnotate !== true) fail('not_permitted');
    if (cursor && (!readingAnnotationId(cursor) || revision === undefined) || revision !== undefined && !integer(revision, 0, Number.MAX_SAFE_INTEGER)) fail('invalid_revision');
    const path = routePath('delete_reading_sessions_sessionId', {sessionId: this.manifest.sessionId}) + '/annotations';
    return this.#control(() => this.#client.json(query(path, {cursor, revision}), 'GET', undefined, signal,
      raw => parseReadingAnnotations(raw, this.manifest, location => validateLocation(location, this.manifest), cursor), 2 << 20, this.#headers(), this.signal), true);
  };
  mutateAnnotation = async (request: ReadingAnnotationMutation, signal?: AbortSignal): Promise<Readonly<{revision: number}>> => {
    this.#check(); if (this.manifest.canAnnotate !== true) fail('not_permitted');
    const captured = captureReadingAnnotationMutation(request, location => validateLocation(location, this.manifest));
    const path = routePath('delete_reading_sessions_sessionId', {sessionId: this.manifest.sessionId}) + '/annotations';
    return this.#control(() => this.#client.json(path, 'PUT', captured, signal,
      raw => parseReadingAnnotationSaved(raw, captured.expectedRevision), 4096, this.#headers(), this.signal), true);
  };
  close = async (signal?: AbortSignal): Promise<void> => {
    if (this.signal.aborted) return;
    this.retire(); this.#client.check(signal);
    return this.#client.response(routePath('delete_reading_sessions_sessionId', {sessionId: this.manifest.sessionId}), {method: 'DELETE'}, signal, async (r, active, check) => {
      if (r.status !== 204) return this.#client.error(r, active, check);
      check(); await discard(r);
    });
  };
}
