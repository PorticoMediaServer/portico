import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, RequestTimeoutError, requestDeadlineMs} from '../src/index.ts';
import {presentError} from '../src/presentation/index.ts';
import {awaitWebClaim} from '../src/claim-web.ts';

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

test('claim approval waits through the ten-minute decision window and remains bounded', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = silentServer();
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.request('/v1/networking/claim/await-approval', 'POST', {expected: {}}).catch(error => error);
  await Promise.resolve();
  t.mock.timers.tick(600_000);
  assert.equal(server.seen[0]?.aborted, false, 'approval may still arrive after the ordinary one-minute write deadline');
  t.mock.timers.tick(15_001);
  assert.ok(await pending instanceof RequestTimeoutError);
  assert.equal(server.seen[0]?.aborted, true);
  assert.equal(requestDeadlineMs('/v1/networking/claim/prepare', 'POST'), 60_000);
});

test('the real claim adapter accepts approval arriving after the ordinary write deadline', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const fetcher = ((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((resolve, reject) => {
    const timer = setTimeout(() => resolve(new Response(' \n' + JSON.stringify({
      state: 'installed', identity: {serverId: 'server-1', localGeneration: '7'},
      installationAcknowledged: true, approvalRequired: false, actions: [],
    }))), 90_000);
    init?.signal?.addEventListener('abort', () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')); });
  })) as typeof fetch;
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', fetcher);
  const pending = awaitWebClaim(api, {serverId: 'server-1', publicKey: 'pubkey', fingerprint: 'fp'}, {operationId: 'op-1', localGeneration: '7', revision: '3'});
  t.mock.timers.tick(90_001);
  const status = await pending;
  assert.equal(status.state, 'installed');
  assert.equal(status.installationAcknowledged, true);
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

test('bulk metadata preview spans ordinary request deadlines and remains caller cancellable', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = silentServer();
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const life = new AbortController();
  const pending = api.request('/v1/metadata/bulk/preview', 'POST', {}, life.signal).catch(error => error);
  await Promise.resolve();
  t.mock.timers.tick(600_000);
  assert.equal(server.seen[0]?.aborted, false);
  life.abort();
  assert.equal((await pending).name, 'AbortError');
  assert.equal(server.seen[0]?.aborted, true);
  assert.equal(requestDeadlineMs('/v1/metadata/bulk/preview', 'GET'), 30_000);
  assert.equal(requestDeadlineMs('/v1/metadata/bulk/preview?extra=1', 'POST'), 60_000);
  assert.equal(requestDeadlineMs('/v1/metadata/bulk', 'POST'), 60_000);
});
