import {encodedRoutePath, routePattern, routes} from '../../contracts/src/index.ts';

/** The only fact families a reread may report; the server sends a partition. */
export type ReadingRereadField = 'author' | 'series' | 'seriesNumber';
/** Client-only presentation status derived from the partition, never a wire field. */
export type ReadingRereadStatus = 'refreshed' | 'unknown';
export type ReadingRereadTarget = Readonly<{itemId: string; sourceId: string; sourceRevision: string; metadataRevision: string}>;
export type ReadingRereadLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type ReadingRereadReceipt = Readonly<{
  sourceId: string; sourceRevision: string; metadataRevision: string;
  refreshed: readonly ReadingRereadField[]; unknown: readonly ReadingRereadField[]; status: ReadingRereadStatus;
}>;
export type ReadingRereadApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

const fields: readonly ReadingRereadField[] = ['author', 'series', 'seriesNumber'];
/** The generated literal route union is read through a plain descriptor view; the
 * operation is looked up by id, never by a value that may not exist in that union. */
const publishedRoutes: readonly Readonly<{id: string; method: string; path: string}>[] = routes;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const revision = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value);
const invalid = (): never => {throw Object.assign(new Error('invalid_reading_reread'), {code: 'invalid_reading_reread'});};
const unavailable = (): never => {throw Object.assign(new Error('reading_reread_unavailable'), {code: 'reading_reread_unavailable'});};
const cancelled = (): never => {throw Object.assign(new Error('reading_reread_cancelled'), {name: 'AbortError', code: 'cancelled'});};

/** The exact published operation descriptor is the only path source. An older
 * contract withholds the action instead of manufacturing a route. */
export function readingRereadPath(itemId: string): string | undefined {
  if (!identifier(itemId)) return undefined;
  const descriptor = publishedRoutes.find(route => route.id === 'post_items_itemId_reading_reread');
  if (!descriptor || descriptor.method !== 'POST' || descriptor.path !== routePattern('post_items_itemId_reading_reread')) return undefined;
  return encodedRoutePath('post_items_itemId_reading_reread', {itemId: encodeURIComponent(itemId)});
}

/** The server's partition: each known family once, disjoint, covering all three. */
function partition(value: unknown): readonly ReadingRereadField[] {
  if (!Array.isArray(value) || value.length > fields.length) return invalid();
  const seen = new Set<string>();
  for (const field of value) {
    if (typeof field !== 'string' || !(fields as readonly string[]).includes(field) || seen.has(field)) return invalid();
    seen.add(field);
  }
  return Object.freeze(value as ReadingRereadField[]);
}

/** Parse only the exact success shape. `sourceId`/`sourceRevision` must still
 * describe the opened source; metadata may change or stay. Unknown is absent
 * for a model that knows every family. */
export function parseReadingReread(value: unknown, target: ReadingRereadTarget): ReadingRereadReceipt {
  const opened = targetOf(target);
  if (!record(value)) return invalid();
  const allowed = ['sourceId', 'sourceRevision', 'metadataRevision', 'refreshed', 'unknown'];
  if (Object.keys(value).some(key => !allowed.includes(key)) || !Object.hasOwn(value, 'sourceId') || !Object.hasOwn(value, 'sourceRevision')
    || !Object.hasOwn(value, 'metadataRevision') || !Object.hasOwn(value, 'refreshed')) return invalid();
  if (value.sourceId !== opened.sourceId || value.sourceRevision !== opened.sourceRevision || !revision(value.metadataRevision)) return invalid();
  const refreshed = partition(value.refreshed);
  const unknown = value.unknown === undefined ? Object.freeze([] as ReadingRereadField[]) : partition(value.unknown);
  const all = new Set<string>([...refreshed, ...unknown]);
  if (all.size !== fields.length || fields.some(field => !all.has(field)) || refreshed.some(field => unknown.includes(field))) return invalid();
  return Object.freeze({
    sourceId: opened.sourceId, sourceRevision: opened.sourceRevision, metadataRevision: value.metadataRevision,
    refreshed, unknown, status: unknown.length ? 'unknown' : 'refreshed',
  });
}

function targetOf(target: ReadingRereadTarget): ReadingRereadTarget {
  if (!record(target) || !identifier(target.itemId) || !identifier(target.sourceId) || !revision(target.sourceRevision) || !revision(target.metadataRevision)) return invalid();
  return Object.freeze({itemId: target.itemId, sourceId: target.sourceId, sourceRevision: target.sourceRevision, metadataRevision: target.metadataRevision});
}

/** Ask the server to reopen this exact source and refresh its file facts. The
 * receipt carries no field values: the caller re-reads the repair document for
 * the values the server actually accepted. No role or permission is derived
 * here; the server admits the owner and main profile. */
export async function rereadReadingSource(api: ReadingRereadApi, target: ReadingRereadTarget, lifetime: ReadingRereadLifetime): Promise<ReadingRereadReceipt> {
  const opened = targetOf(target), check = () => {if (lifetime.signal.aborted || !lifetime.isCurrent()) throw cancelled();};
  check();
  const path = readingRereadPath(opened.itemId);
  if (!path) throw unavailable();
  const body = Object.freeze({sourceId: opened.sourceId, expectedSourceRevision: opened.sourceRevision, expectedMetadataRevision: opened.metadataRevision});
  let raw: unknown;
  try { raw = await api.request<unknown>(path, 'POST', body, lifetime.signal); }
  catch (error) { check(); throw error; }
  check();
  return parseReadingReread(raw, opened);
}
