import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeEventStream} from '../src/event-feed.ts';

test('the phone and TV feed opens /v1/events as a stream with its place and the token in use at each connect', async () => {
  const calls: Array<[string, Record<string, string>]> = [];
  let token = 'first';
  const open = nativeEventStream(async (path, headers) => {
    calls.push([path, headers]);
    return new Response(': connected\n\n', {status: 503});
  }, () => token);
  for (const [current, place] of [['first', ''], ['renewed', 'opaque-cursor']] as const) {
    token = current;
    await assert.rejects(open(place, () => {}, new AbortController().signal));
    const expected: Record<string, string> = {Accept: 'text/event-stream', Authorization: 'Bearer ' + current};
    if (place) expected['Last-Event-ID'] = place;
    assert.deepEqual(calls.at(-1), ['/v1/events', expected]);
  }
});
