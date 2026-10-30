/** Follow-up 2 (Q7): SavedService.mutate rejects on retry-required/conflict.
 * Apple EntryMenu PlaylistSheet surfaces that rejection in its existing error
 * Notice (run() try/catch -> setError(say(...))), never a silent catch, and
 * the queue stays unblocked so the next command still lands. This pins the
 * service contract from Apple's import path; the sheet's run() mirrors the
 * web picker's handled path (code-reviewed, same try/catch shape). */
import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedService} from '../../../packages/client-core/src/saved.ts';

const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
let ids = 0;
const requestId = async () => `00000000-0000-4000-8000-${String(++ids).padStart(12, '0')}`;
const occurrence = (n: number) => ({id: `e${n}`, kind: 'playlist_entry', hidden: false, media: {id: `m${n}`, libraryId: 'library', kind: 'movie', title: `Movie ${n}`, available: true, playback: {itemId: `m${n}`, startSeconds: 0}}});
const projection = () => ({
  scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'playlist', entityId: 'playlist', viewerFence: 'fence'},
  revision: {catalog: 0, viewer: 0}, playlistRevision: 0,
  heading: {key: 'saved.title', fallback: 'Saved'},
  navigation: [
    {id: 'watchlist', labelKey: 'saved.watchlist', view: 'watchlist'},
    {id: 'favorites', labelKey: 'saved.favorites', view: 'favorites'},
    {id: 'playlists', labelKey: 'saved.playlists', view: 'playlists'},
  ],
  query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
  sorts: [], filters: [],
  sections: [{id: 'entries', type: 'list', heading: {key: 'saved.entries', fallback: 'Entries'}, entries: [occurrence(0)], totalCount: 2, nextCursor: ''}],
});
const resource = () => ({
  serverId: 'server', viewerFence: 'fence', id: 'playlist', name: 'Playlist', summary: '', revision: 0,
  role: 'owner', entryCount: 2, actions: ['rename', 'summary', 'delete', 'share', 'add', 'remove', 'reorder'],
  limits: {maxEntries: 1000, maxShares: 100, maxReorderEntries: 1000}, shares: [],
});

test('a rejected playlist add surfaces mutationError and the next add still lands', async () => {
  const posts: string[] = [];
  const api = {
    request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
      if ((method ?? 'GET') === 'GET') return (path.includes('/content') ? projection() : resource()) as T;
      const itemId = (body as {itemId?: string}).itemId!;
      posts.push(itemId);
      if (itemId === 'bad') throw Object.assign(new Error('denied'), {code: 'denied'});
      const b = body as {operationId: string};
      return {serverId: 'server', viewerFence: 'fence', operationId: b.operationId, playlistId: 'playlist', revision: 0, deleted: false} as T;
    },
  };
  const service = new SavedService({api, scope, requestId, timeoutMs: 500});
  await service.select({view: 'playlist', playlistId: 'playlist'});
  // The sheet's run() awaits mutate inside try/catch and shows say(e) in its
  // Notice; here we pin the contract it relies on: the failure rejects (so it
  // cannot be mistaken for success) and lands in snapshot.mutationError.
  await assert.rejects(service.mutate({action: 'add', itemId: 'bad'}), /denied/);
  assert.deepEqual(service.getSnapshot().pending, [], 'failed command leaves the queue');
  assert.equal(service.getSnapshot().mutationError?.code, 'denied');
  await service.mutate({action: 'add', itemId: 'good'});
  assert.deepEqual(posts, ['bad', 'good'], 'the second command proceeds past the failure');
  service.dispose();
});
