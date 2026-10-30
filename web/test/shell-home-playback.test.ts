import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, type DockPlayback, type QueuePlaybackFence, type QueueScope} from '../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../src/platform/playback/index.ts';
import type {DockNoticeData, DockPresentation} from '../src/kit/dock/index.ts';
import {fenceKey, hasDock, localVideoSurfaceActive, PlaybackPresentations, retainedPlaybackNotice} from '../src/shell/playback-model.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';
import {PlaybackMediaOwner} from '../src/shell/playback-media-owner.ts';

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'};
const fence = (id: string, generation = 1): QueuePlaybackFence => ({kind: 'queue', playback: {scope, run: 1}, sessionId: id, itemId: id, entryId: id, sourceId: id, generation});
const audio: DockPlayback = {fence: fence('song'), kind: 'music', positionSeconds: 107, playing: true, expanded: false, queue: null};
const video: DockPlayback = {fence: fence('movie'), kind: 'video', positionSeconds: 845, playing: true, expanded: true, queue: null};
function presentation(value: DockPlayback): DockPresentation { return {fence: value.fence, identity: {id: value.fence.kind === 'queue' ? value.fence.itemId : value.fence.channelId, kind: value.kind === 'music' ? 'music' : 'video', title: f.movie, subtitle: []}, controls: ['play', 'pause'], skipBackSeconds: 10, skipForwardSeconds: 30}; }
function snapshot(dock: PlaybackDock): WebPlaybackRuntimeSnapshot { return {dock: dock.getSnapshot()} as WebPlaybackRuntimeSnapshot; }

test('presentation keys preserve queue and channel identities independently of wire property order', () => {
  const original = fence('movie');
  const reordered: QueuePlaybackFence = {generation: original.generation, sourceId: original.sourceId, entryId: original.entryId,
    itemId: original.itemId, sessionId: original.sessionId, playback: {run: 1, scope: {...scope}}, kind: 'queue'};
  assert.equal(fenceKey(original), fenceKey(reordered));
  assert.notEqual(fenceKey(original), fenceKey({...reordered, generation: 2}));
  const channel = {kind: 'channel' as const, channelKind: 'live-source' as const, playback: original.playback,
    sourceId: original.sourceId, sessionId: original.sessionId, generation: 1, channelId: 'movie', channelGeneration: '1'};
  assert.notEqual(fenceKey(original), fenceKey(channel));
  assert.notEqual(fenceKey(channel), fenceKey({...channel, channelGeneration: '2'}));
});

