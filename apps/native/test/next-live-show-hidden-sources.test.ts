import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';

// Profile-hidden channels stay in source availability; owner-hidden channels never enter this viewer read.
test('source availability retains a source whose channels are hidden by this profile', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const reads: string[] = [];
  const api = {request: async <T,>(path: string) => {
    if (path.startsWith('/v1/live/channels?')) {
      reads.push(path);
      assert.equal(new URL(path, 'https://server').searchParams.get('hidden'), 'true');
      return {items: [], sources: [{id: 'aerial', name: 'Aerial', kind: 'live', channels: 1, favorites: 0, groups: []}]} as T;
    }
    throw new Error('Unexpected read '+path);
  }};
  const controller = new NativeLiveController(lifetime, api as never, 'America/Halifax');
  await controller.loadSources();
  assert.equal(controller.getSnapshot().sources.length, 1);
  assert.deepEqual(controller.getSnapshot().sources.map(source => source.id), ['aerial']);
  assert.equal(reads.length, 1);
  controller.dispose?.();
});
