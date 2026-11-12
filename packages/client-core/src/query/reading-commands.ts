import {routePath} from '../../../contracts/src/routes.ts';
import {readingMarkAllPath, readingMarkAllView, type ReadingMarkAllProjection, type ReadingMarkAllCommand} from '../reading-mark-all.ts';
import {serverPath} from './server-api.ts';

interface ReadingCommandApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const reviewCodes = new Set(['personal_state_conflict', 'personal_needs_resolution', 'operation_expired', 'idempotency_key_reused',
  'stale_continuation', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized', 'idempotency_conflict', 'revision_mismatch']);
export const readingMarkAllNeedsReview = (error: unknown): boolean => object(error) && typeof error.code === 'string' && reviewCodes.has(error.code);

/** How many of the library's titles this profile has not read: what "Mark all read" would mark. */
export function readReadingMarkAll(api: ReadingCommandApi, serverId: string, libraryId: string,
  receive?: (proof: ReadingMarkAllProjection) => void) {
  const path = readingMarkAllPath('GET', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  return api.request(path, 'GET').then(raw => {
    if (!object(raw) || typeof raw.total !== 'number' || !Number.isSafeInteger(raw.total) || raw.total < 0) throw new Error('invalid_reading_mark_all');
    const proof: ReadingMarkAllProjection = Object.freeze({serverId, libraryId, viewerFence: '', revision: Object.freeze({catalog: 0, viewer: 0}), count: raw.total});
    receive?.(proof);
    return proof;
  });
}
type MarkJob = Readonly<{id: string; state: string; error?: string}>;
const job = (value: unknown): MarkJob => {
  if (!object(value) || typeof value.id !== 'string' || !value.id || typeof value.state !== 'string') throw new Error('invalid_reading_mark_all');
  return value as MarkJob;
};
const pause = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));
/** Marks every title of the library read, as this profile, with the server's set_state job over
 * the library's view, and resolves once the job has finished. Only the profile's own marks change. */
export function commitReadingMarkAll(api: ReadingCommandApi, _serverId: string, libraryId: string,
  _proof: ReadingMarkAllProjection, _operation: ReadingMarkAllCommand,
  lifetime: Readonly<{isCurrent: () => boolean; onCommitted?: () => void}>, wait: (ms: number) => Promise<void> = pause) {
  const path = readingMarkAllPath('POST', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  return api.request(path, 'POST', {kind: 'set_state', target: {filter: readingMarkAllView(libraryId)}, params: {watched: true}}).then(async raw => {
    if (!lifetime.isCurrent()) return false;
    let current = job(raw);
    while (current.state !== 'done') {
      if (current.state === 'failed' || current.state === 'cancelled') throw Object.assign(new Error(current.error || 'mark_all_failed'), {code: 'request_failed'});
      await wait(1000);
      if (!lifetime.isCurrent()) return false;
      current = job(await api.request(serverPath('GET /v1/jobs/{id}', {id: current.id}), 'GET'));
    }
    if (!lifetime.isCurrent()) return false;
    lifetime.onCommitted?.();
    return true;
  });
}

/** Browser lifecycle delivery retains the existing bounded farewell transport. */
export function farewellReadingSession(api: Readonly<{farewell?: (path: string, method: string) => unknown}>, sessionId: string): void {
  api.farewell?.(routePath('delete_reading_sessions_sessionId', {sessionId}), 'DELETE');
}
