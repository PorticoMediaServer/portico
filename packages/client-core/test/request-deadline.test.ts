import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, RequestTimeoutError, requestDeadlineMs} from '../src/index.ts';
import {presentError} from '../src/presentation/index.ts';

/** A server that accepts the request and never answers; it only ends when the request is aborted. */
function silentServer() {
  const seen: {url: string; aborted: boolean}[] = [];
  const fetcher = ((input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_, reject) => {
    const call = {url: String(input), aborted: false};
    seen.push(call);
    init?.signal?.addEventListener('abort', () => { call.aborted = true; const e = new Error('Aborted'); e.name = 'AbortError'; reject(e); });
  })) as typeof fetch;
  return {fetcher, seen};
}

test('a read the server never answers fails as a timeout after half a minute, not as a cancellation', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = silentServer();
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.request('/v1/libraries').then(() => 'answered', error => error);
  await Promise.resolve();
  t.mock.timers.tick(29_000);
  assert.equal(server.seen[0]?.aborted, false);
  t.mock.timers.tick(1_500);
  const error = await pending;
  assert.ok(error instanceof RequestTimeoutError);
  assert.equal(error.code, 'request_timeout');
  assert.equal(error.retryable, true);
  assert.equal(server.seen[0]?.aborted, true, 'the abandoned request is released');
  assert.notEqual(error.name, 'AbortError');
});

test('a caller who cancels gets a cancellation, never a timeout', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = silentServer();
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const controller = new AbortController();
  const pending = api.request('/v1/libraries', 'GET', undefined, controller.signal).then(() => 'answered', error => error);
  await Promise.resolve();
  controller.abort();
  const error = await pending;
  assert.equal((error as Error).name, 'AbortError');
});

test('a long poll may wait as long as it asked the server to hold it, plus a margin', () => {
  assert.equal(requestDeadlineMs('/v1/events?waitMs=25000&after=7', 'GET'), 40_000);
  assert.equal(requestDeadlineMs('/v1/events?after=7&waitMs=55000', 'GET'), 70_000);
  assert.equal(requestDeadlineMs('/v1/libraries', 'GET'), 30_000);
  assert.equal(requestDeadlineMs('/v1/items/abc/watched', 'PUT'), 60_000);
  assert.equal(requestDeadlineMs('/v1/admin/backups', 'POST'), 120_000);
});

test('the raw request used by playback and events has the same deadline', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = silentServer();
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.requestRaw('/v1/events?waitMs=25000', 'GET').then(() => 'answered', error => error);
  await Promise.resolve();
  t.mock.timers.tick(39_000);
  assert.equal(server.seen[0]?.aborted, false, 'the server is allowed its 25 seconds and a margin');
  t.mock.timers.tick(2_000);
  assert.ok(await pending instanceof RequestTimeoutError);
});

test('a timeout is presented as taking too long, with Try again', () => {
  const shown = presentError(new RequestTimeoutError());
  assert.equal(shown.category, 'timeout');
});
