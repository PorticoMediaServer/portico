import test from 'node:test';
import assert from 'node:assert/strict';
import type {PlaybackInfo} from '../../../packages/client-core/src/playback-v1/playback-info.ts';
import {committedInfo, infoWords} from './helpers/playback-info.ts';
import {playbackFence} from '../../../packages/player-core/src/index.ts';
import {reconciledPlaybackLosses} from '../../../packages/client-core/src/playback-info-projection.ts';
import {nativeWatchLossNotice, readNativePlaybackInfo} from '../src-next/screens/watch/playback-info.ts';

// FMT-22: the notice uses the shared comparison's reconciliation, not raw losses.
const atmosCopy: PlaybackInfo = {...committedInfo, route: 'direct', routes: {picture: 'converted', sound: 'direct'},
  source: {...committedInfo.source, audio: {codec: 'eac3', channels: 8, sampleRate: 48000, objectAudio: 'atmos'}},
  delivered: {...committedInfo.delivered, audio: {codec: 'eac3', channels: 8, sampleRate: 48000}},
  losses: [{feature: 'atmos', replacement: {audioCodec: 'eac3'}}, {feature: 'hdr', replacement: {dynamicRange: 'sdr'}}]} as unknown as PlaybackInfo;

test('an observed Atmos passthrough removes the false loss and keeps the picture loss', () => {
  const reconciled = reconciledPlaybackLosses(atmosCopy, {audio: {handling: 'passthrough', codec: 'eac3'}} as never);
  assert.deepEqual(reconciled.map(loss => loss.feature), ['hdr']);
  assert.equal(nativeWatchLossNotice({losses: reconciled.filter(loss => loss.feature === 'atmos')}), undefined, 'no Atmos notice once the output proves it survived');
});

test('without an observation, or after conversion, the object loss stays', () => {
  assert.deepEqual(reconciledPlaybackLosses(atmosCopy, undefined).map(loss => loss.feature), ['atmos', 'hdr']);
  const converted = {...atmosCopy, routes: {...atmosCopy.routes, sound: 'converted'}} as PlaybackInfo;
  assert.deepEqual(reconciledPlaybackLosses(converted, {audio: {handling: 'passthrough', codec: 'eac3'}} as never).map(loss => loss.feature), ['atmos', 'hdr']);
});

test('a film in parts that cannot be joined shows the server\'s sentence, even with picture and sound notices off', async () => {
  const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 's', accountId: 'a', profileId: 'p', controllerId: 'c', controllerEpoch: 'e', commandLaneId: 'l', authority: 'local'}, run: 1},
    sessionId: 'session', itemId: 'film', sourceId: 'v1', entryId: 'entry', generation: 1});
  const notice = 'Only part 1 of 2 can play: the parts are encoded differently.';
  const playback = {getSnapshot: () => ({retired: false, fence, dock: {local: {fence}}}),
    readSession: async () => ({id: 'session', kind: 'vod', role: 'local', state: 'playing', itemId: 'film', versionId: 'v1', queue: {queueId: 'q', entryId: 'entry'},
      presentation: {generation: 1, notice}, playbackInfo: atmosCopy})};
  const info = await readNativePlaybackInfo(playback as never, fence, infoWords().words, {signal: new AbortController().signal, isCurrent: () => true});
  assert.deepEqual(info?.notice, {message: notice, lead: notice, detail: '', icon: 'picture', required: true}, 'the part sentence replaces the loss note and is not optional');
});
