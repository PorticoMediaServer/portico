import type {Storage} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

export type ServerStorage = Storage;

/** The data folder's disk and what uses it (`GET /v1/admin/storage`), for the owner. */
export function readServerStorage(api: ServerRequester, signal?: AbortSignal): Promise<ServerStorage> {
  return callServer(api, 'GET /v1/admin/storage', {signal});
}

/** The header's facts: free space on the data folder's disk and the database's size. */
export function storageFacts(storage: ServerStorage): Readonly<{freeBytes?: number; databaseBytes?: number}> {
  const database = storage.folders?.find(part => part.name === 'database');
  return {freeBytes: storage.diskBytes > 0 ? storage.freeBytes : undefined, databaseBytes: database?.bytes};
}

/**
 * What's using space (12-console Storage, #3208): the data folder's parts, then each conversion or recording location
 * outside the data folder. A location inside the data folder is already one of its parts and is not counted again; one
 * the server could not measure has no size and says why.
 */
const KNOWN_PARTS: ReadonlySet<string> = new Set(['database', 'artwork', 'conversions', 'logs', 'backups', 'reports', 'title-data']);
export type StorageUsageLine = Readonly<{id: string; kind: 'part' | 'conversions' | 'recordings'; name: string; bytes?: number; folder?: string; external?: boolean; problem?: string}>;
const inside = (folder: string, root: string) => {
  const trim = (path: string) => path.replace(/[\\/]+$/, '');
  return trim(folder) === trim(root) || trim(folder).startsWith(`${trim(root)}/`) || trim(folder).startsWith(`${trim(root)}\\`);
};
export function storageUsageLines(storage: ServerStorage): readonly StorageUsageLine[] {
  // The server names its own parts (database, artwork, conversions, logs, backups, reports, title-data); a loose file it
  // does not name is grouped here by what it is (a database's journal, a key) or as other files, never shown by file name.
  const group = (name: string) => /\.db(-wal|-shm)?$/.test(name) ? 'database' : /\.key$/.test(name) ? 'keys' : KNOWN_PARTS.has(name) ? name : 'other';
  const sums = new Map<string, number>();
  for (const part of storage.folders ?? []) if (part.bytes > 0) sums.set(group(part.name), (sums.get(group(part.name)) ?? 0) + part.bytes);
  const parts: StorageUsageLine[] = [...sums].sort(([a], [b]) => (a === 'other' ? 1 : 0) - (b === 'other' ? 1 : 0)).map(([name, bytes]) => ({id: `part:${name}`, kind: 'part' as const, name, bytes}));
  const places = (kind: 'conversions' | 'recordings', list: Storage['conversions'] | undefined) => (list ?? [])
    .filter(place => !inside(place.folder, storage.dataFolder))
    .map(place => ({id: `${kind}:${place.folder}`, kind, name: kind, folder: place.folder, external: place.external,
      ...(place.problem ? {problem: place.problem} : {bytes: place.bytes})}));
  return [...parts, ...places('conversions', storage.conversions), ...places('recordings', storage.recordings)];
}

/** Empty cache: how much it would free now, and emptying it (no restart). */
export async function readCachePreview(api: ServerRequester, signal?: AbortSignal): Promise<number> {
  return (await callServer(api, 'GET /v1/admin/storage/cache', {signal})).bytes;
}
export async function emptyServerCache(api: ServerRequester, signal?: AbortSignal): Promise<number> {
  return (await callServer(api, 'POST /v1/admin/storage/cache/empty', {signal})).bytes;
}

/** Keep generated files: a preset that writes the one prepared-downloads time, or that time itself (#3208). */
export const KEEPING_PRESETS = ['balanced', 'short', 'long'] as const;
export const PREPARED_DOWNLOAD_DAYS = [7, 14, 30, 90, 365] as const;
export type StorageKeeping = Readonly<{preset: (typeof KEEPING_PRESETS)[number] | 'custom'; preparedDownloadsDays: number}>;
const keeping = (raw: {preset: string; preparedDownloadsDays: number}): StorageKeeping =>
  ({preset: (KEEPING_PRESETS as readonly string[]).includes(raw.preset) ? raw.preset as StorageKeeping['preset'] : 'custom', preparedDownloadsDays: raw.preparedDownloadsDays});
export async function readKeeping(api: ServerRequester, signal?: AbortSignal): Promise<StorageKeeping> {
  return keeping(await callServer(api, 'GET /v1/admin/storage/keeping', {signal}));
}
export async function saveKeeping(api: ServerRequester, edit: Readonly<{preset: (typeof KEEPING_PRESETS)[number]} | {preparedDownloadsDays: number}>, signal?: AbortSignal): Promise<StorageKeeping> {
  return keeping(await callServer(api, 'PUT /v1/admin/storage/keeping', {body: edit, signal}));
}
