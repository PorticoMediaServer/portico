import {routes} from '../../contracts/src/index.ts';

export type ReadingFavoriteFlag = Readonly<{itemId: string; favorite: boolean; revision: number; viewerFence: string; operationId?: string}>;
export type ReadingFavoriteCommand = Readonly<{operationId: string; expectedRevision: number; favorite: boolean}>;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);

/** The official descriptor is the only path source. Until Release publishes
 * both operations, the caller withholds the action rather than inventing a URL. */
export function readingFavoritePath(method: 'GET' | 'PUT', itemId: string): string | undefined {
  if (!identifier(itemId)) return undefined;
  const operation: string = method === 'GET' ? 'get_items_itemId_reading_favorite' : 'put_items_itemId_reading_favorite';
  const descriptor = routes.find(route => route.id === operation);
  if (!descriptor || descriptor.method !== method || descriptor.path[0] !== '/' || descriptor.path.includes('?') || descriptor.path.includes('#')) return undefined;
  const parameters = descriptor.path.match(/\{[^}]+\}/g);
  if (parameters?.length !== 1 || parameters[0] !== '{itemId}') return undefined;
  return descriptor.path.replace('{itemId}', encodeURIComponent(itemId));
}

export function parseReadingFavorite(value: unknown, serverId: string, itemId: string, operationId?: string): ReadingFavoriteFlag {
  if (!record(value) || value.serverId !== serverId || value.itemId !== itemId || typeof value.favorite !== 'boolean'
    || typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision < 0
    || typeof value.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(value.viewerFence)
    || value.operationId !== operationId) throw new Error('invalid_reading_favorite');
  return Object.freeze({itemId, favorite: value.favorite, revision: value.revision, viewerFence: value.viewerFence, ...(operationId === undefined ? {} : {operationId})});
}

export function validReadingFavoriteCommand(value: ReadingFavoriteCommand): boolean {
  return identifier(value.operationId) && Number.isSafeInteger(value.expectedRevision) && value.expectedRevision >= 0 && typeof value.favorite === 'boolean';
}
