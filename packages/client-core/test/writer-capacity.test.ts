import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, WRITER_RETRY_BUDGET_MS, retryWriterCapacity, writerRetryDelayMs} from '../src/index.ts';
import {call, PlaybackApiError, type V1Request, type V1Response} from '../src/playback-v1/http.ts';
import {presentError} from '../src/presentation/index.ts';

const refusal = (seconds: string) => new Response(JSON.stringify({error: {code: 'writer_capacity', message: 'The server is busy saving other changes. Try again shortly.', retry: 'same_request', retryAfterMs: Number(seconds) * 1000}}), {status: 503, headers: {'Content-Type': 'application/json', 'Retry-After': seconds}});

/** A server that answers from a script, recording each request body. */
function scripted(answers: (() => Response)[]) {
  const bodies: unknown[] = [];
  const fetcher = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    bodies.push(init?.body === undefined ? undefined : JSON.parse(String(init.body)));
    const next = answers.shift();
    if (!next) throw new Error('unexpected request');
    return next();
  }) as typeof fetch;
  return {fetcher, bodies};
}

async function flush() { for (let i = 0; i < 20; i++) await Promise.resolve(); }

test('a change the writer refused is sent again after Retry-After, not dropped', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = scripted([() => refusal('3'), () => refusal('1'), () => new Response(JSON.stringify({saved: true}), {status: 200, headers: {'Content-Type': 'application/json'}})]);
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.request<{saved: boolean}>('/v1/me/saved', 'POST', {itemId: 'a'});
  await flush();
  assert.equal(server.bodies.length, 1);
  t.mock.timers.tick(2_999);
  await flush();
  assert.equal(server.bodies.length, 1, 'waits the whole Retry-After');
  t.mock.timers.tick(1);
  await flush();
  assert.equal(server.bodies.length, 2);
  t.mock.timers.tick(1_000);
  await flush();
  assert.deepEqual(await pending, {saved: true});
  assert.deepEqual(server.bodies, [{itemId: 'a'}, {itemId: 'a'}, {itemId: 'a'}], 'the same change each time');
});

test('a writer refusal past the retry budget is reported as a retryable busy failure', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const server = scripted([() => refusal('30'), () => refusal('30'), () => refusal('30')]);
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.request('/v1/me/saved', 'POST', {itemId: 'a'}).then(() => 'answered', error => error);
  for (let waited = 0; waited <= WRITER_RETRY_BUDGET_MS; waited += 30_000) { await flush(); t.mock.timers.tick(30_000); }
  await flush();
  const error = await pending;
  assert.ok(error instanceof ApiError);
  assert.equal(error.code, 'writer_capacity');
  assert.equal(error.retryable, true);
  assert.equal(error.retryAfterSeconds, 30);
  assert.equal(server.bodies.length, 3, 'two waits of 30 s fill the 60 s budget');
  assert.equal(presentError(error).category, 'busy');
});

test('cancelling a request ends its writer wait at once', async () => {
  const server = scripted([() => refusal('30')]);
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const controller = new AbortController();
  const pending = api.request('/v1/me/saved', 'POST', {itemId: 'a'}, controller.signal).then(() => 'answered', error => error);
  await flush();
  controller.abort();
  const error = await pending;
  assert.equal((error as Error).name, 'AbortError');
  assert.equal(server.bodies.length, 1);
});

test('other failures are not retried', async () => {
  for (const answer of [
    () => new Response(JSON.stringify({error: {code: 'server_busy', retryable: true}}), {status: 503, headers: {'Retry-After': '1'}}),
    () => new Response(JSON.stringify({error: {code: 'storage_full', retry: 'same_request'}}), {status: 507}),
    () => new Response(JSON.stringify({error: {code: 'invalid_request', retry: 'never'}}), {status: 400}),
  ]) {
    const server = scripted([answer]);
    const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
    await assert.rejects(api.request('/v1/me/saved', 'POST', {}), ApiError);
    assert.equal(server.bodies.length, 1);
  }
});

test('playback v1 resends a refused change with the same Idempotency-Key', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const sent: V1Request[] = [];
  const answers: V1Response[] = [
    {status: 503, headers: {'Retry-After': '2'}, body: {error: {code: 'writer_capacity', retry: 'same_request'}}},
    {status: 204, headers: {}},
  ];
  const http = {send: async (request: V1Request) => { sent.push(request); return answers.shift()!; }};
  const pending = call(http, {method: 'POST', path: '/v1/sessions/s/stop', headers: {'Idempotency-Key': 'k'.repeat(16)}});
  await flush();
  assert.equal(sent.length, 1);
  t.mock.timers.tick(2_000);
  await flush();
  assert.equal((await pending).status, 204);
  assert.equal(sent.length, 2);
  assert.equal(sent[0], sent[1], 'the identical request, key included');
});

test('the wait is held to the 1-30 s the server may ask for', () => {
  assert.equal(writerRetryDelayMs(undefined), 1_000);
  assert.equal(writerRetryDelayMs(0), 1_000);
  assert.equal(writerRetryDelayMs(4_000), 4_000);
  assert.equal(writerRetryDelayMs(600_000), 30_000);
});

test('a v1 refusal past the budget surfaces as a PlaybackApiError', async () => {
  const waits: number[] = [];
  const error = await retryWriterCapacity(
    async () => { throw new PlaybackApiError(503, 'writer_capacity', 'same_request', undefined, 30_000); },
    e => e instanceof PlaybackApiError && e.code === 'writer_capacity' ? {retryAfterMs: e.retryAfterMs} : undefined,
    undefined,
    async ms => { waits.push(ms); },
  ).then(() => undefined, e => e);
  assert.ok(error instanceof PlaybackApiError);
  assert.deepEqual(waits, [30_000, 30_000]);
});

test('a read refused behind a WAL reset is resent after its Retry-After', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const busy = () => new Response(JSON.stringify({error: {code: 'database_busy', retry: 'same_request', retryAfterMs: 1000}}), {status: 503, headers: {'Content-Type': 'application/json', 'Retry-After': '1'}});
  const server = scripted([busy, () => new Response(JSON.stringify({ok: true}), {status: 200, headers: {'Content-Type': 'application/json'}})]);
  const api = new HttpLocalApi('http://192.168.1.20:32500', '', server.fetcher);
  const pending = api.request<{ok: boolean}>('/v1/libraries');
  await flush();
  t.mock.timers.tick(1_000);
  await flush();
  assert.deepEqual(await pending, {ok: true});
  assert.equal(server.bodies.length, 2);
});
