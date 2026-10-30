import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {trickplayFrameAt} from '@core/trickplay.ts';
import {readPlaybackTrickplay, type PlaybackTrickplaySource} from '../src/platform/playback/trickplay.ts';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';

const authority = Object.freeze({serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', epoch: 1, viewerFence: 'home-viewer-fence'});
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
const target = {libraryId: 'library', itemId: 'item'}, setId = 'a'.repeat(64), revision = 'b'.repeat(64);
function rig() {
  let live = true;
  const controller = new AbortController();
  let snapshot: ReturnType<PlaybackTrickplaySource['getSnapshot']> = {authority, disposed: false, fence, offers: {
    scope: {serverId: 'server', libraryId: 'library', itemId: 'item', viewerFence: 'detail-viewer-fence'},
    current: {sessionId: 'session', generation: 1, sourceId: 'source', state: 'playing', delivery: 'hls', audioSelection: 'server_first_audio', subtitleSelection: 'off', qualityId: 'original'},
  }};
  const analysis = {
    scope: {...snapshot.offers!.scope, sessionId: 'session', sessionGeneration: 1},
    source: {id: 'source', revision: 'inventory-revision', mappingRevision: 'c'.repeat(64), durationUS: '60000000', needsProbe: false},
    sources: [{id: 'source', label: 'Source 1'}], canManage: false, markerRevision: 1,
    policy: {tier: 'complete', revision: 1, operations: [], trickplay: {intervalMs: 10000, tileWidth: 320, maxTiles: 100}},
    results: [], markers: [], previews: [], jobs: [], budgets: null,
  };
  const descriptor = {scope: {...snapshot.offers!.scope, sessionId: '', sessionGeneration: 0}, sets: [{
    id: setId, sourceId: 'source', sourceRevision: 'inventory-revision', revision,
    width: 640, height: 360, columns: 2, rows: 2, tileWidth: 320, tileHeight: 180,
    intervalMs: 10000, durationMs: 60000, sourceOffsetMs: 20000, frameCount: 8, tileCount: 2, stale: false,
    tilesUrl: '/v1/items/item/trickplay/' + setId + '/tiles', thumbnailsUrl: '/v1/items/item/trickplay/' + setId + '/thumbnails.vtt',
  }]};
  const requests: URL[] = [];
  let onRead = async (_url: URL) => {};
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); requests.push(url); await onRead(url);
    return new Response(JSON.stringify(url.pathname.endsWith('/analysis') ? analysis : descriptor), {headers: {'Content-Type': 'application/json'}});
  });
  const source: PlaybackTrickplaySource = {api, authority, contentScope: {serverId: 'server', viewerId: 'opaque-viewer'}, isCurrent: () => live, getSnapshot: () => snapshot};
  return {source, controller, analysis, descriptor, requests, read: () => readPlaybackTrickplay(source, fence, target, controller.signal),
    replace: (patch: Partial<typeof snapshot>) => { snapshot = {...snapshot, ...patch}; }, snapshot: () => snapshot,
    retire: () => { live = false; }, onRead: (callback: typeof onRead) => { onRead = callback; }};
}

test('real authenticated analysis and descriptor reads bind an independent inventory revision and canonical sprite geometry', async () => {
  const t = rig(), selected = await t.read();
  assert.ok(selected); assert.equal(selected.isCurrent(), true);
  assert.equal(selected.sourceRevision, 'inventory-revision'); assert.equal(selected.viewerFence, 'detail-viewer-fence');
  assert.equal(t.requests.length, 2);
  assert.equal(t.requests[0]!.pathname, '/v1/items/item/analysis');
  assert.deepEqual(Object.fromEntries(t.requests[0]!.searchParams), {sourceId: 'source', sessionId: 'session', generation: '1'});
  assert.equal(t.requests[1]!.pathname, '/v1/items/item/trickplay'); assert.equal(t.requests[1]!.search, '');
  const frame = trickplayFrameAt(selected.set, 5)!;
  assert.deepEqual([frame.index, frame.x, frame.y, frame.width, frame.height], [2, 0, 180, 320, 180]);
  assert.equal(frame.tileUrl, '/v1/items/item/trickplay/' + setId + '/tiles/0.jpg');
  assert.equal(Object.isFrozen(selected), true); assert.equal(Object.isFrozen(selected.set), true);
});

test('unknown independent revision never falls back to factsRevision or the set revision', async () => {
  const t = rig(); (t.analysis.source as {revision?: string}).revision = undefined;
  await assert.rejects(t.read()); assert.equal(t.requests.length, 1);
});

test('stale, foreign, differently revised or differently timed sets provide no preview', async () => {
  for (const change of [{stale: true}, {sourceId: 'other-source'}, {sourceRevision: 'old-inventory'}, {durationMs: 61000}]) {
    const t = rig(); Object.assign(t.descriptor.sets[0]!, change); assert.equal(await t.read(), null);
  }
  const t = rig(); t.analysis.source.durationUS = '60000499';
  assert.ok(await t.read(), 'the descriptor uses the producer’s explicit rounded integer-millisecond boundary');
});

test('analysis and descriptor viewer fences must match the actual captured offers, without coercing the Home fence', async () => {
  for (const stage of ['analysis', 'descriptor'] as const) {
    const t = rig(); t[stage].scope.viewerFence = 'another-viewer';
    assert.equal(await t.read(), null); assert.equal(t.requests.length, stage === 'analysis' ? 1 : 2);
  }
  const t = rig(); t.analysis.source.needsProbe = true;
  assert.equal(await t.read(), null); assert.equal(t.requests.length, 1);
  const prepared = rig(); prepared.replace({offers: {...prepared.snapshot().offers!, current: {...prepared.snapshot().offers!.current!, preparedVersionId: 'prepared-version'}}});
  assert.equal(await prepared.read(), null); assert.equal(prepared.requests.length, 0, 'a prepared derivative has no proven original-source preview clock');
});

test('source, occurrence, generation, authority and abort changes fence reads and every later image interest', async () => {
  const changes: ((t: ReturnType<typeof rig>) => void)[] = [
    t => t.replace({fence: {...fence, sourceId: 'another-source'}}), t => t.replace({fence: {...fence, entryId: 'another-entry'}}),
    t => t.replace({fence: {...fence, generation: 2}}), t => t.replace({authority: {...authority, epoch: 2}}),
    t => t.replace({disposed: true}), t => t.retire(), t => t.controller.abort(),
  ];
  for (const change of changes) {
    const t = rig(), selected = await t.read(); assert.ok(selected);
    change(t); assert.equal(selected.isCurrent(), false); const before = t.requests.length;
    assert.equal(await t.read(), null); assert.equal(t.requests.length, before);
  }
  for (const stage of ['analysis', 'trickplay']) {
    const t = rig(); t.onRead(async url => { if (url.pathname.endsWith('/' + stage)) t.replace({fence: {...fence, generation: 2}}); });
    assert.equal(await t.read(), null); assert.equal(t.requests.length, stage === 'analysis' ? 1 : 2);
  }
  const t = rig(), selected = await t.read(); assert.ok(selected);
  t.replace({offers: {...t.snapshot().offers!, scope: {...t.snapshot().offers!.scope, viewerFence: 'changed'}}});
  assert.equal(selected.isCurrent(), false);
  assert.equal(await t.read(), null);
  assert.equal(t.requests.length, 3, 'a fresh read captures the new offer fence; the old-fence analysis cannot authorize a descriptor request');
});
