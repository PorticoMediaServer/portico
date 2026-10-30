import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueuePlaylistSave, queueSaveMessage, saveQueueAsPlaylist, type QueuePlaylistSave} from '../src/playback-v1/queue.ts';

test('NEW-37: a long save is followed by replaying the same key until it is saved, with progress each time', async () => {
  const keys: string[] = [];
  const answers = [
    {playlistId: 'pl1', revision: '1', entries: 200, total: 1001, state: 'saving'},
    {playlistId: 'pl1', revision: '2', entries: 600, total: 1001, state: 'saving'},
    {playlistId: 'pl1', revision: '3', entries: 1001, total: 1001, state: 'saved'},
  ];
  const client = {saveAsPlaylist: async (_id: string, _name: string, key?: string) => { keys.push(key!); return parseQueuePlaylistSave(answers[keys.length - 1]); }};
  const seen: QueuePlaylistSave[] = [];
  const done = await saveQueueAsPlaylist(client, 'q1', 'Road trip', {key: 'K', onProgress: s => seen.push(s), waitMs: () => 0});
  assert.deepEqual(keys, ['K', 'K', 'K'], 'one save, replayed by its key');
  assert.equal(done.state, 'saved');
  assert.deepEqual(seen.map(s => s.entries), [200, 600, 1001]);
  assert.deepEqual(queueSaveMessage(seen[0]!), {id: 'player.queueSavingProgress', values: {done: 200, total: 1001}});
  assert.deepEqual(queueSaveMessage(done), {id: 'player.queueSaved'});
});

test('NEW-37: a queue that changed during the copy fails with its own message; a short save is done at once', async () => {
  const changed = await saveQueueAsPlaylist({saveAsPlaylist: async () => parseQueuePlaylistSave({playlistId: 'p', revision: '1', entries: 400, total: 900, state: 'failed', errorCode: 'queue_changed'})}, 'q', 'n', {waitMs: () => 0});
  assert.deepEqual(queueSaveMessage(changed), {id: 'player.queueSaveChanged'});
  assert.deepEqual(queueSaveMessage(parseQueuePlaylistSave({playlistId: 'p', revision: '1', entries: 1, state: 'failed', errorCode: 'other'})), {id: 'player.queueSaveFailed'});
  let calls = 0;
  const short = await saveQueueAsPlaylist({saveAsPlaylist: async () => { calls++; return parseQueuePlaylistSave({playlistId: 'p', revision: '1', entries: 12, state: 'saved'}); }}, 'q', 'n');
  assert.equal(calls, 1);assert.equal(short.entries, 12);
  // An unknown state is read as done (the playlist exists); a missing playlist id is unreadable.
  assert.equal(parseQueuePlaylistSave({playlistId: 'p', entries: 3, state: 'archived'}).state, 'saved');
  assert.throws(() => parseQueuePlaylistSave({entries: 3, state: 'saved'}));
});
