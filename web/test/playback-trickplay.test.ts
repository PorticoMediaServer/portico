import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {trickplayFrameAt} from '@core/trickplay.ts';
import {readPlaybackTrickplay, type PlaybackTrickplaySource} from '../src/platform/playback/trickplay.ts';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';

const authority = Object.freeze({serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', epoch: 1, viewerFence: 'home-viewer-fence'});
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
const target = {libraryId: 'library', itemId: 'item'};
function rig() {
  let live = true;
  const controller = new AbortController();
  let snapshot: ReturnType<PlaybackTrickplaySource['getSnapshot']> = {authority, disposed: false, fence, offers: {
    scope: {serverId: 'server', libraryId: 'library', itemId: 'item', viewerFence: 'detail-viewer-fence'},
    current: {sessionId: 'session', generation: 1, sourceId: 'source', state: 'playing', delivery: 'hls', audioSelection: 'server_first_audio', subtitleSelection: 'off', qualityId: 'original'},
  }};
  const descriptor = {version: 'source', available: true, durationMs: 60000, intervalMs: 10000, frameWidth: 320, frameHeight: 180, columns: 10, frameCount: 6,
    sheets: ['/v1/items/item/scrub-previews?version=source&sheet=0']};
  const requests: URL[] = [];
  let onRead = async (_url: URL) => {};
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); requests.push(url); await onRead(url);
    return new Response(JSON.stringify(descriptor), {headers: {'Content-Type': 'application/json'}});
  });
  const source: PlaybackTrickplaySource = {api, authority, contentScope: {serverId: 'server', viewerId: 'opaque-viewer'}, isCurrent: () => live, getSnapshot: () => snapshot};
  return {source, controller, descriptor, requests, read: () => readPlaybackTrickplay(source, fence, target, controller.signal),
    replace: (patch: Partial<typeof snapshot>) => { snapshot = {...snapshot, ...patch}; }, snapshot: () => snapshot,
    retire: () => { live = false; }, onRead: (callback: typeof onRead) => { onRead = callback; }};
}

test('the playing version\'s scrub previews are read once, with the viewer\'s sign-in, and map to its sheet', async () => {
  const t = rig(), selected = await t.read();
  assert.ok(selected); assert.equal(selected.isCurrent(), true);
  assert.equal(t.requests.length, 1);
  assert.equal(t.requests[0]!.pathname, '/v1/items/item/scrub-previews'); assert.equal(t.requests[0]!.search, '?version=source');
  const frame = trickplayFrameAt(selected.set, 25)!;
  assert.deepEqual([frame.index, frame.x, frame.y, frame.width, frame.height], [2, 640, 0, 320, 180]);
  assert.equal(frame.tileUrl, '/v1/items/item/scrub-previews?version=source&sheet=0');
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
  {
    const t = rig(); t.onRead(async () => { t.replace({fence: {...fence, generation: 2}}); });
    assert.equal(await t.read(), null, 'a playback that moved on during the read draws nothing'); assert.equal(t.requests.length, 1);
  }
  const t = rig(), selected = await t.read(); assert.ok(selected);
  t.replace({offers: {...t.snapshot().offers!, scope: {...t.snapshot().offers!.scope, viewerFence: 'newer'}}});
  assert.equal(selected.isCurrent(), true, 'a moved offers fence is a newer answer for the same session, source and generation');
  assert.ok(await t.read(), 'a fresh read still selects the set');
});
