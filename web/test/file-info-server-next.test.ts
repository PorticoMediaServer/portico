import test from 'node:test';
import assert from 'node:assert/strict';
import {createDetailBindings} from '../src/screens/detail/bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

test('the web binding opens File info from a real new-server Detail, without an opening token', async () => {
  const calls: string[] = [];
  const item = {id: 'movie', kind: 'movie', libraryId: 'library', title: 'A film', images: {}, addedAt: '',
    state: {watched: false, favorite: false, inList: false, playCount: 0}, versions: [
      {id: 'version', size: 1000, durationMs: 30000, container: 'mkv', parts: [{size: 1000, streams: []}]}]};
  const context = withQueries({api: {async request<T>(path: string) {calls.push(path); return item as T;}},
    scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry() {}, openSettings() {}, play: async () => {}, reportError() {}},
  }) as unknown as ScreenContext;
  const binding = createDetailBindings(context);
  try {
    await binding.select({itemId: 'movie', libraryId: 'library', kind: 'movie'});
    const data = binding.service.getSnapshot().data!;
    assert.ok(data); assert.equal('fileInfoOpening' in data, false);
    const panel = binding.openFileInfo(data)!; await panel.groups();
    assert.equal(panel.getSnapshot().groups.status, 'ready');
    assert.equal(panel.getSnapshot().groups.rows!.rows[0].id, 'version');
    assert.equal(calls.every(path => path === '/v1/items/movie'), true);
    binding.dispose();
    const reads = calls.length; await panel.groups(); assert.equal(calls.length, reads); panel.retire();
  } finally {binding.dispose();}
});
