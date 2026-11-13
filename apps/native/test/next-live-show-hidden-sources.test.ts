import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';

test('Show hidden changes the profile-hidden channel rows and their source counts together', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const reads: string[] = [];
  const visible = {id: 'visible', name: 'Visible', number: '1', kind: 'source', favorite: false, hidden: false};
  const hidden = {...visible, id: 'hidden', name: 'Hidden', number: '2', hidden: true};
  const api = {request: async <T,>(path: string) => {
    reads.push(path);
    const rows = new URL(path, 'https://server').searchParams.get('hidden') === 'true' ? [visible, hidden] : [visible];
    if (path.startsWith('/v1/live/channels')) return {items: rows,
      sources: [{id: 'aerial', name: 'Aerial', kind: 'live', channels: rows.length, favorites: 0, groups: []}]} as T;
    if (path.startsWith('/v1/live/guide?')) return {items: rows.map(channel => ({channel, programmes: []}))} as T;
    throw new Error('Unexpected read '+path);
  }};
  const controller = new NativeLiveController(lifetime, api as never, 'America/Halifax');
  const check = async (count: number) => {
    await controller.loadSources();
    assert.equal(controller.getSnapshot().sources[0].channelCount, count);
    const store = controller.guide;
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {off(); reject(new Error('channel rows did not arrive'));}, 5000);
      const ready = () => {if (store.getSnapshot().total === count) {clearTimeout(timer); off(); resolve();}};
      const off = store.subscribe(ready);
      controller.setViewport({firstRow: 0, lastRow: 1, start: Date.now(), end: Date.now() + 60_000});
      ready();
    });
    assert.equal(store.channelAt(0)?.id, 'visible');
    assert.equal(store.channelAt(1)?.id, count === 2 ? 'hidden' : undefined);
  };
  try {
    await check(1);
    controller.select({includeServerHidden: true});
    await check(2);
    assert.equal(controller.getSnapshot().options.includeHidden, true);
    controller.select({includeServerHidden: false});
    await check(1);
    assert.ok(reads.some(path => new URL(path, 'https://server').searchParams.get('hidden') === 'true'));
  } finally {controller.dispose(); lifetime.dispose();}
});
