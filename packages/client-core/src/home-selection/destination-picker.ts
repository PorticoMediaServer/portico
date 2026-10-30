import {routePath} from '../../../contracts/src/route-paths.ts';
import type {HomeApi} from '../home.ts';
import {validateContentEntry} from '../library-content.ts';
import type {HomeSelectionPickerRow} from './types.ts';
import {selectionExecutionError} from './execution.ts';
export type HomeDestinationPickerRequest = Readonly<{kind: 'playlist' | 'collection'; query: string; cursor?: string; limit: number; itemIds?: readonly string[]}>;
export type HomeDestinationPickerPage = Readonly<{rows: readonly HomeSelectionPickerRow[]; nextCursor: string; revision: Readonly<{catalog: number; viewer: number}>}>;
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
export async function fetchHomeDestinationPicker(api: HomeApi, request: HomeDestinationPickerRequest, serverId: string, viewerFence: string, signal?: AbortSignal): Promise<HomeDestinationPickerPage> {
  if (!Number.isSafeInteger(request.limit) || request.limit < 1 || request.limit > 40 || [...request.query].length > 100 || /[\x00-\x1f\x7f-\x9f]/.test(request.query)) throw selectionExecutionError('invalid-destination-query');
  return parseHomeDestinationPicker(await api.request(routePath('post_home_destination_picker'), 'POST', request, signal), request, serverId, viewerFence);
}
