import test from 'node:test';
import assert from 'node:assert/strict';
import {call, PlaybackApiError, type V1Http} from '../src/playback-v1/http.ts';
import {connectionTooSlow} from '../src/playback-v1/quality.ts';

const answering = (body: unknown): V1Http => ({send: async () => ({status: 422, headers: {}, body})});

test('a too-slow refusal keeps the server reason and names the quality that fits', async () => {
  const error = await call(answering({error: {code: 'connection_too_slow', retry: 'never', reason: '720p-high'}}), {method: 'PATCH', path: '/v1/playback/sessions/s'}).catch(e => e);
  assert.ok(error instanceof PlaybackApiError);
  assert.equal(error.reason, '720p-high');
  assert.equal(connectionTooSlow(error)?.fits?.height, 720);
});

test('no fitting quality, an unknown reason or another failure', async () => {
  const none = await call(answering({error: {code: 'connection_too_slow', retry: 'never'}}), {method: 'PATCH', path: '/x'}).catch(e => e);
  assert.deepEqual(connectionTooSlow(none), {});
  assert.deepEqual(connectionTooSlow({code: 'connection_too_slow', reason: '999p'}), {});
  assert.equal(connectionTooSlow(new PlaybackApiError(409, 'session_conflict', 'after_refresh')), null);
  assert.equal(connectionTooSlow(new Error('offline')), null);
});
