import assert from 'node:assert/strict';
import test from 'node:test';
import {farewellPlaybackSession, readPlaybackCapabilities, readPlaybackItem, readPlaybackGuide, readPlaybackSubtitleFonts, readPlaybackSubtitleLines} from '../src/query/playback.ts';

test('playback operations preserve request arguments and never retry or change the supplied document capability', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const response = {id: 'item'}, api = {async request<T>(...args: unknown[]): Promise<T> {calls.push(args);return response as T;}};
  assert.equal(await readPlaybackCapabilities(api, signal), response);
  assert.equal(await readPlaybackItem(api, 'item/a', signal), response);
  await readPlaybackGuide(api, new URLSearchParams({channels: 'channel', cursor: '', limit: '1'}), signal);
  await readPlaybackSubtitleFonts(api, '/v1/subtitles/fonts?grant=original', signal);
  await readPlaybackSubtitleLines(api, '/v1/subtitles/lines?grant=original', signal);
  assert.deepEqual(calls, [
    ['/v1/capabilities', 'GET', undefined, signal],
    ['/v1/items/item%2Fa', 'GET', undefined, signal],
    ['/v1/guide?channels=channel&cursor=&limit=1', 'GET', undefined, signal],
    ['/v1/subtitles/fonts?grant=original', 'GET', undefined, signal],
    ['/v1/subtitles/lines?grant=original', 'GET', undefined, signal],
  ]);
});

test('page farewell remains synchronous with its original generation and optional rounded position', () => {
  const calls: unknown[][] = [], farewell = (...args: unknown[]) => {calls.push(args);};
  assert.equal(farewellPlaybackSession(farewell, 'session/a', 7), undefined);
  farewellPlaybackSession(farewell, 'session', 8, -12);
  farewellPlaybackSession(farewell, 'session', 8, 1200.6);
  assert.deepEqual(calls, [
    ['/v1/playback/sessions/session%2Fa', 'DELETE', {generation: 7}],
    ['/v1/playback/sessions/session', 'DELETE', {generation: 8, positionMs: 0}],
    ['/v1/playback/sessions/session', 'DELETE', {generation: 8, positionMs: 1201}],
  ]);
});
