// Saved on the rebuilt server: what SavedService reads and sends, and that the screens' own checks accept it.
import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedService} from '../src/saved.ts';
import {readPlaylistOrder, readShareCandidates, sendPlaylistCommand} from '../src/saved-next.ts';

import {nextSavedServer as server} from './fixtures/next-saved-server.ts';

const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
const service = (api: unknown) => new SavedService({scope, api: api as never, requestId: async () => '00000000-0000-4000-8000-000000000001', timeoutMs: 1000});

test('My List is read from the rebuilt server with real filter and shape counts, and its page passes the Saved checks', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'watchlist', filter: 'unwatched', shape: 'posters', sort: 'title', direction: 'asc'});
  const shot = saved.getSnapshot();
  assert.equal(shot.phase, 'ready', JSON.stringify(shot.error));
  assert.deepEqual(shot.projection!.sections[0]!.entries.map(entry => entry.id), ['one', 'two']);
  assert.deepEqual(shot.projection!.filters[0]!.options.map(option => [option.id, option.count]), [['all', 5], ['unwatched', 5], ['inProgress', 5], ['watched', 5], ['favorites', 5]]);
  assert.deepEqual(shot.projection!.shapeCounts, {posters: 5, squares: 5});
  const page = new URL(s.calls.find(call => new URL(call.path, 'https://x').searchParams.get('limit') === '40')!.path, 'https://x').searchParams;
  assert.deepEqual([page.get('inList'), page.get('watched'), page.get('sort'), page.get('order'), page.get('kind')], ['true', 'false', 'title', 'asc', 'movie,show,season,episode,audiobook,book,comic,series,collection']);
  saved.dispose();
});

test('a playlist page lists its entries in its own order with the last entry, and the playlist says what its maker may do', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'playlist', playlistId: 'pl'});
  const shot = saved.getSnapshot();
  assert.equal(shot.phase, 'ready', JSON.stringify(shot.error));
  assert.deepEqual(shot.projection!.sections[0]!.entries.map(entry => entry.id), ['e1', 'e2', 'e3'], 'a title twice is two entries');
  assert.deepEqual(shot.projection!.sections[0]!.order, {last: 'e3'});
  assert.deepEqual([shot.playlist!.revision, shot.playlist!.role, shot.playlist!.actions.includes('pin')], [7, 'owner', false]);
  assert.deepEqual(shot.playlist!.shares?.map(share => [share.profileId, share.role, share.displayName]), [['kid', 'viewer', 'Kid']]);
  saved.dispose();
});

test('the playlists list holds the profile\'s own and shared ones, and Pinned waits for the pins setting', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'playlists', filter: 'mine'});
  assert.deepEqual(saved.getSnapshot().projection!.sections[0]!.entries.map(entry => entry.id), ['pl']);
  await saved.select({view: 'playlists', filter: 'pinned'});
  assert.equal(saved.getSnapshot().error?.code, 'not_available');
  saved.dispose();
});

test('an edit names the list\'s revision, and one on an out-of-date copy is refused as "This list changed"', async () => {
  const s = server();
  await sendPlaylistCommand(s.api, scope, {action: 'rename', name: ' Long drive '}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1), {path: '/v1/lists/pl', method: 'PATCH', body: {name: 'Long drive'}, headers: {'If-Match': '"7"'}});
  const stale = {request: s.api.request, requestRaw: async () => ({status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'This list changed. Reload and try again.'}}})};
  await assert.rejects(sendPlaylistCommand(stale, scope, {action: 'delete'}, {playlistId: 'pl', revision: 6}, 'op'), {code: 'list_changed', status: 412});
});

