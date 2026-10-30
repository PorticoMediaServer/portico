import {encodedRoutePath, routePattern, routes} from '../../contracts/src/index.ts';

// Older published contracts intentionally withhold this action. Read their common
// descriptor shape so that absence remains a runtime check across generated cohorts.
const publishedOperations: readonly Readonly<{id:string;method:string;path:string}>[] = routes;

export type ReadingMarkAllPair = Readonly<{catalog: number; viewer: number}>;
export type ReadingMarkAllProjection = Readonly<{serverId: string; libraryId: string; viewerFence: string; revision: ReadingMarkAllPair; count: number}>;
export type ReadingMarkAllCommand = Readonly<{operationId: string; viewerFence: string; expectedRevision: ReadingMarkAllPair}>;
export type ReadingMarkAllReceipt = ReadingMarkAllProjection & Readonly<{operationId: string; expectedRevision: ReadingMarkAllPair; changed: number}>;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const integer = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const exact = (value: Record<string, unknown>, keys: readonly string[]) => Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key));
const bad = (): never => {throw new Error('invalid_reading_mark_all');};
function pair(value: unknown): ReadingMarkAllPair {
  if (!record(value) || !exact(value, ['catalog', 'viewer']) || !integer(value.catalog) || !integer(value.viewer)) return bad();
  return Object.freeze({catalog: value.catalog, viewer: value.viewer});
}
const samePair = (a: ReadingMarkAllPair, b: ReadingMarkAllPair) => a.catalog === b.catalog && a.viewer === b.viewer;

/** Canonical published operations alone supply paths. An older contract
 * withholds this action instead of manufacturing a route or authority. */
export function readingMarkAllPath(method: 'GET' | 'POST', libraryId: string): string | undefined {
  if (!identifier(libraryId)) return undefined;
  const operation = method === 'GET' ? 'get_libraries_libraryId_reading_mark_all' : 'post_libraries_libraryId_reading_mark_all';
  const descriptor = publishedOperations.find(route => route.id === operation);
  if (!descriptor || descriptor.method !== method || descriptor.path !== routePattern(operation)) return undefined;
  return encodedRoutePath(operation, {libraryId: encodeURIComponent(libraryId)});
}
function projection(value: Record<string, unknown>, serverId: string, libraryId: string): ReadingMarkAllProjection {
  if (!identifier(serverId) || !identifier(libraryId) || value.serverId !== serverId || value.libraryId !== libraryId
    || typeof value.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(value.viewerFence) || !integer(value.count)) return bad();
  return Object.freeze({serverId, libraryId, viewerFence: value.viewerFence, revision: pair(value.revision), count: value.count});
}
export function parseReadingMarkAllProjection(value: unknown, serverId: string, libraryId: string): ReadingMarkAllProjection {
  if (!record(value) || !exact(value, ['serverId', 'libraryId', 'viewerFence', 'revision', 'count'])) return bad();
  return projection(value, serverId, libraryId);
}
export function createReadingMarkAllCommand(proof: ReadingMarkAllProjection, operationId: string): ReadingMarkAllCommand {
  if (!identifier(operationId)) return bad();
  return Object.freeze({operationId, viewerFence: proof.viewerFence, expectedRevision: Object.freeze({...proof.revision})});
}
/** Validate the original operation and its original projection, never whatever
 * response/route/profile happens to be current when the write completes. */
export function parseReadingMarkAllReceipt(value: unknown, proof: ReadingMarkAllProjection, command: ReadingMarkAllCommand): ReadingMarkAllReceipt {
  if (!record(value) || !exact(value, ['serverId', 'libraryId', 'viewerFence', 'revision', 'count', 'operationId', 'expectedRevision', 'changed'])
    || value.operationId !== command.operationId || command.viewerFence !== proof.viewerFence || !samePair(command.expectedRevision, proof.revision)) return bad();
  const out = projection(value, proof.serverId, proof.libraryId), expectedRevision = pair(value.expectedRevision);
  if (out.viewerFence !== proof.viewerFence || !samePair(expectedRevision, command.expectedRevision) || out.count !== proof.count
    || out.revision.catalog < proof.revision.catalog || out.revision.viewer < proof.revision.viewer || !integer(value.changed) || value.changed > out.count) return bad();
  return Object.freeze({...out, operationId: command.operationId, expectedRevision, changed: value.changed});
}
