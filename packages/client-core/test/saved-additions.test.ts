// Adding to a playlist and making one still work from transports that cannot send headers (the pickers outside the
// Saved screens): an addition overwrites nothing, so it is sent as before, without its Idempotency-Key. An edit that
// must name the list's revision is still refused there (area 6 rule 34).
import test from 'node:test';
import assert from 'node:assert/strict';
import {sendPlaylistCommand} from '../src/saved-next.ts';
import {HomeSelectionDestinations} from '../src/home-selection/destinations.ts';
import {list, nextSavedServer} from './fixtures/next-saved-server.ts';

const scope = {serverId: 's', viewerId: 'v'};
/** The rebuilt server, reached through `request` alone. */
function plain(answers: Parameters<typeof nextSavedServer>[0] = {}) {
  const s = nextSavedServer(answers);
  return {calls: s.calls, api: {request: s.api.request}};
}

test('through a transport without headers, add, put back and make are sent and succeed; edits are refused unsent', async () => {
  const s = plain(), entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}];
  const added = await sendPlaylistCommand(s.api, scope, {action: 'add', itemId: 'z'}, {playlistId: 'pl', revision: 7}, 'op-add');
  assert.deepEqual([s.calls.at(-1)!.method, s.calls.at(-1)!.path, s.calls.at(-1)!.body, s.calls.at(-1)!.headers], ['POST', '/v1/lists/pl/entries', {items: ['z']}, undefined]);
  assert.equal(added.revision, 8);
  const created = await sendPlaylistCommand(s.api, scope, {action: 'create', name: 'New', itemIds: ['a']}, {}, 'op-create');
  assert.deepEqual([s.calls.at(-1)!.path, s.calls.at(-1)!.body], ['/v1/lists', {kind: 'playlist', name: 'New', items: ['a']}]);
  assert.equal(created.playlistId, 'pl');
  // An Undo made on the Saved screen can be put back from here too.
  const token = 'undo.' + btoa(JSON.stringify({listId: 'pl', itemId: 'b', after: 'e1'})).replace(/=+$/, '');
  await sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: token}, {playlistId: 'pl'}, 'op-restore');
  assert.deepEqual(s.calls.at(-1)!.body, {items: ['b'], after: 'e1'});
  const writes = () => s.calls.filter(call => call.method !== 'GET').length, sent = writes();
  for (const intent of [{action: 'rename', name: 'X'}, {action: 'remove', entryId: 'e2'}, {action: 'reorder', entryIds: ['e2'], afterEntryId: ''}, {action: 'delete'},
    {action: 'share', actor: {authority: 'local', accountId: 'k', profileId: 'k'}, role: 'viewer'}] as const)
    await assert.rejects(sendPlaylistCommand(s.api, scope, intent as never, {playlistId: 'pl', revision: 7, entries}, 'op'), {code: 'saved_unsupported'}, intent.action);
  assert.equal(writes(), sent, 'no edit was sent (sharing only read who it is shared with)');
});

test('making a playlist from a selection picker (web title and library pickers, Home selection) makes it with the titles in it', async () => {
  const s = plain({
    'GET /v1/lists': () => ({items: []}),
    'POST /v1/lists': call => list({id: 'new', name: call.body.name, count: call.body.items.length, revision: '1'}),
  });
  const target = {mode: 'explicit', items: [{item: {kind: 'movie', id: 'm1', libraryId: 'lib'}}],
    context: {scope: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', viewId: 'view', privacyFence: 'opened'}, revision: {catalogRevision: '0', viewerRevision: '0'}}};
  let shown: any;
  const session = new HomeSelectionDestinations({api: s.api, requestId: async () => '00000000-0000-4000-8000-000000000001'} as never, target as never, 'playlist',
    new AbortController().signal, () => true, state => {shown = state;});
  try {
    await session.load();
    await session.create('New');
    const made = s.calls.filter(call => call.method === 'POST');
    assert.deepEqual(made.map(call => [call.path, call.body, call.headers]), [['/v1/lists', {kind: 'playlist', name: 'New', items: ['m1']}, undefined]], 'one request makes it with the title in it');
    assert.deepEqual([shown.phase, shown.destination?.id, shown.receipt?.added], ['added', 'new', 1]);
  } finally {session.dispose();}
});
