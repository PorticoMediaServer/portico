import assert from 'node:assert/strict';
import test from 'node:test';
import {startFailureHttp, type HeldPlaybackStartFailure} from '../src/playback-v1/held-start.ts';
import type {V1Request, V1Response} from '../src/playback-v1/http.ts';
import type {HttpLocalApi} from '../src/index.ts';
import {routePath} from '../../contracts/src/routes.ts';

const limitRefusal = (playing = 1): V1Response => ({status: 429, headers: {}, body: {error: {code: 'stream_limit_reached', retry: 'same_request', reason: 'owner_account_cap',
  current: {scope: 'account', limit: 1, playing}, requestId: 'r'}}});
const refusal = (status: number, code: string, retry = 'never'): V1Response => ({status, headers: {}, body: {error: {code, retry, requestId: 'r'}}});
const admitted: V1Response = {status: 201, headers: {}, body: {ok: true}};
const start = (body: unknown = {itemId: 'item', versionId: 'v'}): V1Request =>
  ({method: 'POST', path: routePath('start_playback_session'), headers: {'Idempotency-Key': 'key-0000000000000001'}, body});

function fixture(responses: V1Response[], options: {page?: unknown; now?: () => number; onPublish?: (failure: HeldPlaybackStartFailure | undefined) => void} = {}) {
  const sent: V1Request[] = [], published: (HeldPlaybackStartFailure | undefined)[] = [];
  let cancelled = 0, hidden = false;
  const visible = new Set<() => void>();
  const http = {send: async (request: V1Request) => {sent.push(request); const next = responses.shift(); assert.ok(next, 'an unexpected send'); return next;}};
  const api = {request: async () => options.page ?? {items: [], nextCursor: '', streamLimit: {scope: 'account', limit: 1, playing: 0}}} as unknown as HttpLocalApi;
  const life = new AbortController();
  const held = startFailureHttp({http, api, events: {on: () => () => {}}, life: life.signal,
    visibility: {hidden: () => hidden, onVisible: listener => {visible.add(listener); return () => {visible.delete(listener);};}},
    isCurrent: () => true, isInitial: () => true, captureCurrent: () => () => true, now: options.now,
    publish: failure => {published.push(failure); options.onPublish?.(failure);}, cancelStart: () => {cancelled++;}});
  return {held, sent, published, life, get cancelled() {return cancelled;},
    hide() {hidden = true;}, show() {hidden = false; for (const listener of [...visible]) listener();}};
}
const nextFailure = async (f: ReturnType<typeof fixture>, from: number) => {
  for (let i = 0; i < 50 && f.published.length <= from; i++) await new Promise(resolve => setImmediate(resolve));
  const failure = f.published.at(-1);
  assert.ok(failure, 'a held failure is drawn');
  return failure;
};

test('an account refusal holds the same request; explicit retry waits for exhausted automation', async () => {
  let clock = 1_000;
  const f = fixture([limitRefusal(), limitRefusal(), admitted], {now: () => clock});
  const response = f.held.send(start());
  const first = await nextFailure(f, 0);
  assert.equal(first.scope, 'account'); assert.equal(first.automaticRecoveryExhausted, false);
  await assert.rejects(first.retry(), {name: 'AbortError'}, 'automation still owns an account refusal');
  // The first complete read shows a free slot, so it resumes automatically once.
  await first.remote();
  const second = await nextFailure(f, f.published.indexOf(first) + 1 + 1);
  assert.notEqual(second, first);
  clock += 30_000;
  assert.equal(second.automaticRecoveryExhausted, true, 'thirty seconds from the first recovery exhausts automation');
  await second.retry();
  assert.equal((await response).status, 201);
  assert.equal(f.sent.length, 3);
  for (const request of f.sent) {
    assert.deepEqual(request.body, {itemId: 'item', versionId: 'v'});
    assert.equal(request.headers?.['Idempotency-Key'], 'key-0000000000000001');
  }
});

test('an automatic continuation that expires while hidden keeps the original refusal and sends nothing', async () => {
  let clock = 0, suspend = false;
  const f = fixture([limitRefusal(), admitted], {now: () => clock, onPublish: failure => {
    // The automatic resume withdraws the card; the app is then suspended past the budget.
    if (!failure && suspend) {suspend = false; f.hide(); clock += 31_000;}
  }});
  const response = f.held.send(start());
  const first = await nextFailure(f, 0);
  suspend = true;
  await first.remote();
  f.show();
  const again = await nextFailure(f, f.published.length);
  assert.equal(f.sent.length, 1, 'no new send, key or budget after suspension');
  assert.equal(again.code, 'stream_limit_reached');
  assert.equal(again.automaticRecoveryExhausted, true);
  await again.retry();
  assert.equal((await response).status, 201);
  assert.equal(f.sent.length, 2);
});

test('a permanent refusal is drawn but has no retry authority', async () => {
  const f = fixture([refusal(422, 'unsupported_media')]);
  const response = f.held.send(start());
  const failure = await nextFailure(f, 0);
  assert.equal(failure.retryable, false);
  await assert.rejects(failure.retry(), {name: 'AbortError'});
  failure.cancel();
  await assert.rejects(response, {name: 'AbortError'});
  assert.equal(f.cancelled, 1);
  assert.equal(f.sent.length, 1);
});

test('revision, identity and non-replayable refusals reach the original caller unheld', async () => {
  for (const response of [refusal(412, 'revision_mismatch', 'after_refresh'), refusal(409, 'idempotency_key_reused'),
    refusal(403, 'forbidden'), refusal(409, 'source_changed', 'after_refresh'), refusal(401, 'unauthorized', 'after_reauth')]) {
    const f = fixture([response]);
    assert.equal(await f.held.send(start()), response);
    assert.equal(f.published.length, 0);
  }
});

test('a conversion refusal is held as a permanent failure', async () => {
  const f = fixture([refusal(403, 'transcode_not_allowed')]);
  void f.held.send(start()).catch(() => {});
  const failure = await nextFailure(f, 0);
  assert.equal(failure.code, 'transcode_not_allowed'); assert.equal(failure.retryable, false);
  failure.cancel();
});

test('queue creation without Play, and requests without a key, pass straight through', async () => {
  const queue: V1Request = {method: 'POST', path: routePath('create_queue'), headers: {'Idempotency-Key': 'key-0000000000000002'}, body: {items: []}};
  const f = fixture([refusal(429, 'stream_limit_reached', 'same_request'), refusal(429, 'stream_limit_reached', 'same_request')]);
  assert.equal((await f.held.send(queue)).status, 429);
  assert.equal((await f.held.send({...start(), headers: {}})).status, 429);
  assert.equal(f.published.length, 0);
});
