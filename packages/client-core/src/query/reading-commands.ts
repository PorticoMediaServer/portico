import {routePath} from '../../../contracts/src/routes.ts';
import {readingMarkAllPath, parseReadingMarkAllProjection, parseReadingMarkAllReceipt,
  type ReadingMarkAllProjection, type ReadingMarkAllCommand} from '../reading-mark-all.ts';

interface ReadingCommandApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const reviewCodes = new Set(['personal_state_conflict', 'personal_needs_resolution', 'operation_expired', 'idempotency_key_reused',
  'stale_continuation', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized', 'idempotency_conflict', 'revision_mismatch']);
export const readingMarkAllNeedsReview = (error: unknown): boolean => object(error) && typeof error.code === 'string' && reviewCodes.has(error.code);

export function readReadingMarkAll(api: ReadingCommandApi, serverId: string, libraryId: string,
  receive?: (proof: ReadingMarkAllProjection) => void) {
  const path = readingMarkAllPath('GET', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  return api.request(path, 'GET').then(raw => {
    const proof = parseReadingMarkAllProjection(raw, serverId, libraryId);
    receive?.(proof);
    return proof;
  });
}
/** Reuse the original proof/body through uncertain outcomes. */
export function commitReadingMarkAll(api: ReadingCommandApi, serverId: string, libraryId: string,
  proof: ReadingMarkAllProjection, operation: ReadingMarkAllCommand,
  lifetime: Readonly<{isCurrent: () => boolean; onCommitted?: () => void}>) {
  const path = readingMarkAllPath('POST', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  return api.request(path, 'POST', operation).then(async raw => {
    if (!lifetime.isCurrent()) return false;
    parseReadingMarkAllReceipt(raw, proof, operation);
    if (!lifetime.isCurrent()) return false;
    lifetime.onCommitted?.();
    return true;
  });
}

/** Browser lifecycle delivery retains the existing bounded farewell transport. */
export function farewellReadingSession(api: Readonly<{farewell?: (path: string, method: string) => unknown}>, sessionId: string): void {
  api.farewell?.(routePath('delete_reading_sessions_sessionId', {sessionId}), 'DELETE');
}
