/**
 * Remote storage on the new server, for the owner (definition 03, rules 7 to 14): WebDAV, an rclone
 * remote pasted from rclone's configuration, or Google Drive with the owner's own service account
 * key or OAuth client. Nothing is saved until the connection works: adding and changing an address
 * or a password are tested by the server first (rule 10). A source has one of four presets and its
 * own limits (rules 11, 12). A saved source no library folder is on yet is "Setup not finished"
 * (`state: 'setup'`), and Continue setup puts one of its folders in a library.
 */
import type {AdminLibrary, AdminLibraryPage, DriveSignInStarted, RemoteFolder, RemoteLimits, RemoteSource, RemoteSourceCreate, RemoteSourceEdit} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

export type {RemoteFolder, RemoteLimits, RemoteSource, RemoteSourceCreate, RemoteSourceEdit};
export type RemotePreset = 'list-only' | 'read-once' | 'everything' | 'on-request';
/** The four presets in the order they are offered, with the limits each starts with (rule 12). */
export const remotePresets: readonly Readonly<{id: RemotePreset; limits: RemoteLimits}>[] = Object.freeze([
  {id: 'list-only', limits: {connections: 2, perSecond: 2}}, {id: 'read-once', limits: {connections: 4, perSecond: 4}},
  {id: 'everything', limits: {connections: 8, perSecond: 10}}, {id: 'on-request', limits: {connections: 2, perSecond: 2}},
]);
export const defaultRemotePreset: RemotePreset = 'read-once';

const PAGES = 20;
async function allPages<T>(read: (cursor?: string) => Promise<{items: T[]; nextCursor?: string}>): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < PAGES; page++) {
    const next = await read(cursor);
    out.push(...next.items);
    if (!next.nextCursor) break;
    cursor = next.nextCursor;
  }
  return out;
}

/** Every source, with its state: connected, setup (not finished), unreachable, sign_in, waiting, limited or needs_attention. */
export const listRemoteSources = (api: ServerRequester, signal?: AbortSignal): Promise<RemoteSource[]> =>
  allPages(cursor => callServer(api, 'GET /v1/admin/remote-sources', {query: {cursor}, signal}));
export const readRemoteSource = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<RemoteSource> =>
  callServer(api, 'GET /v1/admin/remote-sources/{id}', {id, signal});
/** Tests the connection and saves the source only if it works; a refusal says what failed. */
export const addRemoteSource = (api: ServerRequester, source: RemoteSourceCreate, signal?: AbortSignal): Promise<RemoteSource> =>
  callServer(api, 'POST /v1/admin/remote-sources', {body: source, signal});
/** Rename, change the preset or limits, or replace the address, password or configuration (tested first). */
export const changeRemoteSource = (api: ServerRequester, id: string, edit: RemoteSourceEdit, signal?: AbortSignal): Promise<RemoteSource> =>
  callServer(api, 'PATCH /v1/admin/remote-sources/{id}', {id, body: edit, signal});
/** Disconnects a source no library folder is on (`409 source_in_use` otherwise). */
export const removeRemoteSource = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<void> =>
  callServer(api, 'DELETE /v1/admin/remote-sources/{id}', {id, signal}).then(() => undefined);
export const testRemoteSource = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<RemoteSource> =>
  callServer(api, 'POST /v1/admin/remote-sources/{id}/test', {id, signal});
/** The folders in one folder of a source ('' for its top). */
export const remoteSourceFolders = (api: ServerRequester, id: string, path: string, signal?: AbortSignal): Promise<RemoteFolder[]> =>
  allPages(cursor => callServer(api, 'GET /v1/admin/remote-sources/{id}/folders', {id, query: {path, cursor}, signal}));
/** Starts a Google sign-in with the owner's own OAuth client: open `address`, then paste the address the browser lands on. */
export const startDriveSignIn = (api: ServerRequester, clientId: string, clientSecret: string, signal?: AbortSignal): Promise<DriveSignInStarted> =>
  callServer(api, 'POST /v1/admin/remote-sources/drive-sign-in', {body: {clientId: clientId.trim(), clientSecret}, signal});

/** What a source can be added with, from the wizard's fields. A missing field is named for the form. */
export type RemoteSourceDraft =
  | Readonly<{kind: 'webdav'; name: string; address: string; user: string; password: string}>
  | Readonly<{kind: 'rclone'; name: string; config: string; path: string}>
  | Readonly<{kind: 'drive'; name: string; path: string} & ({serviceAccountKey: string} | {signIn: string; redirectedUrl: string})>;
