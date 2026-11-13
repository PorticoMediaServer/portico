import {rebuiltNewIssues, rebuiltPeriodicalGroups, rebuiltPeriodicalIssues} from './reading-periodicals-rebuilt.ts';
import {validReadingCover} from './reading-cover.ts';
import {serverPath} from './query/server-api.ts';
import {rebuiltReadingContinue, rebuiltReadingPage, rebuiltReadingSeries} from './reading-lists-rebuilt.ts';
import {retryAfterFrom} from './timer.ts';
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
/** Where a Continue card's book is (SRV-A2): the 1-based page of a page book. An EPUB's saved
 * `section` is a package path (OEBPS/chapter3.xhtml), not a place a reader would name, so it gives
 * none until the row carries a chapter title; a stale location gives none either. */
export function readingResumePage(progress: Readonly<{stale?: boolean; location: Readonly<{format: string; pageIndex?: number}>}>): number | undefined {
  const index = progress.location.pageIndex;
  return !progress.stale && progress.location.format !== 'epub' && Number.isSafeInteger(index) && index! >= 0 ? index! + 1 : undefined;
}
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
  /** A busy or rate-limited answer's Retry-After, in whole seconds (presentError reads it). */
  readonly retryAfterSeconds?: number;
  constructor(code: string, status?: number, retryAfterSeconds?: number) {
    super(code); this.name = 'ReadingError'; this.code = code; this.status = status;
    if (retryAfterSeconds !== undefined) this.retryAfterSeconds = retryAfterSeconds;
  }
}
/** The answer's Retry-After (by the shared parser) in whole seconds. */
function retryAfter(response: Response): number | undefined {
  const ms = retryAfterFrom(response.headers);
  return ms === undefined ? undefined : Math.min(Math.ceil(ms / 1000), 86400);
}
/** RD-8 (DECISIONS W-19): only the server-wide cap answers busy; wait as asked, briefly. */
const BUSY_WAIT_DEFAULT_S = 2, BUSY_WAIT_MAX_S = 10;
const waitFor = (ms: number, signal?: AbortSignal) => new Promise<void>((resolve, reject) => {
  if (signal?.aborted) {reject(cancellation(signal)); return;}
  const stop = () => {clearTimeout(timer); reject(cancellation(signal!));};
  const timer = setTimeout(() => {signal?.removeEventListener('abort', stop); resolve();}, ms);
  signal?.addEventListener('abort', stop, {once: true});
});
type Sender = (path: string, init: RequestInit) => Promise<Response>;
type Capture = Readonly<{acknowledge?: (key: string) => Promise<void>; send: Sender; isCurrent: () => boolean; subscribe: (retire: () => void) => () => void;
  /** The full address of a path on this server, for a link the browser follows itself. */
  address?: (path: string) => string}>;
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
/** Each title's read mark, as Discover's cards show it. */
const marks = (items: readonly ReadingItem[]): ReadingPreviewMarks => new Map(items.flatMap(item => item.readingMark ? [[item.id, item.readingMark] as const] : []));
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
        // The server counts words in EPUBs and PDFs alike (reading/discover_short_reads.go); both are short reads.
        if (entry.item.format !== 'epub' && entry.item.format !== 'pdf' || !integer(entry.wordCount, 1, 44999) || !integer(entry.estimatedSeconds, 1, 10800) || entry.estimatedSeconds !== Math.ceil(entry.wordCount * 60 / 250)) fail('invalid_document');
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

const comicFormat = (s: string) => s === 'cbz' || s === 'cbr' || s === 'cb7';
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

/** The rebuilt server's answer to GET /v1/items/{id}/book: how to read one version of a book. */
type BookAnswer = Readonly<{itemId: string; version: string; versions: readonly Readonly<{id: string; format: string; name?: string; fingerprint?: string}>[];
  format: string; fingerprint: string; size: number; unreadable?: Readonly<{code: string; message: string}>; mediaUrl?: string; saveUrl?: string;
  direction?: string; openingLines?: string; parts?: readonly Readonly<{path: string; type: string; size: number}>[]; spine?: readonly string[];
  contents?: readonly Readonly<{title: string; part: string; fragment?: string; depth: number}>[]; pages?: readonly string[]}>;
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const itemPath = (itemId: string) => serverPath('GET /v1/items/{id}', {id: itemId});
/** A media address the server gave: its own, under /v1/media/, never another origin. */
const mediaAddress = (v: unknown): v is string => typeof v === 'string' && /^\/v1\/media\/[A-Za-z0-9._-]+(\/source\?save=1)?$/.test(v);
function parseBook(raw: unknown, itemId: string): BookAnswer {
  if (!isRecord(raw) || raw.itemId !== itemId || typeof raw.version !== 'string' || !Array.isArray(raw.versions) || raw.versions.length > 128
    || typeof raw.format !== 'string' || typeof raw.fingerprint !== 'string' || !/^[0-9a-f]{16}$/.test(raw.fingerprint)
    || typeof raw.size !== 'number' || !integer(raw.size, 0, Number.MAX_SAFE_INTEGER)
    || raw.mediaUrl !== undefined && !mediaAddress(raw.mediaUrl) || raw.saveUrl !== undefined && !mediaAddress(raw.saveUrl)) fail('invalid_document');
  return raw as BookAnswer;
}
/** The reader's format for a book's: a comic made of pictures reads as a CBZ does, page by page. */
const readerFormat = (format: string) => format === 'images' ? 'cbz' : format;
/** A place as the rebuilt server keeps it in the state's position: the reader's location, the file
 * it was made in, and whether the reader reached the end (which marks the book read). */
