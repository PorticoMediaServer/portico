import {routePath} from '../../../contracts/src/routes.ts';
import {isUserMutationKey, parseUserMutationReceipt, UserMutationError} from '../user-mutations.ts';
import {readingMarkAllPath, parseReadingMarkAllProjection, parseReadingMarkAllReceipt,
  type ReadingMarkAllProjection, type ReadingMarkAllCommand} from '../reading-mark-all.ts';

interface ReadingCommandApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const reviewCodes = new Set(['personal_state_conflict', 'personal_needs_resolution', 'operation_expired', 'idempotency_key_reused',
  'stale_continuation', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized', 'receipt_expired', 'idempotency_conflict', 'mutation_needs_review', 'revision_mismatch']);
export const readingMarkAllNeedsReview = (error: unknown): boolean => object(error) && typeof error.code === 'string' && reviewCodes.has(error.code);

export async function readReadingMarkAll(api: ReadingCommandApi, serverId: string, libraryId: string) {
  const path = readingMarkAllPath('GET', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  return parseReadingMarkAllProjection(await api.request(path, 'GET'), serverId, libraryId);
}
/** Reuse the original proof/body through uncertain outcomes. A receipt re-reads
 * current counts under the same authority; it never reconstructs old counts. */
export async function commitReadingMarkAll(api: ReadingCommandApi, serverId: string, libraryId: string,
  proof: ReadingMarkAllProjection, operation: ReadingMarkAllCommand,
  lifetime: Readonly<{isCurrent: () => boolean; acknowledge?: (operationId: string) => Promise<void>; onCommitted?: () => void}>) {
  const path = readingMarkAllPath('POST', libraryId);
  if (!path) throw new Error('invalid_reading_mark_all');
  const raw = await api.request(path, 'POST', operation);
  if (!lifetime.isCurrent()) return false;
  if (object(raw) && raw.mutationReceipt !== undefined) {
    const marker = raw.mutationReceipt;
    if (!object(marker) || typeof marker.key !== 'string' || !isUserMutationKey(marker.key)) throw new Error('invalid_reading_mark_all');
    const receipt = parseUserMutationReceipt(marker, {key: marker.key, operation: 'post_libraries_libraryId_reading_mark_all'});
    if (receipt.targetId !== libraryId) throw new Error('invalid_reading_mark_all');
    if (receipt.status !== 'applied') throw new UserMutationError('mutation_needs_review', receipt.key, receipt);
    const readPath = readingMarkAllPath('GET', libraryId);
    if (!readPath) throw new Error('invalid_reading_mark_all');
    const rawCurrent = await api.request(readPath, 'GET');
    if (!lifetime.isCurrent()) return false;
    const fresh = parseReadingMarkAllProjection(rawCurrent, serverId, libraryId);
    if (fresh.viewerFence !== proof.viewerFence || fresh.revision.catalog < proof.revision.catalog || receipt.revision === undefined || fresh.revision.viewer < receipt.revision) throw new Error('invalid_reading_mark_all');
  } else parseReadingMarkAllReceipt(raw, proof, operation);
  if (!lifetime.isCurrent()) return false;
  await lifetime.acknowledge?.(operation.operationId);
  if (!lifetime.isCurrent()) return false;
  lifetime.onCommitted?.();
  return true;
}

/** Browser lifecycle delivery retains the existing bounded farewell transport. */
export function farewellReadingSession(api: Readonly<{farewell?: (path: string, method: string) => unknown}>, sessionId: string): void {
  api.farewell?.(routePath('delete_reading_sessions_sessionId', {sessionId}), 'DELETE');
}
