// List changes from the web Saved screen go through its runtime's transport wrapper, and still name the list's revision
// (area 6 rule 34, decision R4).
import test from 'node:test';
import assert from 'node:assert/strict';
import {createSavedRuntime} from '../src/screens/saved/runtime.ts';
import {nextSavedServer} from '../../packages/client-core/test/fixtures/next-saved-server.ts';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};

test('a rename on the web Saved screen names the playlist revision it was made on', async () => {
  const s = nextSavedServer(), runtime = createSavedRuntime({scope, isCurrent: () => true, api: s.api});
  try {
    await runtime.saved.select({view: 'playlist', playlistId: 'pl'});
    assert.equal(runtime.saved.getSnapshot().phase, 'ready', JSON.stringify(runtime.saved.getSnapshot().error));
    await runtime.saved.mutate({action: 'rename', name: 'Long drive'});
    const edit = s.calls.find(call => call.method === 'PATCH');
    assert.deepEqual([edit?.path, edit?.headers], ['/v1/lists/pl', {'If-Match': '"7"'}]);
  } finally { runtime.dispose(); }
});

test('once the web Saved lifetime ends, its header-sending transport sends nothing', async () => {
  let current = true;
  const s = nextSavedServer(), runtime = createSavedRuntime({scope, isCurrent: () => current, api: s.api});
  try {
    await runtime.saved.select({view: 'playlist', playlistId: 'pl'});
    const before = s.calls.length; current = false;
    await runtime.saved.mutate({action: 'rename', name: 'Long drive'}).catch(() => {});
    assert.equal(s.calls.slice(before).some(call => call.method === 'PATCH'), false);
  } finally { runtime.dispose(); }
});
