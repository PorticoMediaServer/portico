import test from 'node:test';
import assert from 'node:assert/strict';
import type {PlaybackInfo} from '../../../packages/client-core/src/playback-v1/playback-info.ts';
import {committedInfo} from './helpers/playback-info.ts';
import {reconciledPlaybackLosses} from '../../../packages/client-core/src/playback-info-projection.ts';
import {nativeWatchLossNotice} from '../src-next/screens/watch/playback-info.ts';

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
