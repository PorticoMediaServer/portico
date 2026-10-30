import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, type DockPlayback, type PlaybackFence, type QueuePlaybackFence, type QueueScope, type RemoteDock} from '../../../packages/player-core/src/index.ts';
import {playbackIdentity} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {sameDockFence, commandAllowed, commandIsCurrent, currentOccupant, dockCommand, dockSourceIdentity, progressOf, releaseAside, releaseDock, remoteSubtitle, seekTo} from '../src-next/kit/dock/model.ts';
import {currentMorphFence} from '../src-next/kit/dock/morph-fence.ts';
import type {DockPresentation} from '../src-next/kit/dock/types.ts';

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const video: DockPlayback = {fence, kind: 'video', queue: null, positionSeconds: 75, playing: true, expanded: false};
const presentation = (over: Partial<DockPresentation> = {}): DockPresentation => ({fence, identity: playbackIdentity({kind: 'video', item: {id: 'item', kind: 'episode', title: 'Episode', show: {title: 'Show'}, episode: {mode: 'seasonal', season: 1, episode: 1}}}, 'dock', {t: defaultI18n.t}), durationSeconds: 100, skipBackSeconds: 10, capabilities: {play: true, pause: true, seek: true, stop: true, previous: false, next: true, cast: false}, ...over});
const remote = (over: Partial<RemoteDock> = {}): RemoteDock => ({id: 'receiver', kind: 'cast', sequence: '1', phase: 'playing', playback: video, positionSeconds: 40, ...over});
function setup(model = presentation(), playback: DockPlayback = video) {
  const dock = new PlaybackDock(scope);
  dock.start(playback);
  const get = () => currentOccupant(dock, dock.getSnapshot(), {local: model, remote: model})!;
  return {dock, get};
}

test('dock text identity follows local plays and actual queue/channel sources', () => {
  assert.equal(dockSourceIdentity(fence), 'source');
  const nextQueueFence = {...fence, itemId: 'next-item', entryId: 'next-entry', generation: 2};
  assert.equal(dockSourceIdentity(nextQueueFence), 'source');
  assert.equal(dockSourceIdentity({kind: 'channel', sourceId: 'live-source'}), 'live-source');
  assert.equal(dockSourceIdentity({kind: 'channel'}), undefined, 'a library channel without a separate source stays source-less');
  assert.equal(dockSourceIdentity({kind: 'local', playId: 'local-play'}), 'local-play');
  assert.notEqual(dockSourceIdentity({kind: 'local', playId: 'next-local-play'}), dockSourceIdentity({kind: 'local', playId: 'local-play'}));
});

test('presentation is bound to exact source and snapshot; a former render cannot paint a replacement', () => {
  const {dock} = setup(), old = dock.getSnapshot();
  assert.ok(currentOccupant(dock, old, {local: presentation()}));
  dock.update({...video, positionSeconds: 80});
  assert.equal(currentOccupant(dock, old, {local: presentation()}), null);
  assert.equal(currentOccupant(dock, dock.getSnapshot(), {local: presentation({fence: {...fence, generation: 2}})}), null);
  dock.expand(fence);
  assert.equal(currentOccupant(dock, dock.getSnapshot(), {local: presentation()}), null);
});

test('remote metadata and receiver position win, even while local video remains playing during connection', () => {
  const {dock, get} = setup();
  dock.beginRemote(remote({kind: 'play-on', phase: 'connecting'}), 0);
  const occupant = get();
  assert.equal(occupant.mode, 'play-on');
  assert.equal(dock.getSnapshot().local?.playing, true);
  assert.equal(progressOf(occupant), .4);
  assert.equal(remoteSubtitle(occupant)[0].text, defaultI18n.t('playOn.connectingTo', {device: defaultI18n.t('cast.theTV')}));
});

test('actions captured before a new source or receiver sequence cannot reach its transport', () => {
  const {dock, get} = setup();
  const pause = dockCommand(get(), 'pause');
  assert.equal(commandIsCurrent(dock, pause), true);
  dock.start({...video, fence: {...fence, generation: 2}});
  assert.equal(commandIsCurrent(dock, pause), false);
  dock.beginRemote(remote(), 0);
  const remotePause = dockCommand(get(), 'pause');
  dock.remote(remote({sequence: '2'}), 1);
  assert.equal(commandIsCurrent(dock, remotePause), false);
  assert.equal(commandIsCurrent(dock, pause), false);
});

test('live progress and Back use actual rewind bounds, including tune-in and the live edge', () => {
  const liveFence: PlaybackFence = {kind: 'channel', playback: fence.playback, sessionId: 'live-session', sourceId: 'live-source', channelKind: 'live-source', channelId: 'channel', channelGeneration: 'buffer-1', generation: 1};
  const live: DockPlayback = {...video, kind: 'live', fence: liveFence, queue: null};
  const {get} = setup(presentation({fence: liveFence, seekWindow: {start: 50, end: 90}}), live);
  const occupant = get();
  assert.equal(progressOf(occupant), .625);
  assert.equal(seekTo(occupant, 0)?.positionSeconds, 50);
  assert.equal(seekTo(occupant, 100)?.positionSeconds, 90);
  assert.equal(seekTo(occupant, NaN), undefined);
  const unknown = setup(presentation({durationSeconds: undefined})).get();
  assert.equal(progressOf(unknown), undefined);
  assert.equal(seekTo(unknown, 0), undefined);
});

