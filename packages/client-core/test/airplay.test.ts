import test from 'node:test';
import assert from 'node:assert/strict';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {parseSession} from '../src/playback-v1/types.ts';

for (const captions of ['off', 'text', 'drawn'] as const) test(`AirPlay receipt preserves current ${captions} and nondefault audio without changing the local session`, async () => {
  const selected = captions === 'off' ? undefined : {trackId: 'chosen', kind: captions === 'text' ? 'text' : 'ass', delivery: captions === 'text' ? 'sidecar' : 'device'};
  const session = parseSession({id: 'session', revision: '8', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie', presentation: {generation: 3, choiceRevision: 7, mode: 'direct', engine: 'native', url: '/v1/media/local', quality: {mode: 'original'}, startPositionMs: 1000, audioTrackId: 'a2', subtitle: selected, subtitles: [], decision: {}}});
  let sent: unknown;
  const receipt = {sessionId: 'session', generation: 3, choiceRevision: 7, presentation: {...session.presentation, mode: 'stream', url: '/v1/media/receiver/master.m3u8', subtitle: selected ? {...selected, delivery: captions === 'text' ? 'sidecar' : 'burn'} : undefined}};
  const client = new SessionsClient({send: async request => {sent = request;return {status: 200, headers: {}, body: receipt};}});
  const result = await client.prepareAirPlay(session, 83000);
  assert.equal(result.presentation.audioTrackId, 'a2');assert.equal(result.presentation.subtitle?.trackId, selected?.trackId);
  assert.equal(result.presentation.subtitle?.delivery, captions === 'off' ? undefined : captions === 'text' ? 'sidecar' : 'burn');
  assert.deepEqual((sent as {body: unknown}).body, {generation: 3, choiceRevision: 7, positionMs: 83000});
  assert.equal(session.presentation.mode, 'direct');assert.equal(session.presentation.subtitle?.delivery, selected?.delivery);
  for (const replacement of [{sessionId: 'retired'}, {generation: 2}, {choiceRevision: 6}, {presentation: {...receipt.presentation, audioTrackId: 'a1'}}, {presentation: {...receipt.presentation, subtitle: {trackId: 'other', kind: 'text', delivery: 'sidecar'}}}]) {
    const bad = new SessionsClient({send: async () => ({status: 200, headers: {}, body: {...receipt, ...replacement}})});
    await assert.rejects(bad.prepareAirPlay(session, 83000), /AirPlay/);
  }
  const abort = new AbortController();
  const stale = new SessionsClient({send: async () => {abort.abort();return {status: 200, headers: {}, body: receipt};}});
  await assert.rejects(stale.prepareAirPlay(session, 83000, abort.signal), {name: 'AbortError'});
});