type Place = ReadingLocation & Readonly<{fingerprint?: string; atEnd?: boolean}>;
function readPlace(raw: unknown): Place | undefined {
  if (typeof raw !== 'string' || !raw) return undefined;
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value) || typeof value.progression !== 'number' || !Number.isFinite(value.progression) || value.progression < 0 || value.progression > 1) return undefined;
    return value as Place;
  } catch {return undefined;}
}
/** The new server keeps no revisions: the later edit wins. A fence of the old shape stands in. */
const noFence = '0'.repeat(64);

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
async function text(response: Response, max: number, check: () => void): Promise<Uint8Array> {
  const declared = response.headers.get('Content-Length');
  if (declared !== null && (!/^\d+$/.test(declared) || !integer(Number(declared), 0, max))) { await discard(response); return fail('response_size'); }
  const out = new TextEncoder().encode(await response.text());
  check();
  return out.length > max ? fail('response_size') : out;
}
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
  /** The full address of a path on the captured server. */
  address(path: string): string { return this.#capture.address ? this.#capture.address(path) : path; }
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
  async json<T>(path: string, method: string, body: unknown, signal: AbortSignal | undefined, decode: (value: unknown, recovered: boolean) => T, max: number, headers?: Record<string, string>, sessionSignal?: AbortSignal, sourceRevision?: (value: T) => string, maxBody = 8192): Promise<T> {
    const payload = body === undefined ? undefined : JSON.stringify(body);
    if (payload !== undefined && new TextEncoder().encode(payload).length > maxBody) fail('request_size');
    return this.response(path, {method, headers: {...headers, Accept: 'application/json', ...(payload === undefined ? {} : {'Content-Type': 'application/json'})}, ...(payload === undefined ? {} : {body: payload})}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.error(r, active, check);
      if (mime(r.headers.get('Content-Type')) !== 'application/json') return fail('invalid_response');
      // A phone's fetch (Hermes) gives a response no body stream: its JSON is read as text, within the same limit.
      const raw = r.body ? await bytes(r, max, active, check) : await text(r, max, check);
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
    await discard(response);
    throw new ReadingError(code, response.status, response.status === 503 || response.status === 429 ? retryAfter(response) : undefined);
  }
  // A book's favourite, My List and read mark are the profile's own state on the title, set
  // through the state route; the later edit wins, so there is no revision to keep. The old
  // shapes' revision and fence are kept for the screens and carry nothing.
  #signedIn(): boolean {
    const viewer = this.viewer;
    return !!viewer.authority && !!viewer.accountId && !!viewer.profileId && ['owner', 'admin', 'member'].includes(viewer.role);
  }
  canFavorite(itemId: string): boolean { return this.#signedIn() && id(itemId); }
  async getFavorite(itemId: string, signal?: AbortSignal) {
    const state = await this.state(itemId, signal);
    return Object.freeze({itemId, favorite: state.favorite === true, revision: 0, viewerFence: noFence});
  }
  async setFavorite(itemId: string, command: ReadingFavoriteCommand, signal?: AbortSignal) {
    if (!validReadingFavoriteCommand(command)) fail('invalid_request');
    const state = await this.setState(itemId, {favorite: command.favorite}, signal);
    return Object.freeze({itemId, favorite: state.favorite === true, revision: command.expectedRevision + 1, viewerFence: noFence, operationId: command.operationId});
  }
  /** A series has no My List of its own on this server: the control is not offered. */
  canUseGroupMyList(_target: ReadingSavedGroupTarget): boolean { return false; }
  async getGroupMyList(_target: ReadingSavedGroupTarget, _signal?: AbortSignal): Promise<ReturnType<typeof parseReadingSavedGroupMembership>> { return fail('unsupported'); }
  async setGroupMyList(_target: ReadingSavedGroupTarget, _command: ReadingSavedGroupCommand, _signal?: AbortSignal): Promise<ReturnType<typeof parseReadingSavedGroupMembership>> { return fail('unsupported'); }
  canUseMyList(itemId: string): boolean { return this.#signedIn() && id(itemId); }
  async getMyList(itemId: string, signal?: AbortSignal) {
    const state = await this.state(itemId, signal);
    return Object.freeze({itemId, listed: state.inList === true, revision: 0, viewerFence: noFence});
  }
  async setMyList(itemId: string, command: ReadingListCommand, signal?: AbortSignal) {
    if (!validReadingSavedCommand(command)) fail('invalid_request');
    const state = await this.setState(itemId, {inList: command.listed}, signal);
    return Object.freeze({itemId, listed: state.inList === true, revision: command.expectedRevision + 1, viewerFence: noFence, operationId: command.operationId});
  }
  canMarkRead(itemId: string): boolean { return this.#signedIn() && id(itemId); }
  async readMark(itemId: string, signal?: AbortSignal) {
    const state = await this.state(itemId, signal);
    return Object.freeze({itemId, read: state.watched === true, revision: 0, viewerFence: noFence});
  }
  /** Marking by hand also clears the place in the book. */
  async setReadMark(itemId: string, command: ReadingReadMarkCommand, signal?: AbortSignal) {
    if (!validReadingMarkCommand(command)) fail('invalid_request');
    const state = await this.setState(itemId, {watched: command.read}, signal);
    return Object.freeze({itemId, read: state.watched === true, revision: command.expectedRevision + 1, viewerFence: noFence, operationId: command.operationId});
  }
  /** The rebuilt server's own routes, read as JSON through this captured sign-in. */
  #rebuilt = {request: <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => this.json<T>(path, method, body, signal, value => value as T, 4 << 20)};
  /** A library's titles, by title: every book, comic and comic issue in it. */
  async list(options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<GetReadingItemsResponse> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 50, 1, 100)) fail('invalid_request');
    return rebuiltReadingPage(this.#rebuilt, {libraryId: options.libraryId, cursor: options.cursor, limit: options.limit ?? 50}, signal);
  }
  /** A Read library's Discover: its recently added titles and its newest magazine issues. The
   * series, short reads and newspaper rows have no read on the rebuilt server yet and are not drawn
   * (a stopgap while those reads are built, not a deferral). */
  async discover(options: Readonly<{libraryId?: string; limit?: number; seriesLimit?: number; shortReadsLimit?: number; newIssuesLimit?: number; newspaperLimit?: number}> = {}, signal?: AbortSignal): Promise<ReadingDiscover & {newIssueCards: readonly ReadingPeriodicalIssue[]; newspaperCard?: ReadingPeriodicalGroup; previewMarks: ReadingPreviewMarks}> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 20, 1, 100)) fail('invalid_request');
    const [page, issues] = await Promise.all([rebuiltReadingPage(this.#rebuilt, {libraryId: options.libraryId, limit: options.limit ?? 20, sort: 'added'}, signal),
      options.libraryId && options.newIssuesLimit ? rebuiltNewIssues(this.#rebuilt, options.libraryId, {limit: options.newIssuesLimit}, signal).catch(() => undefined) : undefined]);
    return {rows: [{key: 'recent', items: page.items, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})},
      ...(issues?.issues.length ? [{key: 'new-issues' as const, items: [], ...(issues.nextCursor ? {nextCursor: issues.nextCursor} : {})}] : [])],
      newIssueCards: issues?.issues ?? [], previewMarks: marks(page.items)};
  }
  /** One Discover row alone, its next page from that row's own `nextCursor`. Only Recently added
   * has a read on the rebuilt server; any other row is empty. */
  async discoverRow(row: ReadingDiscoverRowKey, options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<Readonly<{row: ReadingDiscover['rows'][number]; newIssueCards: readonly ReadingPeriodicalIssue[]; previewMarks: ReadingPreviewMarks}>> {
    if (!discoverRows.includes(row) || options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 20, 1, 100)) fail('invalid_request');
    if (row === 'new-issues' && options.libraryId) {
      const issues = await rebuiltNewIssues(this.#rebuilt, options.libraryId, {cursor: options.cursor, limit: options.limit ?? 20}, signal);
      return Object.freeze({row: {key: row, items: [], ...(issues.nextCursor ? {nextCursor: issues.nextCursor} : {})}, newIssueCards: issues.issues, previewMarks: new Map()});
    }
    if (row !== 'recent') return Object.freeze({row: {key: row, items: []}, newIssueCards: [], previewMarks: new Map()});
    const page = await rebuiltReadingPage(this.#rebuilt, {libraryId: options.libraryId, cursor: options.cursor, limit: options.limit ?? 20, sort: 'added'}, signal);
    return Object.freeze({row: {key: 'recent', items: page.items, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})}, newIssueCards: [], previewMarks: marks(page.items)});
  }
  async periodicalGroups(libraryId: string, publicationType: ReadingPeriodicalType, options: Readonly<{cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<ReadingPeriodicalGroupsPage> {
    if (!id(libraryId) || !periodicalType(publicationType) || !integer(options.limit ?? 24, 1, 100) || options.cursor !== undefined && options.cursor.length > 4096) fail('invalid_request');
    return rebuiltPeriodicalGroups(this.#rebuilt, libraryId, publicationType, options, signal);
  }
  async periodicalIssues(libraryId: string, seriesId: string, publicationType: ReadingPeriodicalType, options: Readonly<{cursor?: string; limit?: number; issueDate?: string; year?: string; month?: string; order?: ReadingPeriodicalOrder}> = {}, signal?: AbortSignal) {
    if (!id(libraryId) || !id(seriesId) || !periodicalType(publicationType) || !integer(options.limit ?? 50, 1, 100) || options.order !== undefined && !['newest', 'oldest'].includes(options.order)) fail('invalid_request');
    return rebuiltPeriodicalIssues(this.#rebuilt, libraryId, seriesId, publicationType, options, signal);
  }
  /** A series' volumes, in volume order. */
  async series(libraryId: string, seriesId: string, options: Readonly<{cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<ReadingSeriesPage & {overview?: ReadingSeriesOverview}> {
    if (!id(libraryId) || !id(seriesId) || !integer(options.limit ?? 50, 1, 100)) fail('invalid_request');
    return rebuiltReadingSeries(this.#rebuilt, seriesId, {cursor: options.cursor, limit: options.limit ?? 50}, signal) as Promise<ReadingSeriesPage>;
  }
  /** The titles in progress, most recent first, each with its place: a library's own Continue
   * reading, or every library's (Home's Continue your books). One row, no further page. */
  async continue(options: Readonly<{libraryId?: string; cursor?: string; limit?: number}> = {}, signal?: AbortSignal): Promise<Omit<GetReadingContinueResponse,'items'> & {items:ReadingContinueItem[]}> {
    if (options.libraryId !== undefined && !id(options.libraryId) || !integer(options.limit ?? 50, 1, 100)) fail('invalid_request');
    if (options.cursor !== undefined) return {items: []};
    return {items: (await rebuiltReadingContinue(this.#rebuilt, options.libraryId, signal)).slice(0, options.limit ?? 50)};
  }
  /** How to read one version of a book (the server's choice when version is absent). */
  async book(itemId: string, version: string | undefined, signal?: AbortSignal): Promise<BookAnswer> {
    if (!id(itemId) || version !== undefined && !id(version)) fail('invalid_request');
    return this.json(query(serverPath('GET /v1/items/{id}/book', {id: itemId}), {version}), 'GET', undefined, signal, raw => parseBook(raw, itemId), 8 << 20);
  }
  /** A book's versions. Anything that is not a book (a film, a song) is not found. */
  async versions(itemId: string, signal?: AbortSignal): Promise<GetItemsItemIdReadingVersionsResponse> {
    const book = await this.book(itemId, undefined, signal);
    return {chosenSourceId: book.version, versions: book.versions.map(v => ({sourceId: v.id, format: v.format, size: v.id === book.version ? book.size : 0, inventoryAvailable: true}))};
  }
  /** Opens a book: what the reader needs before it asks for any part of it. Nothing is opened on
   * the server; the session reads through the book's media address, and asks for a new one when
   * that address ends. */
  async open(request: PostReadingSessionsRequest, signal?: AbortSignal, _options?: Readonly<{busyWait?: AbortSignal}>): Promise<ReadingSession> {
    if (!id(request.itemId) || !id(request.sourceId)) fail('invalid_request');
    const [book, item] = await Promise.all([this.book(request.itemId, request.sourceId, signal),
      this.json(itemPath(request.itemId), 'GET', undefined, signal, raw => raw as Record<string, unknown>, 1 << 20)]);
    if (book.unreadable) throw new ReadingError(book.unreadable.code === 'format_not_supported' ? 'unsupported_format' : book.unreadable.code);
    if (!book.mediaUrl) fail('invalid_document');
    const format = readerFormat(book.format);
    if (!engineFormat(format)) fail('unsupported_format');
    const manifest = frozen({
      sessionId: book.version, itemId: request.itemId, sourceId: book.version, revision: book.fingerprint, format, size: book.size,
      metadata: {...(typeof item.wordCount === 'number' ? {wordCount: item.wordCount, estimatedSeconds: Math.ceil(item.wordCount * 60 / 250)} : {}), ...(typeof item.title === 'string' ? {title: item.title} : {}), ...(book.direction === 'rtl' ? {direction: 'rtl'} : {}),
        ...(format === 'epub' && typeof book.openingLines === 'string' && book.openingLines.length <= 4096 ? {openingLines: book.openingLines} : {})},
      ...(format === 'epub' ? {resources: (book.parts ?? []).map(p => ({path: p.path, mime: p.type, size: p.size})), spine: [...(book.spine ?? [])],
        ...(book.contents?.length ? {contents: book.contents.slice(0, 512).map(c => ({title: c.title, section: c.part, depth: c.depth, ...(c.fragment ? {fragment: c.fragment} : {})}))} : {})} : {}),
      ...(comicFormat(format) ? {pageNames: [...(book.pages ?? [])]} : {}),
      canAnnotate: true as const, ...(book.saveUrl ? {canSaveFile: true as const} : {}),
    }) as unknown as ReadingManifest;
    this.check(signal);
    const state = isRecord(item.state) ? item.state : {};
    // The book's copies still kept, when the server names every one (10-reading rule 27).
    const kept = book.versions.every(v => typeof v.fingerprint === 'string' && v.fingerprint) ? new Set(book.versions.map(v => v.fingerprint!)) : undefined;
    const session = new ReadingSession(this, manifest, {mediaUrl: book.mediaUrl, saveUrl: book.saveUrl}, typeof state.position === 'string' ? state.position : '', kept);
    this.#sessions.add(session); return session;
  }
  /** A book's address again, for an open book whose address has ended. */
  async renew(manifest: ReadingManifest, signal: AbortSignal): Promise<Readonly<{revision: string; mediaUrl?: string; saveUrl?: string}>> {
    const book = await this.book(manifest.itemId, manifest.sourceId, signal);
    this.check(signal);
    return {revision: book.fingerprint, mediaUrl: book.mediaUrl, saveUrl: book.saveUrl};
  }
  /** One of the profile's own fields on a title, through the state route. */
  async state(itemId: string, signal?: AbortSignal): Promise<Record<string, unknown>> {
    if (!id(itemId)) fail('invalid_request');
    const item = await this.json(itemPath(itemId), 'GET', undefined, signal, raw => raw as Record<string, unknown>, 1 << 20);
    return isRecord(item.state) ? item.state : {};
  }
  async setState(itemId: string, edit: Record<string, unknown>, signal?: AbortSignal): Promise<Record<string, unknown>> {
    if (!id(itemId)) fail('invalid_request');
    return this.json(serverPath('PUT /v1/items/{id}/state', {id: itemId}), 'PUT', edit, signal, raw => isRecord(raw) ? raw : fail('invalid_document'), 64 << 10);
  }
}

/** A session that could not go on (its book's address ended and the book could not be opened
 * again as the same file) ends with this code, and the reader offers "Open again". */
export const READING_SESSION_EXPIRED = 'reading_session_expired';
/** A book's address that has ended is "not found": the session asks for the book again, once. */
const addressEnded = (error: unknown): error is ReadingError => error instanceof ReadingError && (error.status === 404 || error.status === 410);
type BookAccess = Readonly<{mediaUrl: string; saveUrl?: string}>;
/** Annotations are read whole: a book holds at most 5,000 for a profile, 500 to a page. */
const maxAnnotationPages = 100;

/** One open book. Nothing is open on the server: the book's parts, pages and file are read
 * through the media address the book route gave, which carries its own permission. Byte
 * callbacks carry no credentials or origin URLs into an engine. */
export class ReadingSession {
  readonly manifest: ReadingManifest;
  readonly signal: AbortSignal;
  #client: ReadingClient;
  #life = new AbortController();
  #activeRanges = 0;
  #savingFile = false;
  #retire: () => void;
  #access: BookAccess;
  #renewal: Promise<void> | undefined;
  #annotationRevision = 0;
  /** The fingerprints of the book's copies still kept; undefined when the server does not say. */
  #kept: ReadonlySet<string> | undefined;
  constructor(client: ReadingClient, manifest: ReadingManifest, access: BookAccess, _place: string, kept?: ReadonlySet<string>) {
    this.#client = client; this.manifest = frozen(manifest); this.signal = this.#life.signal; this.#access = access; this.#kept = kept;
    this.#retire = () => this.retire(client.signal.reason); client.signal.addEventListener('abort', this.#retire, {once: true});
    Object.freeze(this);
  }
  retire(reason: unknown = new ReadingError('cancelled')) { this.#life.abort(reason); this.#client.signal.removeEventListener('abort', this.#retire); this.#client.forget(this); }
  #check = () => { this.#client.check(this.signal); };
  /** The session's name; it never changes, since nothing is held on the server. */
  get sessionId(): string { return this.manifest.sessionId; }
  onRenew(_listener: (previous: string, next: string) => void): () => void { return () => {}; }
  /** When the book's address has ended, ask for the book again once and send the one request
   * again under the new address. A different file, or a second failure, ends the session. */
  async #addressed<T>(run: (access: BookAccess) => Promise<T>): Promise<T> {
    const used = this.#access;
    try { return await run(used); }
    catch (error) {
      if (!addressEnded(error)) throw error;
      await this.#renew(used);
      return run(this.#access);
    }
  }
  async #renew(used: BookAccess): Promise<void> {
    if (this.#access !== used) return;
    this.#renewal ??= (async () => {
      try {
        const fresh = await this.#client.renew(this.manifest, this.signal);
        if (fresh.revision !== this.manifest.revision || !fresh.mediaUrl) throw new ReadingError('source_changed', 409);
        this.#access = Object.freeze({mediaUrl: fresh.mediaUrl, ...(fresh.saveUrl ? {saveUrl: fresh.saveUrl} : {})});
      } finally { this.#renewal = undefined; }
    })();
    try { await this.#renewal; }
    catch (error) {
      if (this.signal.aborted) throw this.signal.reason;
      const ended = error instanceof ReadingError && (error.status === 401 || error.status === 403 || error.code === 'cancelled' || error.code === 'source_changed')
        ? error : new ReadingError(READING_SESSION_EXPIRED, 410);
      this.retire(ended); throw ended;
    }
  }
  async #binary<T>(path: (access: BookAccess) => string, init: RequestInit, signal: AbortSignal | undefined, consume: (r: Response, active: AbortSignal, check: () => void) => Promise<T>) {
    this.#check();
    try { return await this.#addressed(access => this.#client.response(path(access), init, signal, consume, this.signal)); }
    catch (error) {
      // The file changed since the book was opened: the reader opens it again.
      if (error instanceof ReadingError && error.status === 409) { const changed = new ReadingError('source_changed', 409); this.retire(changed); throw changed; }
      if (error instanceof ReadingError && (['response_size', 'invalid_response'].includes(error.code) || [401, 403, 404].includes(error.status ?? 0))) this.retire(error);
      throw error;
    }
  }
  headPDF = async (signal?: AbortSignal): Promise<void> => {
    if (this.manifest.format !== 'pdf') fail('invalid_format');
    return this.#binary(a => a.mediaUrl + '/source', {method: 'HEAD'}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      if (r.headers.get('Content-Length') !== String(this.manifest.size) || r.headers.get('Accept-Ranges') !== 'bytes' || mime(r.headers.get('Content-Type')) !== 'application/pdf') fail('source_changed');
      check(); await discard(r);
    });
  };
  readRange = async (range: Readonly<{start: number; endExclusive: number}>, signal?: AbortSignal): Promise<Uint8Array> => {
    if (this.manifest.format !== 'pdf') fail('invalid_format');
    const {start, endExclusive} = range, length = endExclusive - start;
    if (!integer(start, 0, this.manifest.size - 1) || !integer(endExclusive, start + 1, this.manifest.size) || length > 64 << 10) fail('invalid_range');
    if (this.#activeRanges >= 2) fail('range_busy');
    this.#activeRanges++;
    try {
      return await this.#binary(a => a.mediaUrl + '/source', {method: 'GET', headers: {Range: `bytes=${start}-${endExclusive - 1}`}}, signal, async (r, active, check) => {
        if (r.status !== 206) { if (r.status === 200) return fail('invalid_response'); return this.#client.error(r, active, check); }
        if (r.headers.get('Content-Range') !== `bytes ${start}-${endExclusive - 1}/${this.manifest.size}` || r.headers.get('Content-Length') !== String(length) || mime(r.headers.get('Content-Type')) !== 'application/pdf') fail('source_changed');
        return bytes(r, length, active, check, length);
      });
    } finally { this.#activeRanges--; }
  };
  /** Every PDF is read by range on this server; nothing is streamed from the start. */
  openSequential = async (_signal?: AbortSignal): Promise<ReadingByteStream> => fail('invalid_format');
  readAsset = async (path: string, signal?: AbortSignal): Promise<Uint8Array> => {
    if (this.manifest.format !== 'epub') fail('invalid_format');
    const entry = this.manifest.resources?.find(resource => resource.path === path) ?? fail('invalid_asset');
    if (!member(path)) fail('invalid_asset');
    const part = path.split('/').map(encodeURIComponent).join('/');
    return this.#binary(a => a.mediaUrl + '/parts/' + part, {method: 'GET'}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      if (mime(r.headers.get('Content-Type')) !== entry.mime.toLowerCase()) fail('invalid_response');
      return bytes(r, entry.size, active, check, entry.size);
    });
  };
  /** Where a browser saves the book's own file, of any size, over the connection's verified route:
   * it downloads the file itself under the file's name, and nothing is held in the page. Fails
   * with network_error while the route is not ready. */
  saveAddressNow = (): string => {
    this.#check();
    if (this.manifest.canSaveFile !== true || !this.#access.saveUrl) fail('not_permitted');
    return this.#client.address(this.#access.saveUrl) || fail('network_error');
  };
  /** The same address once checked with one HEAD, asked for again if it has ended, so a book open
   * for hours still saves. */
  saveAddress = async (signal?: AbortSignal): Promise<string> => {
    this.saveAddressNow();
    await this.#binary(a => a.saveUrl ?? fail('not_permitted'), {method: 'HEAD'}, signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      if (!(r.headers.get('Content-Disposition') ?? '').startsWith('attachment')) fail('source_changed');
      check(); discard(r);
    });
    return this.saveAddressNow();
  };
  /** Save without holding the whole file in memory. The platform supplies its private file sink. */
  saveFileTo = async (operationId: string, write: (chunk: Uint8Array) => Promise<void>, signal?: AbortSignal): Promise<void> => {
    this.#check();
    if (this.manifest.canSaveFile !== true) fail('not_permitted');
    if (!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(operationId)) fail('invalid_request');
    if (this.#savingFile) fail('request_in_progress');
    this.#savingFile = true;
    try {
      await this.#binary(a => a.saveUrl ?? fail('not_permitted'), {method: 'GET'}, signal, async (r, active, check) => {
        if (r.status !== 200) return this.#client.error(r, active, check);
        if (!(r.headers.get('Content-Disposition') ?? '').startsWith('attachment') || !r.body) fail('source_changed');
        const reader = r.body.getReader(); let size = 0;
        const abort = () => {void reader.cancel().catch(() => {});};
        active.addEventListener('abort', abort, {once: true});
        try {
          for (;;) {
            check(); const next = await reader.read(); check();
            if (next.done) break;
            size += next.value.byteLength; if (size > this.manifest.size) fail('response_size');
            await write(next.value); check();
          }
          if (size !== this.manifest.size) fail('response_size');
        } finally { active.removeEventListener('abort', abort); void reader.cancel().catch(() => {}); reader.releaseLock(); }
      });
    } finally { this.#savingFile = false; }
  };
  /** Buffer a small original. Large-file platform saves must use saveFileTo. */
  saveFile = async (operationId: string, signal?: AbortSignal): Promise<Uint8Array> => {
    if (this.manifest.size > 64 << 20) fail('response_size');
    const chunks: Uint8Array[] = [];
    await this.saveFileTo(operationId, async chunk => {chunks.push(chunk.slice());}, signal);
    const result = new Uint8Array(this.manifest.size); let at = 0;
    for (const chunk of chunks) {result.set(chunk, at); at += chunk.byteLength;}
    return result;
  };
  page = async (index: number, options: Readonly<{width: number; signal?: AbortSignal}>): Promise<ReadingPage> => {
    if (!comicFormat(this.manifest.format)) fail('invalid_format');
    if (!integer(index, 0, this.manifest.pageNames!.length - 1) || !integer(options.width, 32, 2048)) fail('invalid_page');
    return this.#binary(a => query(a.mediaUrl + '/pages/' + index, {w: options.width}), {method: 'GET'}, options.signal, async (r, active, check) => {
      if (r.status !== 200) return this.#client.error(r, active, check);
      const observed = mime(r.headers.get('Content-Type'));
      if (observed !== 'image/jpeg' && observed !== 'image/png' && observed !== 'image/webp') return fail('invalid_response');
      const body = await bytes(r, 16 << 20, active, check);
      return Object.freeze({index, name: this.manifest.pageNames![index], bytes: body, mime: observed});
    });
  };
  /** The profile's place in this book. A place made in this exact file resumes exactly. One made in
   * another version or an earlier copy is "stale": a comic opens at the same share of its pages;
   * other books open at the start. */
  progress = async (signal?: AbortSignal): Promise<ReadingProgress> => {
    this.#check();
    const state = await this.#client.state(this.manifest.itemId, signal);
    this.#check();
    const place = readPlace(state.position);
    if (!place) return {progress: null};
    const observedAt = Date.parse(typeof state.updatedAt === 'string' ? state.updatedAt : '') || Date.now();
    const pages = this.manifest.pageNames?.length ?? 0, format = this.manifest.format;
    const exact = place.fingerprint === this.manifest.revision && place.format === format;
    let location: ReadingLocation | undefined;
    if (exact) {
      location = {format, progression: place.progression, ...(place.cfi !== undefined ? {cfi: place.cfi} : {}), ...(place.section !== undefined ? {section: place.section} : {}),
        ...(place.pageIndex !== undefined ? {pageIndex: place.pageIndex} : {})};
      try { validateLocation(location, this.manifest); } catch { location = undefined; }
    } else if (comicFormat(format) && pages > 0) {
      location = {format, progression: place.progression, pageIndex: Math.min(pages - 1, Math.floor(place.progression * pages))};
    }
    if (!location) {
      return {progress: {location: {format, progression: place.progression, ...(format === 'epub' ? {cfi: 'epubcfi(/6/2)'} : {pageIndex: 0})}, observedAt,
        revision: 1, sourceId: this.manifest.sourceId, sourceRevision: place.fingerprint ?? '', stale: true}};
    }
    return {progress: {location, observedAt, revision: 1, sourceId: this.manifest.sourceId, sourceRevision: this.manifest.revision, stale: !exact}};
  };
  /** Saves the place; the later place wins on the server. Reaching the last page, or the very
   * end of the text, marks the book read there too. saved:false means a later place, saved on
   * another device, stands. */
  saveProgress = async (request: PutReadingSessionsSessionIdProgressRequest, signal?: AbortSignal): Promise<ReadingSavedProgress> => {
    this.#check(); const expectedRevision = request.expectedRevision, location = {...request.location};
    if (!integer(expectedRevision, 0, Number.MAX_SAFE_INTEGER)) fail('invalid_revision'); validateLocation(location, this.manifest);
    const pages = this.manifest.pageNames?.length ?? 0;
    const atEnd = comicFormat(location.format) ? location.pageIndex === pages - 1 : location.progression >= 0.999;
    const place = JSON.stringify({...location, fingerprint: this.manifest.revision, ...(atEnd ? {atEnd: true} : {})});
    const state = await this.#client.setState(this.manifest.itemId, {position: place}, signal);
    this.#check();
    if (state.position !== place) return {saved: false, progress: null};
    return {saved: true, progress: {location, observedAt: Date.now(), revision: expectedRevision + 1, sourceId: this.manifest.sourceId, sourceRevision: this.manifest.revision, stale: false}};
  };
  /** The profile's bookmarks, highlights and notes made in this file. Those made in an earlier
   * copy of the book carry earlierCopy so the screen can try their words or list them separately,
   * and earlierCopyGone when that copy is no longer kept, so a highlight found here may move here. */
  annotations = async (cursor = '', _revision?: number, signal?: AbortSignal): Promise<ReadingAnnotationsPage> => {
    this.#check();
    if (cursor) return Object.freeze({sourceId: this.manifest.sourceId, sourceRevision: this.manifest.revision, revision: this.#annotationRevision, entries: [], nextCursor: ''});
    const entries: ReadingAnnotationsPage['entries'][number][] = [];
    let next = '';
    for (let page = 0; page < maxAnnotationPages; page++) {
      const raw = await this.#client.json(query(serverPath('GET /v1/items/{id}/annotations', {id: this.manifest.itemId}), {cursor: next || undefined}), 'GET', undefined, signal, value => value, 16 << 20);
      this.#check();
      if (!isRecord(raw) || !Array.isArray(raw.items)) fail('invalid_document');
      for (const row of raw.items) {
        if (!isRecord(row) || typeof row.id !== 'string' || typeof row.location !== 'string') continue;
        try {
          const location = JSON.parse(row.location) as ReadingLocation;
          const earlierCopy = row.madeOn !== this.manifest.revision;
          if (!earlierCopy) validateLocation(location, this.manifest);
          const extra = isRecord(row.extra) ? row.extra : {};
          entries.push(Object.freeze({...(earlierCopy ? {earlierCopy: true as const} : {}), ...(earlierCopy && this.#kept && !this.#kept.has(String(row.madeOn)) ? {earlierCopyGone: true as const} : {}), id: row.id, kind: row.kind === 'highlight' ? 'highlight' : 'bookmark', location,
            text: typeof row.text === 'string' ? row.text : '', note: typeof row.note === 'string' ? row.note : '',
            colour: (typeof row.color === 'string' ? row.color : '') as '' | 'yellow' | 'green' | 'blue' | 'pink',
            ...(Array.isArray(extra.rects) ? {rects: Object.freeze(extra.rects as {x: number; y: number; width: number; height: number}[])} : {}),
            createdAt: Date.parse(String(row.createdAt)) || 0, updatedAt: Date.parse(String(row.updatedAt)) || 0}));
        } catch { /* a place this reader cannot use is left out, never shown wrong */ }
      }
      next = typeof raw.nextCursor === 'string' ? raw.nextCursor : '';
      if (!next) break;
    }
    entries.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    return Object.freeze({sourceId: this.manifest.sourceId, sourceRevision: this.manifest.revision, revision: this.#annotationRevision, entries: Object.freeze(entries), nextCursor: ''});
  };
  /** Adds, changes or deletes one. The app makes the id, so sending it again changes nothing. */
  mutateAnnotation = async (request: ReadingAnnotationMutation, signal?: AbortSignal): Promise<Readonly<{revision: number}>> => {
    this.#check();
    if (request.operation === 'upsert' && 'earlierCopy' in request && request.earlierCopy) fail('invalid_request');
    const captured = captureReadingAnnotationMutation(request, location => validateLocation(location, this.manifest));
    const path = serverPath('GET /v1/items/{id}/annotations', {id: this.manifest.itemId}) + '/' + encodeURIComponent(captured.id);
    if (captured.operation === 'delete') {
      await this.#client.response(path, {method: 'DELETE'}, signal, async (r, active, check) => {
        if (r.status !== 204) return this.#client.error(r, active, check);
        check(); await discard(r);
      }, this.signal);
    } else {
      const body = {kind: captured.kind, color: captured.colour, location: JSON.stringify(captured.location), madeOn: this.manifest.revision,
        progress: captured.location.progression, text: captured.text, note: captured.note, ...(captured.rects ? {extra: {rects: captured.rects}} : {})};
      await this.#client.json(path, 'PUT', body, signal, value => value, 256 << 10, undefined, this.signal, undefined, 256 << 10);
    }
    this.#check();
    this.#annotationRevision = Math.max(this.#annotationRevision, captured.expectedRevision) + 1;
    return Object.freeze({revision: this.#annotationRevision});
  };
  /** No search of the book's text runs on the server: the reader searches the open book itself. */
  readonly search: undefined | ((queryText: string, options?: Readonly<{limit?: number; cursor?: string; signal?: AbortSignal}>) => Promise<ReadingSearchPage>) = undefined;
  /** Nothing is open on the server, so closing only stops this session's requests. */
  close = async (_signal?: AbortSignal): Promise<void> => { if (!this.signal.aborted) this.retire(); };
}
