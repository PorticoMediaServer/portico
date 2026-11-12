// Saved tests written against the old server's documents keep pinning SavedService's own behaviour (paging, restore,
// quiet re-reads, commands in order): this transport asks the fixture with the old paths and bodies the service used
// to send, and hands its answers through as they are. Only test code uses this; the app's transport is saved-next.ts.
import {encodedRoutePath, routePath} from '../../../contracts/src/route-paths.ts';
import {resourceContentParams, type SavedApi, type SavedIntent, type SavedRoute} from '../../src/saved.ts';
import type {SavedTransport} from '../../src/saved-next.ts';

const playlistPath = (id: string) => encodedRoutePath('get_playlists_id', {id: encodeURIComponent(id)});

export const legacySavedTransport: SavedTransport = Object.freeze({
  async read(api: SavedApi, _bound: unknown, selected: SavedRoute, cursor: string | null, signal?: AbortSignal) {
    const params = new URLSearchParams({limit: '40'}); if (cursor) params.set('cursor', cursor);
    let path: string;
    if (selected.view === 'playlist') {path = encodedRoutePath('get_playlists_id_content', {id: encodeURIComponent(selected.playlistId)}); resourceContentParams(params, selected);}
    else {
      path = routePath('get_saved_content'); params.set('view', selected.view);
      if (selected.sort) params.set('sort', selected.sort); if (selected.direction) params.set('direction', selected.direction);
      if (selected.shape) params.set('shape', selected.shape); if (selected.filter) params.set('filter', selected.filter);
    }
    const [page, resource] = await Promise.all([api.request<unknown>(path + '?' + params, 'GET', undefined, signal),
      selected.view === 'playlist' ? api.request<unknown>(playlistPath(selected.playlistId), 'GET', undefined, signal) : Promise.resolve(null)]);
    return {page, resource};
  },
  async send(api: SavedApi, _bound: unknown, value: SavedIntent, context: {playlistId?: string; revision?: number}, operationId: string, signal?: AbortSignal) {
    let path: string = routePath('post_playlists'), method = 'POST';
    const body: Record<string, unknown> = {operationId, ...(value.action === 'create' ? {} : {expectedRevision: context.revision})};
    const id = encodeURIComponent(context.playlistId ?? '');
    if (value.action !== 'create') path = encodedRoutePath(value.action === 'rename' ? 'patch_playlists_id' : 'get_playlists_id', {id});
    switch (value.action) {
      case 'pin': path = encodedRoutePath('put_saved_pins_kind_id', {kind: 'playlist', id}); method = 'PUT'; body.pinned = value.pinned; break;
      case 'create': case 'rename': if (value.action === 'create' && value.itemIds) body.itemIds = value.itemIds; if (value.action === 'rename') method = 'PATCH'; if (value.name !== undefined) body.name = value.name; if (value.summary !== undefined) body.summary = value.summary; break;
      case 'add': path = encodedRoutePath('post_playlists_id_entries', {id}); body.itemId = value.itemId; break;
      case 'restore': path = encodedRoutePath('post_playlists_id_entries', {id}); body.undoToken = value.undoToken; break;
      case 'remove': path = encodedRoutePath('delete_playlists_id_entries_entryId', {id, entryId: encodeURIComponent(value.entryId)}); method = 'DELETE'; break;
      case 'reorder': path = encodedRoutePath('reorder_playlist_window', {id}); method = 'PUT'; body.entryIds = value.entryIds; if (value.afterEntryId !== undefined) body.afterEntryId = value.afterEntryId; break;
      case 'delete': path = encodedRoutePath('delete_playlists_id', {id}); method = 'DELETE'; break;
      case 'share': case 'unshare': path = encodedRoutePath(value.action === 'share' ? 'put_playlists_id_shares' : 'delete_playlists_id_shares', {id}); method = value.action === 'share' ? 'PUT' : 'DELETE'; Object.assign(body, value.actor); if (value.action === 'share') body.role = value.role; break;
    }
    return api.request<Record<string, unknown>>(path, method, body, signal);
  },
  async order(api: SavedApi, playlistId: string, cursor: string | null, signal?: AbortSignal, limit = 100) {
    const query = new URLSearchParams({limit: String(limit)}); if (cursor) query.set('cursor', cursor);
    return api.request<any>(encodedRoutePath('get_playlist_order', {id: encodeURIComponent(playlistId)}) + '?' + query, 'GET', undefined, signal);
  },
  async candidates(api: SavedApi, playlistId: string, cursor: string | null, signal?: AbortSignal) {
    const query = new URLSearchParams({limit: '40'}); if (cursor) query.set('cursor', cursor);
    return api.request<any>(encodedRoutePath('get_playlists_id_share_candidates', {id: encodeURIComponent(playlistId)}) + '?' + query, 'GET', undefined, signal);
  },
} as unknown as SavedTransport);