test('disabled and remote lifecycle controls follow genuine capabilities', () => {
  const {dock, get} = setup();
  assert.equal(commandAllowed(get(), 'cast'), false);
  assert.equal(commandAllowed(get(), 'previous'), false);
  assert.equal(commandAllowed(get(), 'next'), true);
  dock.beginRemote(remote({phase: 'connecting'}), 0);
  assert.equal(commandAllowed(get(), 'play'), false);
  assert.equal(commandAllowed(get(), 'open-remote'), false);
  assert.equal(commandAllowed(get(), 'stop'), true);
  dock.remote(remote({phase: 'stopped', sequence: '2'}), 1);
  assert.equal(commandAllowed(get(), 'continue-here'), true);
  assert.equal(commandAllowed(get(), 'dismiss-remote'), true);
  assert.equal(commandAllowed(get(), 'seek'), false);
});

test('stopped, waiting, replaced and error lines retain exact catalogue messages without separators', () => {
  const expected = {waiting: defaultI18n.t('cast.waitingForAllow'), replaced: defaultI18n.t('cast.replaced'), error: defaultI18n.t('cast.error.unreachable'), stopped: defaultI18n.t('next.dock.native.stoppedAt', {position: '0:40'})};
  for (const phase of ['waiting', 'replaced', 'error', 'stopped'] as const) {
    const {dock, get} = setup();
    dock.beginRemote(remote({phase}), 0);
    assert.deepEqual(remoteSubtitle(get()).map(part => part.text), [expected[phase]]);
  }
});

test('structured episode code remains separate from the truncating name', () => {
  const model = presentation();
  assert.equal(model.identity.title, 'Show');
  assert.deepEqual(model.identity.subtitle.map(part => part.text), ['S1 E1', 'Episode']);
});

test('bookmark controls are source fenced and cannot restore across viewer reset', () => {
  const {dock} = setup();
  const audio = {...video, kind: 'music' as const, fence: {...fence, itemId: 'song', sessionId: 'audio', sourceId: 'audio-source'}};
  dock.start(audio);
  const restoreVideo = {type: 'restore-video' as const, fence};
  assert.equal(commandIsCurrent(dock, restoreVideo), true);
  dock.reset({...scope, profileId: 'other'});
  assert.equal(commandIsCurrent(dock, restoreVideo), false);
});

test('release uses position and signed velocity, including reversals and exact boundaries', () => {
  assert.equal(releaseDock(.35, 600), 0);
  assert.equal(releaseDock(.351, 0), 1);
  assert.equal(releaseDock(.1, 601), 1);
  assert.equal(releaseDock(.8, -601), 0);
  assert.equal(releaseAside(100, 800, 300), 0);
  assert.equal(releaseAside(101, 0, 300), 1);
  assert.equal(releaseAside(120, -801, 300), -1);
  assert.equal(releaseAside(-120, 801, 300), 1);
  assert.equal(releaseAside(NaN, 900, 300), 0);
  assert.equal(releaseAside(100, 900, 0), 0);
});

test('an idle morph rejects captures and queued settlements, including after scope replacement', () => {
  assert.equal(currentMorphFence(undefined, undefined), false);
  assert.equal(currentMorphFence(undefined, fence), false);
  assert.equal(currentMorphFence(fence, undefined), false);
  assert.equal(currentMorphFence({...fence}, fence), true);
  assert.equal(currentMorphFence({...fence, sourceId: 'replacement'}, fence), false);
  assert.equal(currentMorphFence({...fence, generation: fence.generation + 1}, fence), false);
  const otherProfile = {...fence, playback: {...fence.playback, scope: {...scope, profileId: 'other-profile'}}};
  assert.equal(currentMorphFence(otherProfile, fence), false);
});


test('Cast view custody is distinct from runtime fences and changes with actual attachment/controller/occurrence',()=>{
 const identity={kind:'cast-view' as const,attachmentId:'11111111-1111-4111-8111-111111111111',remote:{controller:{bindingId:'binding',controllerGeneration:2,connectionGeneration:3},occurrence:{sessionId:'remote-session',generation:4,itemId:'item'}}};
 assert.equal(sameDockFence(identity,identity),true);assert.equal(sameDockFence(identity,fence),false);
 assert.equal(sameDockFence(identity,{...identity,attachmentId:'22222222-2222-4222-8222-222222222222'}),false);
 assert.equal(sameDockFence(identity,{...identity,remote:{...identity.remote,controller:{...identity.remote.controller,controllerGeneration:3}}}),false);
 assert.equal(sameDockFence(identity,{...identity,remote:{...identity.remote,occurrence:{...identity.remote.occurrence,generation:5}}}),false);
 const {dock}=setup();assert.equal(commandIsCurrent(dock,{type:'stop',fence:identity}),false,'a view cannot authorize local runtime IO');
});
