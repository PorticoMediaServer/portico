import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {parsePlaybackInfo} from '@core/playback-v1/playback-info.ts';
import {lossSentences, watchLossNotice} from '../src/screens/watch/loss-copy.ts';

const info = parsePlaybackInfo({route: 'converted', routes: {sound: 'repackaged', picture: 'converted'}, reason: {kind: 'container'},
  source: {audio: {codec: 'eac3', objectAudio: 'atmos'}}, delivered: {audio: {codec: 'eac3', delivery: 'decoded'}},
  losses: [{feature: 'atmos', replacement: {audioCodec: 'eac3'}}, {feature: 'hdr', replacement: {dynamicRange: 'sdr'}}], network: 'home', basis: 'committed_delivery_plan'})!;
const sound = parsePlaybackInfo({...info, route: 'repackaged', routes: {sound: 'repackaged'}, losses: [{feature: 'atmos', replacement: {audioCodec: 'eac3'}}]} as never)!;
const passthrough = {audio: {handling: 'passthrough' as const, codec: 'eac3'}};

test('the loss notice uses the observed reconciliation: preserved Atmos is no loss, the HDR loss stays', () => {
  assert.ok(lossSentences(defaultI18n, info, undefined, 'sound'), 'without an observation the planner\'s loss is shown');
  assert.equal(lossSentences(defaultI18n, info, passthrough, 'sound'), undefined);
  assert.ok(lossSentences(defaultI18n, info, passthrough, 'picture'));
  assert.equal(watchLossNotice(defaultI18n, info, passthrough, 'p1')?.kind, 'picture');
  assert.equal(watchLossNotice(defaultI18n, sound, passthrough, 'p1'), undefined, 'no notice when nothing was actually lost');
  assert.equal(watchLossNotice(defaultI18n, sound, undefined, 'p1')?.kind, 'sound');
});