test('presentation retention follows actual parked/bookmarked fences and clears when the source goes away', () => {
  const dock = new PlaybackDock(scope), values = new PlaybackPresentations();
  dock.start(audio); assert.equal(values.update(snapshot(dock), presentation(audio)).length, 1);
  dock.start(video); const parked = values.update(snapshot(dock), presentation(video));
  assert.equal(parked.length, 2); assert.ok(parked.some(value => value.fence === audio.fence));
  dock.reset(scope); assert.deepEqual(values.update(snapshot(dock), undefined), []);
  dock.start(video); values.update(snapshot(dock), presentation(video)); values.clear();
  assert.deepEqual(values.update(snapshot(dock), undefined), []);
});
test('a source generation replacement cannot reuse the old display identity', () => {
  const dock = new PlaybackDock(scope), values = new PlaybackPresentations(); dock.start(video); values.update(snapshot(dock), presentation(video));
  const replaced = {...video, fence: fence('movie', 2)}; dock.start(replaced);
  const current = values.update(snapshot(dock), presentation(replaced));
  assert.equal(current.length, 1); assert.equal(current[0].fence.generation, 2);
});
test('an authority-bound cache rejects an obsolete snapshot even with identical playback fences', () => {
  const dock = new PlaybackDock(scope); dock.start(audio); dock.start(video);
  const old = snapshot(dock); let current = old;
  const values = new PlaybackPresentations({getSnapshot: () => current});
  assert.equal(values.update(old, presentation(audio)).length, 1);
  current = snapshot(dock);
  assert.deepEqual(values.update(old, presentation(video)), []);
  assert.deepEqual(values.update(current, undefined), [], 'stale private facts were retired');
  assert.deepEqual(values.update(current, presentation(video)), [presentation(video)]);
});
test('empty playback and authority gaps cannot read or retain a nullable notice', () => {
  const dock = new PlaybackDock(scope), runtime = {};
  assert.equal(retainedPlaybackNotice(null, undefined, undefined, [presentation(audio)]), null);
  assert.equal(retainedPlaybackNotice(null, runtime, dock.getSnapshot(), [presentation(audio)]), null);
  dock.start(audio); dock.start(video);
  const value: DockNoticeData = {kind: 'paused-audio', playback: audio, presentation: presentation(audio), changedAtMs: 0};
  const notice = {runtime, value};
  assert.equal(retainedPlaybackNotice(notice, undefined, dock.getSnapshot(), [value.presentation]), null);
  assert.equal(retainedPlaybackNotice(notice, {}, dock.getSnapshot(), [value.presentation]), null);
  assert.equal(retainedPlaybackNotice(notice, runtime, dock.getSnapshot(), []), null);
  assert.equal(retainedPlaybackNotice(notice, runtime, dock.getSnapshot(), [value.presentation]), value);
  dock.reset(scope);
  assert.equal(retainedPlaybackNotice(notice, runtime, dock.getSnapshot(), [value.presentation]), null);
});
test('restoring a bookmark retires its notice even while that same playback presentation remains current', () => {
  const dock = new PlaybackDock(scope), runtime = {}, presentations = [presentation(audio), presentation(video)];
  dock.start(audio); dock.start(video);
  const audioNotice = {runtime, value: {kind: 'paused-audio', playback: audio, presentation: presentation(audio), changedAtMs: 0} satisfies DockNoticeData};
  assert.equal(retainedPlaybackNotice(audioNotice, runtime, dock.getSnapshot(), presentations), audioNotice.value);
  dock.restoreAudio();
  assert.equal(retainedPlaybackNotice(audioNotice, runtime, dock.getSnapshot(), presentations), null);
  const videoNotice = {runtime, value: {kind: 'stopped-video', playback: video, presentation: presentation(video), changedAtMs: 1} satisfies DockNoticeData};
  assert.equal(retainedPlaybackNotice(videoNotice, runtime, dock.getSnapshot(), presentations), videoNotice.value);
  dock.restoreVideo();
  assert.equal(retainedPlaybackNotice(videoNotice, runtime, dock.getSnapshot(), presentations), null);
});
test('notice retention compares canonical channel identity and rejects a replaced channel generation', () => {
  const dock = new PlaybackDock(scope), runtime = {};
  const channel: DockPlayback = {kind: 'live', fence: {kind: 'channel', channelKind: 'live-source', playback: {scope, run: 1},
    sourceId: 'source', sessionId: 'session', generation: 1, channelId: 'channel', channelGeneration: '1'}, queue: null, positionSeconds: 10, playing: true, expanded: true};
  dock.start(channel); dock.start(audio);
  const notice = {runtime, value: {kind: 'stopped-video', playback: channel, presentation: presentation(channel), changedAtMs: 0} satisfies DockNoticeData};
  assert.equal(retainedPlaybackNotice(notice, runtime, dock.getSnapshot(), [notice.value.presentation]), notice.value);
  const nextChannel: DockPlayback = {...channel, fence: {...channel.fence, channelGeneration: '2'}};
  dock.start(nextChannel); dock.start(audio);
  assert.equal(retainedPlaybackNotice(notice, runtime, dock.getSnapshot(), [notice.value.presentation, presentation(nextChannel)]), null);
});
test('spacing follows accepted dock arbitration, including remote ownership while local video is expanded', () => {
  const dock = new PlaybackDock(scope); assert.equal(hasDock(snapshot(dock)), false);
  dock.start(video); assert.equal(hasDock(snapshot(dock)), false);
  dock.collapse(video.fence); assert.equal(hasDock(snapshot(dock)), true);
  dock.expand(video.fence); dock.beginRemote({id: 'remote', kind: 'play-on', phase: 'connecting', sequence: '1', playback: video, positionSeconds: 845}, 0);
  assert.equal(hasDock(snapshot(dock)), true);
});
test('the persistent local picture survives real pending receiver phases and retires on confirmed play', () => {
  const dock = new PlaybackDock(scope);dock.start(video);
  const read = () => ({...snapshot(dock), disposed: false, presentation: {kind: 'video', fence: video.fence}} as WebPlaybackRuntimeSnapshot);
  assert.equal(localVideoSurfaceActive(read()), true);
  for (const [index, phase] of ['connecting', 'pairing', 'waiting'].entries()) {
    dock.beginRemote({id: 'remote', kind: 'play-on', phase: phase as 'connecting' | 'pairing' | 'waiting', sequence: String(index + 1), playback: video, positionSeconds: 845}, index);
    assert.equal(localVideoSurfaceActive(read()), true);
  }
  dock.remote({id: 'remote', kind: 'play-on', phase: 'playing', sequence: '4', playback: video, positionSeconds: 845}, 3);
  assert.equal(localVideoSurfaceActive(read()), false);
});
test('no absent, disposed or mismatched local source can expose the persistent picture', () => {
  const dock = new PlaybackDock(scope);dock.start(video);
  const shown = {...snapshot(dock), disposed: false, presentation: {kind: 'video', fence: video.fence}} as WebPlaybackRuntimeSnapshot;
  assert.equal(localVideoSurfaceActive(undefined), false);
  assert.equal(localVideoSurfaceActive({...shown, disposed: true}), false);
  dock.start({...video, fence: fence('replacement')});
  assert.equal(localVideoSurfaceActive({...shown, dock: dock.getSnapshot()}), false);
});
test('one media element survives scope gaps and old runtime disposal precedes every new adoption', async () => {
  const media = {}, owner = new PlaybackMediaOwner<object>(), calls: unknown[] = [];
  const runtime = (id: string) => ({mount(element: object) { calls.push(['mount', id, element]); return () => { calls.push(['dispose', id]); }; }});
  const first = runtime('first'), second = runtime('second');
  assert.equal(await owner.adopt(undefined, media), undefined);
  const oldCleanup = (await owner.adopt(first, media))!;
  await owner.adopt(first, media);
  assert.deepEqual(calls, [['mount', 'first', media]]);
  await owner.adopt(undefined, media);
  const cleanup = (await owner.adopt(second, media))!;
  oldCleanup();
  assert.deepEqual(calls, [['mount', 'first', media], ['dispose', 'first'], ['mount', 'second', media]]);
  cleanup(); cleanup();
  assert.deepEqual(calls.at(-1), ['dispose', 'second']);
  await assert.rejects(() => owner.adopt(runtime('replacement'), {}), /element-replaced/);
});
test('direct authority handover retires the previous runtime before attaching the same media element', async () => {
  const element = {}, owner = new PlaybackMediaOwner<object>(), events: string[] = [];
  await owner.adopt({mount: () => { events.push('first-mounted'); return () => { events.push('first-disposed'); }; }}, element);
  (await owner.adopt({mount: actual => { assert.equal(actual, element); events.push('second-mounted'); return () => { events.push('second-disposed'); }; }}, element))!();
  assert.deepEqual(events, ['first-mounted', 'first-disposed', 'second-mounted', 'second-disposed']);
});
