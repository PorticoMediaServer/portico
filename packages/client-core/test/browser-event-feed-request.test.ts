import test from 'node:test';
import assert from 'node:assert/strict';
import {openBrowserEventFeed} from '../src/event-feed.ts';

test('browser stream request preserves resume headers, current route/bearer and the caller signal without adding request options', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const response = new Response('stream');
  const answer = Promise.resolve(response);
  const api = {baseUrl: 'https://first.example', routeFetch: (...args: unknown[]) => {calls.push(args); return answer;}};
  let bearer = 'first';
  const headers = {Accept: 'text/event-stream', 'Last-Event-ID': 'opaque-cursor', Authorization: 'old'};
  for (const origin of ['https://first.example', 'https://verified-route.example']) {
    api.baseUrl = origin; bearer = origin;
    const pending = openBrowserEventFeed(api, () => bearer, headers, signal);
    assert.equal(await pending, response);
    assert.deepEqual(calls.at(-1), [origin + '/v1/events', {
      signal, cache: 'no-store', headers: {Accept: 'text/event-stream', 'Last-Event-ID': 'opaque-cursor', Authorization: 'Bearer ' + origin},
    }]);
  }
  assert.equal(headers.Authorization, 'old', 'the shared caller headers stay untouched');
});
