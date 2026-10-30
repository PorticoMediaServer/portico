import {routes} from '../../contracts/src/index.ts';

export type ReadingReadMark = Readonly<{itemId: string; read: boolean; revision: number; viewerFence: string; operationId?: string}>;
export type ReadingReadMarkCommand = Readonly<{operationId: string; expectedRevision: number; read: boolean}>;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);

/** The official descriptor is the only path source. Until Release publishes
 * both operations, the caller withholds the action rather than inventing a URL. */
export function readingMarkPath(method: 'GET' | 'PUT', itemId: string): string | undefined {
  if (!identifier(itemId)) return undefined;
  const operation: string = method === 'GET' ? 'get_items_itemId_reading_mark' : 'put_items_itemId_reading_mark';
  const descriptor = routes.find(route => route.id === operation);
  if (!descriptor || descriptor.method !== method || descriptor.path[0] !== '/' || descriptor.path.includes('?') || descriptor.path.includes('#')) return undefined;
  const parameters = descriptor.path.match(/\{[^}]+\}/g);
  if (parameters?.length !== 1 || parameters[0] !== '{itemId}') return undefined;
  return descriptor.path.replace('{itemId}', encodeURIComponent(itemId));
}

export function parseReadingMark(value: unknown, serverId: string, itemId: string, operationId?: string): ReadingReadMark {
  if (!record(value) || value.serverId !== serverId || value.itemId !== itemId || typeof value.read !== 'boolean'
    || typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision < 0
    || typeof value.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(value.viewerFence)
    || value.operationId !== operationId) throw new Error('invalid_reading_mark');
  return Object.freeze({itemId, read: value.read, revision: value.revision, viewerFence: value.viewerFence, ...(operationId === undefined ? {} : {operationId})});
}

export function validReadingMarkCommand(value: ReadingReadMarkCommand): boolean {
  return identifier(value.operationId) && Number.isSafeInteger(value.expectedRevision) && value.expectedRevision >= 0 && typeof value.read === 'boolean';
}
