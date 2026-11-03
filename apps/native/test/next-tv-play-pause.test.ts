import test from 'node:test';
import assert from 'node:assert/strict';
import {Platform} from 'react-native';
import {PlaybackDock, type DockPlayback, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src/index.ts';
import {playbackIdentity} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {currentOccupant, listeningPlayPauseCommand} from '../src-next/kit/dock/model.ts';
import type {DockPresentation} from '../src-next/kit/dock/types.ts';
import {admitTVPlayPause, registerTVPlayPause, resetTVPlayPauseForTest, tvPlayPauseTogglesPlayback} from '../src-next/platform/tv-play-pause.ts';

const platform = Platform as {OS: string; isTV: boolean};

test('only Apple TV toggles from the key; Android TV leaves it to its media session', () => {
  const before = {...platform};
  try {
    Object.assign(platform, {OS: 'ios', isTV: true}); assert.equal(tvPlayPauseTogglesPlayback(), true);
    Object.assign(platform, {OS: 'android', isTV: true}); assert.equal(tvPlayPauseTogglesPlayback(), false);
    Object.assign(platform, {OS: 'ios', isTV: false}); assert.equal(tvPlayPauseTogglesPlayback(), false);
  } finally {Object.assign(platform, before);}
});

test('TV focus step 6: one owner for Play/Pause — a PIN pad over the player, the player over listening, newest first', () => {
  resetTVPlayPauseForTest();
  let playerUp = true;
  const listening = registerTVPlayPause('shell'), player = registerTVPlayPause('surface', () => playerUp);
  assert.deepEqual([listening.owns(), player.owns()], [false, true], 'the full player takes it over listening');
  const pin = registerTVPlayPause('overlay');
  assert.deepEqual([listening.owns(), player.owns(), pin.owns()], [false, false, true], 'a PIN pad over the player deletes a digit instead');
  const second = registerTVPlayPause('overlay');
  assert.deepEqual([pin.owns(), second.owns()], [false, true], 'the newest overlay first');
  second.release(); pin.release();
  playerUp = false;
  assert.deepEqual([listening.owns(), player.owns()], [true, false], 'with the player down, listening playback owns it from any screen');
  listening.release(); player.release();
  assert.equal(listening.owns(), false, 'a released registration never owns');
  resetTVPlayPauseForTest();
});

test('one press reported both as a key and as a remote command toggles once; repeated presses all pass', () => {
  resetTVPlayPauseForTest();
  assert.equal(admitTVPlayPause('key', 1000), true);
  assert.equal(admitTVPlayPause('system', 1200), false, 'the same press seen by the command centre');
  assert.equal(admitTVPlayPause('key', 1300), true, 'a second real press');
  assert.equal(admitTVPlayPause('key', 1400), true);
  assert.equal(admitTVPlayPause('system', 2100), true, 'a Control Centre press long after');
  assert.equal(admitTVPlayPause('key', 2300), false, 'the reverse order is also one press');
  assert.equal(admitTVPlayPause('key', 3000), true);
  resetTVPlayPauseForTest();
});

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const model = (over: Partial<DockPresentation> = {}): DockPresentation => ({fence, identity: playbackIdentity({kind: 'video', item: {id: 'item', kind: 'movie', title: 'Song'}}, 'dock', {t: defaultI18n.t}),
  durationSeconds: 200, skipBackSeconds: 10, capabilities: {play: true, pause: true, seek: true, stop: true, previous: true, next: true, cast: false}, ...over});
const occupant = (kind: DockPlayback['kind'], playing: boolean, presentation = model()) => {
  const dock = new PlaybackDock(scope);
  dock.start({fence, kind, queue: null, positionSeconds: 20, playing, expanded: false} as DockPlayback);
  return currentOccupant(dock, dock.getSnapshot(), {local: presentation});
};

test('the remote Play/Pause pauses, resumes or replays music and audiobooks, and never drives video', () => {
  assert.equal(listeningPlayPauseCommand(occupant('music', true))?.type, 'pause');
  assert.equal(listeningPlayPauseCommand(occupant('audiobook', false))?.type, 'play');
  assert.equal(listeningPlayPauseCommand(occupant('music', false, model({finished: true})))?.type, 'replay');
  assert.deepEqual(listeningPlayPauseCommand(occupant('music', true))?.fence, fence);
  assert.equal(listeningPlayPauseCommand(occupant('video', true)), undefined, 'the video player handles its own key');
  assert.equal(listeningPlayPauseCommand(occupant('music', true, model({capabilities: {play: true, pause: false, seek: true, stop: true, previous: false, next: false, cast: false}}))), undefined);
  assert.equal(listeningPlayPauseCommand(null), undefined);
});
