import type {Storage} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import {defaultI18n, type MessageId, type MessageValues} from '../../../i18n/src/index.ts';
import {putSettingsDocument, type SettingsDocumentAPI} from '../settings-document.ts';
import {unreadableServerResponse} from '../server-messages.ts';

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
export type StorageUsageLine = Readonly<{id: string; kind: 'part' | 'conversions' | 'recordings'; name: string; bytes?: number; folder?: string; external?: boolean; problem?: string}>;
const inside = (folder: string, root: string) => {
  const trim = (path: string) => path.replace(/[\\/]+$/, '');
  return trim(folder) === trim(root) || trim(folder).startsWith(`${trim(root)}/`) || trim(folder).startsWith(`${trim(root)}\\`);
};
export function storageUsageLines(storage: ServerStorage): readonly StorageUsageLine[] {
  // The server names every part itself (#3300: database, keys, other-files and the data folder's own folders).
  const parts: StorageUsageLine[] = (storage.folders ?? []).filter(part => part.bytes > 0).map(part => ({id: `part:${part.name}`, kind: 'part' as const, name: part.name, bytes: part.bytes}));
  const places = (kind: 'conversions' | 'recordings', list: Storage['conversions'] | undefined) => (list ?? [])
    .filter(place => !inside(place.folder, storage.dataFolder))
    .map(place => ({id: `${kind}:${place.folder}`, kind, name: kind, folder: place.folder, external: place.external,
      ...(place.problem ? {problem: place.problem} : {bytes: place.bytes})}));
  return [...parts, ...places('conversions', storage.conversions), ...places('recordings', storage.recordings)];
}

/** The data folder's parts as the server names them (#3300), in the words every app shows. */
const partNames: Readonly<Record<string, readonly [MessageId, MessageId]>> = {
  database: ['server.space.database', 'server.space.databaseHelp'], artwork: ['server.space.artwork', 'server.space.artworkHelp'],
  conversions: ['server.space.conversions', 'server.space.conversionsInside'], logs: ['server.space.logs', 'server.space.logsHelp'],
  backups: ['server.space.backups', 'server.space.backupsHelp'], reports: ['server.space.reports', 'server.space.reportsHelp'],
  'title-data': ['server.space.titleData', 'server.space.titleDataHelp'], keys: ['server.space.keys', 'server.space.keysHelp'],
  metadata: ['server.space.metadata', 'server.space.metadataHelp'], cache: ['server.space.cache', 'server.space.cacheHelp'],
  temp: ['server.space.temp', 'server.space.tempHelp'], 'other-files': ['server.space.other', 'server.space.otherHelp'],
};
export function storageLineWords(line: StorageUsageLine, t: (id: MessageId, values?: MessageValues) => string = defaultI18n.t): Readonly<{name: string; help: string}> {
  if (line.kind !== 'part') return {name: t(line.kind === 'conversions' ? 'server.space.conversions' : 'server.space.recordings'),
    help: [line.folder, line.external ? t('server.space.external') : '', line.problem ? t('server.space.unmeasured', {problem: line.problem}) : ''].filter(Boolean).join(' · ')};
  const known = partNames[line.name];
  return known ? {name: t(known[0]), help: t(known[1])} : {name: line.name, help: ''};
}

/** Empty cache: how much it would free now, and emptying it (no restart). */
export async function readCachePreview(api: ServerRequester, signal?: AbortSignal): Promise<number> {
  return (await callServer(api, 'GET /v1/admin/storage/cache', {signal})).bytes;
}
export async function emptyServerCache(api: ServerRequester, signal?: AbortSignal): Promise<number> {
  return (await callServer(api, 'POST /v1/admin/storage/cache/empty', {signal})).bytes;
}

/** Keep notifications: the notices document's `keepDays` (7, 30, 90 or 180), saved at the revision read. */
export type NoticesKeep = Readonly<{etag: string; days: number}>;
export async function readNoticesKeep(api: ServerRequester, signal?: AbortSignal): Promise<NoticesKeep> {
  const document = await callServer(api, 'GET /v1/admin/settings/{name}', {id: 'notices', signal});
  const days = (document.values as {keepDays?: unknown}).keepDays;
  if (!/^"[0-9]+"$/.test(document.etag) || !Number.isSafeInteger(days)) throw new Error(unreadableServerResponse);
  return {etag: document.etag, days: days as number};
}
export async function saveNoticesKeep(api: SettingsDocumentAPI, current: NoticesKeep, days: number, signal?: AbortSignal): Promise<void> {
  await putSettingsDocument(api, 'notices', current.etag, {keepDays: days}, signal);
}
