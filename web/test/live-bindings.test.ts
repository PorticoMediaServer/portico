import test from 'node:test';
import assert from 'node:assert/strict';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {createLiveBindings} from '../src/screens/live/bindings.ts';
import {parseLiveScreenSearch} from '../src/screens/live/route-search.ts';
import {screenEntryDestination} from '../src/screens/browse/navigation.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

test('Live accepts an actual channel identity without inventing its source or provenance', () => {
  assert.deepEqual(parseLiveScreenSearch({channelId: 'opaque/channel', token: 'unowned'}), {channelId: 'opaque/channel'});
  assert.deepEqual(parseLiveScreenSearch({sourceId: 'actual-source', kind: 'library-channel', view: 'guide', group: 'Drama'}),
    {sourceId: 'actual-source', kind: 'library-channel', view: 'guide', group: 'Drama'});
  assert.deepEqual(parseLiveScreenSearch({tab: 'recordings', sourceId: 'actual-source', kind: 'live-source'}), {tab: 'recordings', sourceId: 'actual-source', kind: 'live-source'});
  assert.throws(() => parseLiveScreenSearch({tab: 'invented'}));
  for (const raw of [{view: 'invented'}, {kind: 'library'}, {favorites: 'true'}, {channelId: ['opaque']}, {sourceId: 'x\n'}]) assert.throws(() => parseLiveScreenSearch(raw));
});
test('Search navigation preserves only its canonical channel tuple and keeps Person separate', () => {
  assert.deepEqual(screenEntryDestination({id: 'channel/a', kind: 'channel', title: 'Actual channel',
    navigation: {view: 'channel', entityId: 'channel/a', channelId: 'channel/a', sourceId: 'actual-source', provenance: 'live-source'}}),
  {to: '/live', search: {sourceId: 'actual-source', channelId: 'channel/a', kind: 'live-source'}});
  assert.deepEqual(screenEntryDestination({id: 'legacy', kind: 'channel', title: 'Actual legacy channel', navigation: {view: 'channel', entityId: 'legacy'}}),
    {to: '/live', search: {channelId: 'legacy'}});
  assert.deepEqual(screenEntryDestination({id: 'person/a', kind: 'person', title: 'Actual person', navigation: {view: 'person', entityId: 'person/a'}}),
    {to: '/person/$personId', params: {personId: 'person/a'}});
});
test('a channel a link names is read from the guide around it, with what is on it now, and is never guessed', async () => {
  const requests: URL[] = [];let present = true;
  const now = Date.now(), at = (ms: number) => new Date(ms).toISOString();
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request(path: string) {
      const url = new URL(path, 'https://example.test');requests.push(url);
      const channel = {id: 'actual-channel', name: 'Actual channel', number: '1', kind: 'live', favorite: false, hidden: false};
      return {outOfDate: false, items: present ? [{channel, programmes: [{id: 'pg', title: 'On now', startsAt: at(now - 60_000), endsAt: at(now + 600_000)}]}] : []};
    }}}) as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'all', sourceId: '', timezone: 'UTC'});
  try {
    const found = await owner.selectedChannel({channelId: 'actual-channel'});
    assert.deepEqual([found?.row.id, found?.now?.title], ['actual-channel', 'On now']);
    assert.equal(requests[0].pathname, '/v1/live/guide');
    assert.equal(requests[0].searchParams.get('around'), 'actual-channel');
    present = false;assert.equal(await owner.selectedChannel({channelId: 'actual-channel'}), undefined);
    assert.equal(requests.length, 2, 'one read, no guessing through other sources');
  } finally {owner.dispose();}
});
test('Live construction is inert and an authority refusal retires the bounded guide before further HTTP', async () => {
  let reads = 0;
  const denied = Object.assign(new Error('denied'), {status: 403});
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request() {reads++;throw denied;}}}) as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'all', sourceId: '', timezone: 'UTC'});
  assert.equal(reads, 0);
  await assert.rejects(owner.sources(), error => error === denied);
  assert.equal(owner.life.current(), false);
  assert.equal(owner.failure.getSnapshot(), denied);
  assert.deepEqual(owner.store.resident(), {channelPages: 0, tiles: 0, programs: 0, inFlight: 0, queued: 0});
  owner.store.setViewport({firstRow: 0, lastRow: 12, start: 0, end: 60_000});await tick();
  await assert.rejects(owner.sources(), {name: 'AbortError'});
  assert.equal(reads, 1);
  owner.dispose();assert.equal(owner.failure.getSnapshot(), undefined);
});

test('Refresh forgets what the guide and On now read and reads their shown rows again; an aborted refresh reads nothing', async () => {
  const calls: URL[] = [];let name = 'Before';
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request(path: string) {
      const url = new URL(path, 'https://example.test');calls.push(url);
      const channel = {id: 'channel', name, number: '1', kind: 'live', favorite: false, hidden: false};
      if (url.pathname === '/v1/live/now') return {items: [{channel}]};
      if (url.pathname === '/v1/live/guide') return {items: [{channel, programmes: []}], outOfDate: false};
      return {items: [channel]};
    }}}) as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'all', sourceId: '', timezone: 'UTC'}), shelf = owner.onNow();
  try {
    owner.store.setViewport({firstRow: 0, lastRow: 0, start: 0, end: 1});
    shelf.store.setViewport({firstRow: 0, lastRow: 0, start: 0, end: 1});
    for (let i = 0; i < 20; i++) await tick();
    assert.equal(owner.store.channelAt(0)?.name, 'Before');assert.equal(shelf.store.channelAt(0)?.name, 'Before');
    name = 'After';await owner.refresh();
    for (let i = 0; i < 20; i++) await tick();
    assert.equal(owner.store.channelAt(0)?.name, 'After');assert.equal(shelf.store.channelAt(0)?.name, 'After');
    const before = calls.length, request = new AbortController();request.abort();
    await assert.rejects(owner.refresh(request.signal), {name: 'AbortError'});assert.equal(calls.length, before);
  } finally {shelf.dispose();owner.dispose();}
});
