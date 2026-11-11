import type {Storage} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

export type ServerStorage = Storage;

/** The data folder's disk and what uses it (`GET /v1/admin/storage`), for the owner. */
export function readServerStorage(api: ServerRequester, signal?: AbortSignal): Promise<ServerStorage> {
  return callServer(api, 'GET /v1/admin/storage', {signal});
}

/** The header's facts: free space on the data folder's disk, the database's size, and attention while space is low. */
export function storageFacts(storage: ServerStorage): Readonly<{freeBytes?: number; databaseBytes?: number; low: boolean}> {
  const database = storage.folders?.find(part => part.name === 'database');
  return {freeBytes: storage.diskBytes > 0 ? storage.freeBytes : undefined, databaseBytes: database?.bytes, low: storage.level !== 'ok'};
}