export function remoteSourceCreate(draft: RemoteSourceDraft, preset: RemotePreset = defaultRemotePreset): RemoteSourceCreate | Readonly<{missing: string}> {
  const name = draft.name.trim();
  if (!name) return {missing: 'name'};
  const base = {name, preset};
  if (draft.kind === 'webdav') {
    const address = draft.address.trim();
    if (!/^https?:\/\/\S+$/i.test(address)) return {missing: 'address'};
    return {...base, kind: 'webdav', webdav: {address, ...(draft.user.trim() ? {user: draft.user.trim()} : {}), ...(draft.password ? {password: draft.password} : {})}};
  }
  if (draft.kind === 'rclone') {
    if (!/^\s*\[[^\]\n]+\]\s*$/m.test(draft.config) || !/^\s*type\s*=/m.test(draft.config)) return {missing: 'config'};
    return {...base, kind: 'rclone', rclone: {config: draft.config, ...(draft.path.trim() ? {path: draft.path.trim()} : {})}};
  }
  const path = draft.path.trim() ? {path: draft.path.trim()} : {};
  if ('serviceAccountKey' in draft) {
    if (!draft.serviceAccountKey.trim()) return {missing: 'serviceAccountKey'};
    return {...base, kind: 'drive', drive: {serviceAccountKey: draft.serviceAccountKey, ...path}};
  }
  if (!draft.redirectedUrl.trim()) return {missing: 'redirectedUrl'};
  return {...base, kind: 'drive', drive: {signIn: draft.signIn, redirectedUrl: draft.redirectedUrl.trim(), ...path}};
}

/** The libraries a folder can be put in, for Continue setup. */
export const listAdminLibraries = (api: ServerRequester, signal?: AbortSignal): Promise<AdminLibrary[]> =>
  allPages(cursor => callServer(api, 'GET /v1/admin/libraries', {query: {cursor}, signal}).then(page => page as AdminLibraryPage));

/** A library's folders on remote sources, as the server lists them (absent from an older answer). */
type RemoteFolderRef = Readonly<{source: string; path: string}>;
const remoteFoldersOf = (library: AdminLibrary): RemoteFolderRef[] =>
  library.folders.flatMap(folder => { const source = (folder as {source?: unknown}).source; return typeof source === 'string' && source ? [{source, path: folder.path}] : []; });

/** Puts a folder of a source in a library, keeping its other folders; the server scans it. */
export async function addRemoteFolderToLibrary(api: ServerRequester, libraryId: string, folder: RemoteFolderRef, signal?: AbortSignal): Promise<AdminLibrary> {
  const library = await callServer(api, 'GET /v1/admin/libraries/{id}', {id: libraryId, signal});
  const kept = remoteFoldersOf(library);
  if (kept.some(one => one.source === folder.source && one.path === folder.path)) return library;
  // The route takes the whole list of folders on remote sources (`remoteFolders`), beside the local ones.
  return holding(await callServer(api, 'PATCH /v1/admin/libraries/{id}', {id: libraryId, body: {remoteFolders: [...kept, folder]} as never, signal}), folder);
}
/** A server without folders on remote sources answers the library without the folder: that is not done, it is not available. */
function holding(library: AdminLibrary, folder: RemoteFolderRef): AdminLibrary {
  if (!remoteFoldersOf(library).some(one => one.source === folder.source && one.path === folder.path)) throw notOnServerYet();
  return library;
}
/** Makes a library from a folder of a source; it starts its first scan. */
export const createLibraryOnRemoteFolder = async (api: ServerRequester, library: Readonly<{name: string; kind: string}>, folder: RemoteFolderRef, signal?: AbortSignal): Promise<AdminLibrary> =>
  holding(await callServer(api, 'POST /v1/admin/libraries', {body: {name: library.name.trim(), kind: library.kind, folders: [], remoteFolders: [folder]} as never, signal}), folder);

/** The refusal for a step the server does not offer yet. Nothing is sent and nothing is pretended. */
export const notOnServerYet = (): Error => Object.assign(new Error('This is not available on this server yet.'), {code: 'not_on_server'});
export const isNotOnServerYet = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === 'not_on_server';
/** OneDrive or Dropbox with a token pasted from `rclone authorize` (rule 8). The server's field for it is being
 * built; until it is in the routes this answers that it is not available. */
export async function addCloudTokenSource(_api: ServerRequester, _source: Readonly<{name: string; provider: 'onedrive' | 'dropbox'; token: string; path: string; preset: RemotePreset}>,
  _signal?: AbortSignal): Promise<RemoteSource> { throw notOnServerYet(); }
/** A new rclone remote made from its kind and settings rather than pasted (rule 8). Being built on the server, as above. */
export async function addRcloneRemoteFromFields(_api: ServerRequester, _source: Readonly<{name: string; type: string; options: Readonly<Record<string, string>>; path: string; preset: RemotePreset}>,
  _signal?: AbortSignal): Promise<RemoteSource> { throw notOnServerYet(); }
