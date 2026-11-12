// List changes from the native Saved view go through its transport wrapper, and still name the list's revision
// (area 6 rule 34, decision R4).
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeSavedViewDestination} from '../src-next/screens/saved/view-model.ts';
import {nextSavedServer} from '../../../packages/client-core/test/fixtures/next-saved-server.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const requestId = async () => '00000000-0000-4000-8000-000000000001';

test('a rename on the native Saved view names the playlist revision it was made on', async () => {
  const s = nextSavedServer(), view = new NativeSavedViewDestination({api: s.api, scope, requestId, current: () => true});
  try {
    await view.select({view: 'playlist', playlistId: 'pl'});
    assert.equal(view.getSnapshot().phase, 'ready');
    await view.mutateSaved({action: 'rename', name: 'Long drive'});
    const edit = s.calls.find(call => call.method === 'PATCH');
    assert.deepEqual([edit?.path, edit?.headers], ['/v1/lists/pl', {'If-Match': '"7"'}]);
  } finally { view.dispose(); }
});