test('an edit through a transport that cannot send headers is refused before anything is sent', async () => {
  // The Saved screens wrap the app's transport; a wrapper that dropped `requestRaw` must not let an edit go without the
  // list's revision, where an edit on an out-of-date copy would overwrite.
  const sent: {path: string; method: string; body: unknown}[] = [];
  const withoutHeaders = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    sent.push({path, method, body});
    return {id: 'pl', kind: 'playlist', name: 'Long drive', role: 'owner', count: 0, covers: [], revision: '8'} as T;
  }};
  await assert.rejects(sendPlaylistCommand(withoutHeaders, scope, {action: 'rename', name: 'Long drive'}, {playlistId: 'pl', revision: 7}, 'op'), {code: 'saved_unsupported'});
  assert.deepEqual(sent, [], 'nothing may be sent without the revision');
  const s = server();
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'rename', name: 'Long drive'}, {playlistId: 'pl'}, 'op'), {code: 'list_changed'}, 'an edit with no revision to name');
  assert.deepEqual(s.calls, []);
});

test('removing an entry offers an Undo that puts its title back after the entry in front of it', async () => {
  const s = server(), entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}, {id: 'e3', itemId: 'a'}];
  const removed = await sendPlaylistCommand(s.api, scope, {action: 'remove', entryId: 'e2'}, {playlistId: 'pl', revision: 7, entries}, 'op');
  assert.equal(s.calls.at(-1)!.path, '/v1/lists/pl/entries/e2'); assert.equal(s.calls.at(-1)!.method, 'DELETE');
  assert.ok(Date.parse(removed.undoUntil as string) - Date.now() <= 30_000);
  await sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'op2');
  assert.deepEqual(s.calls.at(-1)!.body, {items: ['b'], after: 'e1'});
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: 'nonsense'}, {playlistId: 'pl'}, 'op3'), {code: 'undo_expired'});
});

test('a move places entries after an anchor, at the front, or swaps two neighbours in place', async () => {
  const entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}, {id: 'e3', itemId: 'c'}];
  const moves = async (intent: {entryIds: string[]; afterEntryId?: string}) => {
    const s = server(); await sendPlaylistCommand(s.api, scope, {action: 'reorder', ...intent}, {playlistId: 'pl', revision: 7, entries}, 'op');
    return s.calls.map(call => [call.path.split('/').pop(), call.body, call.headers?.['If-Match']]);
  };
  assert.deepEqual(await moves({entryIds: ['e3'], afterEntryId: 'e1'}), [['e3', {after: 'e1'}, '"7"']]);
  assert.deepEqual(await moves({entryIds: ['e3'], afterEntryId: ''}), [['e3', {before: 'e1'}, '"7"']]);
  assert.deepEqual(await moves({entryIds: ['e3', 'e2']}), [['e3', {after: 'e1'}, '"7"'], ['e2', {after: 'e3'}, '"8"']], 'each move names the revision the one before answered');
});

test('sharing sets who a playlist is shared with, keeping the others; pinning waits for the pins setting', async () => {
  const s = server();
  await sendPlaylistCommand(s.api, scope, {action: 'share', actor: {authority: 'local', accountId: 'gran', profileId: 'gran'}, role: 'editor'}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1)!.body, {shares: [{profileId: 'kid', canEdit: false}, {profileId: 'gran', canEdit: true}]});
  await sendPlaylistCommand(s.api, scope, {action: 'unshare', actor: {authority: 'local', accountId: 'kid', profileId: 'kid'}}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1)!.body, {shares: []});
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'pin', pinned: true}, {playlistId: 'pl'}, 'op'), {code: 'not_available'});
  const created = await sendPlaylistCommand(s.api, scope, {action: 'create', name: 'New', itemIds: ['a']}, {}, 'op');
  assert.deepEqual([s.calls.at(-1)!.path, s.calls.at(-1)!.body], ['/v1/lists', {kind: 'playlist', name: 'New', items: ['a']}]);
  assert.equal(created.playlistId, 'pl');
});

test('the order and the share picker read the playlist\'s own entries and everyone it is not shared with yet', async () => {
  const s = server();
  assert.deepEqual(await readPlaylistOrder(s.api, 'pl', null), {revision: 7, entryIds: ['e1', 'e2', 'e3'], nextCursor: ''});
  const picker = await readShareCandidates(s.api, 'pl', null);
  assert.deepEqual(picker.candidates.map((person: any) => [person.profileId, person.displayName]), [['gran', 'Gran']]);
});
