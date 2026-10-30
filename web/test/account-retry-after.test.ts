import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '@core/index.ts';
import {hostedGate} from '@core/hosted-gate.ts';
import {BrowserAccountApi} from '../src/platform/account.ts';

test('a Portico Account failure keeps the service’s Retry-After, so automatic work waits at least that long', async t => {
  const origin = 'https://account-retry-after.example';
  const api = new BrowserAccountApi(origin);
  t.mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify({error: {code: 'unavailable', message: 'Try again shortly.', retryable: true}}), {status: 503, headers: {'Content-Type': 'application/json', 'Retry-After': '300'}}));
  await assert.rejects(api.send('/v1/servers', 'GET'), (e: unknown) => e instanceof ApiError && e.status === 503 && e.retryAfterSeconds === 300);
  // The same failure through the shared circuit, as routes, list and renewal calls reach it.
  await assert.rejects(hostedGate(origin).run('interactive', () => api.send('/v1/servers', 'GET')));
  assert.ok(hostedGate(origin).blockedFor() >= 300_000, 'Retry-After is a floor');
});

test('a Portico Account failure without Retry-After carries none', async t => {
  const api = new BrowserAccountApi('https://account-no-retry-after.example');
  t.mock.method(globalThis, 'fetch', async () => new Response('{}', {status: 500, headers: {'Content-Type': 'application/json'}}));
  await assert.rejects(api.send('/v1/servers', 'GET'), (e: unknown) => e instanceof ApiError && e.retryAfterSeconds === undefined);
});
