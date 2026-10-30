import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';

const source = (id: string) => ({id, name: id, generation: 'one', publishedAt: '2026-10-26T00:00:00Z', availableStart: '2026-10-26T00:00:00Z', availableEnd: '2026-10-27T00:00:00Z', provenance: 'live-source', refreshState: 'manual', position: 0, recordAvailable: false, guideDays: 1, channelCount: 1, groups: [], groupCounts: [], favoriteCount: 0, unavailableReasons: {}});

// When every channel is hidden for everyone, the plain read has no sources. Turning
// Show hidden channels on must read the sources again, or the guide stays empty.
test('Show hidden channels reloads the source summaries for the owner', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const reads: string[] = [];
  const api = {request: async <T,>(path: string) => {
    if (path.includes('/guide/sources')) {
      reads.push(path);
      const hidden = path.includes('includeServerHidden=true');
      return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: hidden ? 'two' : 'one', sources: hidden ? [source('aerial')] : []} as T;
    }
    return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: 'one', total: 0, offset: 0, limit: 50, channels: [], sources: []} as T;
  }};
  const controller = new NativeLiveController(lifetime, api as never, 'America/Halifax');
  await controller.loadSources();
  assert.equal(controller.getSnapshot().sources.length, 0);
  controller.select({includeServerHidden: true});
  for (let i = 0; i < 20 && controller.getSnapshot().sources.length === 0; i++) await new Promise(resolve => setImmediate(resolve));
  assert.equal(controller.getSnapshot().sources.length, 1, `reads: ${reads.join(' | ')}`);
  assert.ok(reads.at(-1)!.includes('includeServerHidden=true'));
  controller.dispose?.();
});
