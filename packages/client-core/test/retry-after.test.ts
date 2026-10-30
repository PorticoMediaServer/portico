import {test} from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, ServerListWatcher, hostedGate, retryAfterSecondsFrom} from '../src/index.ts';
import {PlaybackApiError, call, type V1Http} from '../src/playback-v1/index.ts';

test('Retry-After accepts delay-seconds', () => {
  assert.equal(retryAfterSecondsFrom('30'), 30);
  assert.equal(retryAfterSecondsFrom('0'), undefined);
  assert.equal(retryAfterSecondsFrom(null), undefined);
  assert.equal(retryAfterSecondsFrom('soon'), undefined);
});

test('Retry-After accepts an HTTP-date', () => {
  const now = Date.parse('Wed, 24 Sep 2026 01:00:00 GMT');
  assert.equal(retryAfterSecondsFrom('Wed, 24 Sep 2026 01:00:45 GMT', now), 45);
  assert.equal(retryAfterSecondsFrom('Wed, 24 Sep 2026 00:59:00 GMT', now), undefined);
});

test('a playback request reads Retry-After as seconds or as an HTTP-date', async t => {
  const now = Date.parse('Wed, 24 Sep 2026 01:00:00 GMT');
  t.mock.method(Date, 'now', () => now);
  const answering = (value: string): V1Http => ({send: async () => ({status: 503, headers: {'Retry-After': value}, body: {error: {code: 'unavailable'}}})});
  const waitFor = (value: string) => call(answering(value), {method: 'GET', path: '/v1/playback/sessions/s1'}).then(() => undefined, (e: unknown) => (e as PlaybackApiError).retryAfterMs);
  assert.equal(await waitFor('30'), 30_000);
  assert.equal(await waitFor(' 30 '), 30_000);
  assert.equal(await waitFor('Wed, 24 Sep 2026 01:00:45 GMT'), 45_000, 'a proxy’s date is a floor too');
  assert.equal(await waitFor('Wed, 24 Sep 2026 00:59:00 GMT'), undefined, 'a date in the past asks for nothing');
});

test('a server-list check keeps an HTTP-date Retry-After as the floor for automatic work', async () => {
  const origin = 'https://hosted-retry-date.example';
  const watcher = new ServerListWatcher({hostedOrigin: origin, listServers: async () => ({items: [], version: '1', watch: 'd2F0Y2g.mac'}), onServers: () => {},
    fetcher: async () => new Response('{}', {status: 503, headers: {'Retry-After': new Date(Date.now() + 600_000).toUTCString()}})});
  watcher.adopt({items: [], version: '1', watch: 'd2F0Y2g.mac'});
  await assert.rejects(watcher.refresh(), (e: unknown) => e instanceof ApiError && e.status === 503 && (e.retryAfterSeconds ?? 0) > 590);
  assert.ok(hostedGate(origin).blockedFor() > 590_000, 'the date is not replaced by the shorter local backoff');
});
