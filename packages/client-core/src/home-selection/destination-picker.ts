import type {ServerRequester} from '../query/server-api.ts';
import {validateContentEntry} from '../library-content.ts';
import {selectionExecutionError} from './execution.ts';
import {readDestinationPage, type DestinationPage} from './list-destinations.ts';

export type HomeDestinationPickerRequest = Readonly<{kind: 'playlist' | 'collection'; query: string; cursor?: string; limit: number; itemIds?: readonly string[]}>;
export type HomeDestinationPickerPage = DestinationPage;
/** One page of the add-to sheet's rows: the profile's playlists or collections whose names hold the query, from the
 * rebuilt server's lists (`list-destinations.ts`). Lists carry their own revisions, so there is no fence to pass. */
export function fetchHomeDestinationPicker(api: ServerRequester, request: HomeDestinationPickerRequest, _serverId?: string, _viewerFence?: string, signal?: AbortSignal): Promise<HomeDestinationPickerPage> {
  return readDestinationPage(api, {kind: request.kind, query: request.query, ...(request.cursor ? {cursor: request.cursor} : {}), limit: request.limit}, signal);
}
/** What an add-to sheet opened from a title or a library selection holds as its selection's privacy fence. Lists need
 * none (each carries its revision), so it is the opened marker every other selection uses. */
export const destinationFence = 'opened';

/** The old server's destination-picker answer, kept only for the reader's add-to-collection (`reading-collection.ts`)
 * until it moves onto `list-destinations.ts`. */
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const id = (value: unknown): value is string => typeof value === 'string' && !!value && value.length <= 256 && !/[\x00-\x1f\x7f]/.test(value);
function bad(): never {throw selectionExecutionError('invalid-destination-picker');}
export function parseHomeDestinationPicker(raw: unknown, request: HomeDestinationPickerRequest, serverId: string, viewerFence: string): HomeDestinationPickerPage {
  if (!record(raw) || raw.serverId !== serverId || raw.viewerFence !== viewerFence || raw.kind !== request.kind || raw.query !== request.query || !record(raw.revision) || !count(raw.revision.catalog) || !count(raw.revision.viewer) || !Array.isArray(raw.rows) || raw.rows.length > request.limit || typeof raw.nextCursor !== 'string' || raw.nextCursor.length > 8192) bad();
  const rows = raw.rows.map(row => {
    if (!record(row) || !id(row.id) || typeof row.name !== 'string' || !row.name || row.name.length > 800 || !count(row.count) || !count(row.revision) || row.revision < 1 || typeof row.canAdd !== 'boolean' || !Array.isArray(row.actions) || row.actions.length > 16 || !row.actions.every(id) || !Array.isArray(row.artwork) || row.artwork.length > 4 || row.alreadyIn !== undefined && (!count(row.alreadyIn) || !request.itemIds || row.alreadyIn > request.itemIds.length)) bad();
    if (row.canAdd !== row.actions.includes(request.kind === 'playlist' ? 'add' : 'entries')) bad();
    return Object.freeze({id: row.id, name: row.name, count: row.count, revision: row.revision, canAdd: row.canAdd, artwork: Object.freeze(row.artwork.map(validateContentEntry)), ...(row.alreadyIn === undefined ? {} : {alreadyIn: row.alreadyIn as number})});
  });
  if (new Set(rows.map(row => row.id)).size !== rows.length) bad();
  return Object.freeze({rows: Object.freeze(rows), nextCursor: raw.nextCursor, revision: Object.freeze({catalog: raw.revision.catalog, viewer: raw.revision.viewer})});
}
