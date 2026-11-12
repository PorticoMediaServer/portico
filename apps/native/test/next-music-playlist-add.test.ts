// Adding a song to a playlist from Music goes through a transport that sends `request` alone. An addition overwrites
// nothing, so it is sent as before (without an Idempotency-Key) and succeeds.
import test from 'node:test';
import assert from 'node:assert/strict';
import {MusicPlaylistPort} from '../src-next/screens/destinations/music/playlist-port.ts';
import {nextSavedServer} from '../../../packages/client-core/test/fixtures/next-saved-server.ts';

test('adding a song to a playlist from Music is sent and succeeds', async () => {
  const s = nextSavedServer();
  const port = new MusicPlaylistPort({api: {request: s.api.request}, scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true,
    requestId: async () => '00000000-0000-4000-8000-000000000001'} as never);
  try {
    await port.add('pl', 'song-1', 'Road trip');
    const add = s.calls.find(call => call.method === 'POST');
    assert.deepEqual([add?.path, add?.body, add?.headers], ['/v1/lists/pl/entries', {items: ['song-1']}, undefined]);
    assert.equal(port.getSnapshot().error, undefined);
    assert.equal(port.getSnapshot().leaf, undefined, 'nothing is left to retry');
  } finally {port.dispose();}
});
