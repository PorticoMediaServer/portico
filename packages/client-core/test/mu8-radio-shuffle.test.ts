/** MU-8: a radio queue orders itself and the server refuses Shuffle on it, so the queue snapshot
 * says so (`radio`) and no player draws a Shuffle control for it. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import type {V1Http} from '../src/playback-v1/http.ts';

async function play(radio: boolean) {
  const server = new FakePlaybackServer({itemKind: () => 'song'}), inner = fakeHttp(server);
  const asRadio = (body: any) => {
    const queue = body?.queue ?? (body && Array.isArray(body.segments) ? body : undefined);
    if (queue?.segments) queue.segments = queue.segments.map((segment: any) => ({...segment, kind: 'radio'}));
    return body;
  };
  const http: V1Http = {send: async request => {const response = await inner.send(request as never) as any; return radio ? {...response, body: asRadio(response.body)} : response;}};
  let serial = 0;
  const api = new V1PlaybackApi(http, {key: () => `api-${++serial}`});
  const playback = new PlaybackService(api, () => `run-${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}, key: () => `key-${++serial}`});
  const stop = player.connect();
  await player.playItem('song-1');
  return {player, stop};
}

test('MU-8: a radio queue is marked radio; any other queue is not', async () => {
  for (const radio of [true, false]) {
    const {player, stop} = await play(radio);
    try {
      assert.equal(player.getSnapshot().view?.queue.radio, radio ? true : undefined);
    } finally {stop(); player.dispose();}
  }
});
