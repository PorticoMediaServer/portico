import test from 'node:test';
import assert from 'node:assert/strict';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {createLiveBindings} from '../src/screens/live/bindings.ts';
import {parseLiveScreenSearch} from '../src/screens/live/route-search.ts';
import {screenEntryDestination} from '../src/screens/browse/navigation.ts';
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
test('a selected channel is one named, authorized guide read and never a guessed source lookup', async () => {
  const requests: URL[] = [];let wrongSource = false;
  const context = {scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request(path: string) {
      const url = new URL(path, 'https://example.test');requests.push(url);
      const source = {id: wrongSource ? 'other-source' : 'actual-source', name: 'Actual source', provenance: 'live-source', generation: 'generation',
        publishedAt: new Date().toISOString(), availableStart: '', availableEnd: '', refreshState: 'healthy'};
      return {protocolVersion: '1.0', serverId: 'server', guide: {viewerFence: 'actual-fence', state: 'no-guide-data',
        start: url.searchParams.get('start'), end: url.searchParams.get('end'), timezone: 'UTC', observedAt: new Date().toISOString(), nextCursor: '', sources: [source],
        channels: [{id: 'actual-channel', sourceId: source.id, provenance: 'live-source', generation: 'generation', name: 'Actual channel', number: '1', group: '',
          programmes: [], tuneAvailable: true, recordAvailable: false, favorite: false, hidden: false, preferenceRevision: 0}]}};
    }}} as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'all', sourceId: '', timezone: 'UTC'});
  try {
    const target = {sourceId: 'actual-source', channelId: 'actual-channel', provenance: 'live-source' as const};
    assert.equal((await owner.selectedChannel(target))?.sourceId, 'actual-source');
    assert.equal(requests[0].pathname, '/v1/guide');
    assert.equal(requests[0].searchParams.get('channels'), target.channelId);
    assert.equal(requests[0].searchParams.get('sourceId'), target.sourceId);
    assert.equal(requests[0].searchParams.get('limit'), '1');
    wrongSource = true;assert.equal(await owner.selectedChannel(target), undefined);
    assert.equal(requests.length, 2);
  } finally {owner.dispose();}
});
test('Live construction is inert and an authority refusal retires the bounded guide before further HTTP', async () => {
  let reads = 0;
  const denied = Object.assign(new Error('denied'), {status: 403});
  const context = {scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request() {reads++;throw denied;}}} as unknown as ScreenContext;
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

test('a source hint refreshes only bounded viewports and retires their prior directory revisions and marks', async () => {
  const calls: URL[] = [];let generation = 1;
  const context = {scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request(path: string) {
      const url = new URL(path, 'https://example.test');calls.push(url);
      const source = {id: 'source', name: 'Actual source', provenance: 'live-source', generation: String(generation),
        publishedAt: new Date(0).toISOString(), availableStart: '', availableEnd: '', refreshState: 'healthy'};
      const channel = {id: 'channel', sourceId: 'source', provenance: 'live-source', generation: String(generation), name: 'Actual channel',
        number: '1', group: '', mark: {text: generation === 1 ? 'AC' : 'A2', seed: 'source:channel'}, programmes: [], tuneAvailable: true,
        recordAvailable: false, favorite: false, hidden: false, preferenceRevision: 0};
      const base = {protocolVersion: '1.0', serverId: 'server'};
      if (url.pathname.endsWith('/sources')) return {...base, viewerFence: 'fence', revision: String(generation), sources: [
        {...source, position: 0, recordAvailable: false, guideDays: 0, channelCount: 1, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}}]};
      if (url.pathname.endsWith('/channels')) return {...base, directory: {viewerFence: 'fence', revision: String(generation), total: 1,
        offset: Number(url.searchParams.get('offset')), limit: Number(url.searchParams.get('limit')), channels: [channel], sources: [source]}};
      return {...base, guide: {viewerFence: 'fence', state: 'no-guide-data', start: url.searchParams.get('start'), end: url.searchParams.get('end'),
        timezone: 'UTC', observedAt: new Date(0).toISOString(), nextCursor: '', sources: [source], channels: [channel]}};
    }}} as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'live-source', sourceId: 'source', timezone: 'UTC'}), shelf = owner.onNow();
  try {
    owner.store.setViewport({firstRow: 0, lastRow: 0, start: 0, end: 1});
    shelf.store.setViewport({firstRow: 0, lastRow: 0, start: 0, end: 1});
    for (let i = 0; i < 20; i++) await tick();
    assert.equal(owner.store.channelAt(0)?.mark?.text, 'AC');assert.equal(shelf.store.channelAt(0)?.mark?.text, 'AC');
    generation = 2;await owner.refresh();
    for (let i = 0; i < 20; i++) await tick();
    assert.equal(owner.store.channelAt(0)?.mark?.text, 'A2');assert.equal(shelf.store.channelAt(0)?.mark?.text, 'A2');
    const directories = calls.filter(url => url.pathname.endsWith('/channels'));
    assert.equal(directories.length, 4);assert.ok(directories.every(url => url.searchParams.get('offset') === '0' && url.searchParams.get('limit') === '50' && !url.searchParams.has('revision')));
    const before = calls.length, request = new AbortController();request.abort();
    await assert.rejects(owner.refresh(request.signal), {name: 'AbortError'});assert.equal(calls.length, before);
  } finally {shelf.dispose();owner.dispose();}
});
