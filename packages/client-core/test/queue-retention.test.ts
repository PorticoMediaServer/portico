import test from 'node:test';
import assert from 'node:assert/strict';
import {QueueClient, type SegmentInput} from '../src/playback-v1/queue.ts';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import type {V1Http, V1Request} from '../src/playback-v1/http.ts';

const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const items = (...ids: string[]): readonly SegmentInput[] => [{source: {items: {ids}}}];
function fixture(options: ConstructorParameters<typeof FakePlaybackServer>[0] = {}) {
  const server = new FakePlaybackServer({viewer, ...options}), requests: V1Request[] = [], inner = fakeHttp(server);
  let key = 0;
  const http: V1Http = {send: request => { requests.push(request); return inner.send(request); }};
  return {server, http, requests, client: new QueueClient(http, () => `retention-key-${++key}-000000`)};
}

test('ordinary create and explicit empty retention still retire every previous queue with GET 404', async () => {
  const f = fixture(), a = await f.client.create(items('song-a'));
  const b = await f.client.create(items('video-b'), {retainQueueIds: [a.queue.id]});
  const c = await f.client.create(items('video-c'));
  for (const id of [a.queue.id, b.queue.id]) await assert.rejects(f.client.header(id), {status: 404});
  await f.client.create(items('video-d'), {retainQueueIds: []});
  await assert.rejects(f.client.header(c.queue.id), {status: 404});
  assert.equal(Object.hasOwn(f.requests[0]!.body as object, 'retainQueueIds'), false);
});

test('retention keeps the real duplicate occurrences, header/current/order and permits a fresh paused session and append', async () => {
  const f = fixture();
  const a = await f.client.create([{source: {items: {ids: ['song-a', 'song-b', 'song-a']}}, anchor: {position: 1}}], {startPlayback: {state: 'playing'}});
  const before = await f.client.entries(a.queue.id, 0, 10);
  const b = await f.client.create(items('video-b'), {retainQueueIds: [a.queue.id], startPlayback: {state: 'playing'}});
  assert.deepEqual(await f.client.header(a.queue.id), a.queue);
  assert.deepEqual(await f.client.entries(a.queue.id, 0, 10), before);
  assert.notEqual(before.items[0]!.entryId, before.items[2]!.entryId);
  assert.equal(f.server.queue(a.queue.id)!.sessionId, undefined);
  const sessions = new SessionsClient(f.http);
  const restored = await sessions.start({queue: {queueId: a.queue.id, entryId: a.queue.current!.entryId}}, {state: 'paused', startPositionMs: 37250}, 'retention-paused-0001');
  assert.notEqual(restored.id, a.session!.id); assert.notEqual(restored.id, b.session!.id);
  assert.equal(restored.state, 'paused'); assert.equal(restored.presentation.startPositionMs, 37250);
  assert.deepEqual(restored.queue, {queueId: a.queue.id, entryId: a.queue.current!.entryId});
  const appended = await f.client.addSegment(a.queue.id, a.queue.revision, 'end', items('song-c')[0]!);
  assert.equal(appended.queue.id, a.queue.id);
  assert.deepEqual((await f.client.entries(a.queue.id, 0, 10)).items.slice(0, 3), before.items);
});

test('all three actual candidates survive and the next normalized set discards the superseded queue', async () => {
  const f = fixture(), ids: string[] = [];
  for (const item of ['song-a', 'video-b', 'video-c']) {
    ids.push((await f.client.create(items(item), {retainQueueIds: ids})).queue.id);
  }
  const next = await f.client.createSelector({items: {ids: ['video-d']}}, {retainQueueIds: ids});
  for (const id of [...ids, next.queue.id]) assert.equal((await f.client.header(id)).id, id);
  await f.client.create(items('video-e'), {retainQueueIds: [ids[1]!, ids[2]!, next.queue.id]});
  await assert.rejects(f.client.header(ids[0]!), {status: 404});
  for (const id of [ids[1]!, ids[2]!, next.queue.id]) await f.client.header(id);
});

test('QueueClient validates the complete bounded dense distinct list before a request', async () => {
  const requests: V1Request[] = [];
  const client = new QueueClient({send: async request => { requests.push(request); throw new Error('unexpected request'); }});
  for (const retainQueueIds of [null, 'queue', [undefined], new Array(1), [''], ['a', 'a'], ['a', 'b', 'c', 'd'], ['a/b'], ['a.b'], ['a\nb'], ['a\n'], ['a\0'], ['a%2Fb'], ['a'.repeat(129)]]) {
    await assert.rejects(client.create(items('movie'), {retainQueueIds: retainQueueIds as never}), /retainQueueIds/);
    assert.equal(requests.length, 0);
  }
});

test('QueueClient copies caller retention data and forwards the same option through createSelector', async () => {
  const f = fixture(), a = await f.client.create(items('a'));
  const keep = [a.queue.id];
  const creating = f.client.createSelector({items: {ids: ['b']}}, {retainQueueIds: keep});
  keep[0] = 'mutated-after-intent';
  await creating;
  const body = f.requests.at(-1)!.body as {retainQueueIds: readonly string[]};
  assert.deepEqual(body.retainQueueIds, [a.queue.id]); assert.ok(Object.isFrozen(body.retainQueueIds));
  await f.client.header(a.queue.id);
});

test('fake retained creates replay exact receipts, digest a semantic set and reject a changed retention body', async () => {
  const f = fixture(), a = await f.client.create(items('a'));
  const b = await f.client.create(items('b'), {retainQueueIds: [a.queue.id]});
  const http = fakeHttp(f.server), key = 'retention-replay-0001';
  const client = new QueueClient(http, () => key);
  const first = await client.create(items('c'), {retainQueueIds: [a.queue.id, b.queue.id], startPlayback: {state: 'paused'}});
  const replay = await client.create(items('c'), {retainQueueIds: [b.queue.id, a.queue.id], startPlayback: {state: 'paused'}});
  assert.deepEqual(replay, first);
  await assert.rejects(client.create(items('c'), {retainQueueIds: [a.queue.id], startPlayback: {state: 'paused'}}), {status: 422, code: 'idempotency_key_reused'});
  const latest = await f.client.create(items('d'));
  assert.deepEqual(await client.create(items('c'), {retainQueueIds: [a.queue.id, b.queue.id], startPlayback: {state: 'paused'}}), first);
  await assert.rejects(f.client.header(a.queue.id), {status: 404});
  await assert.rejects(f.client.header(first.queue.id), {status: 404});
  await f.client.header(latest.queue.id);
});

test('fake invalid/unknown retention and failed selector builds do not retire protected or current queues or commit receipts', async () => {
  const f = fixture(), a = await f.client.create(items('song-a'));
  const b = await f.client.create(items('video-b'), {retainQueueIds: [a.queue.id], startPlayback: {state: 'playing'}});
  const key = 'retention-failed-0001';
  const body = {segments: items('c'), retainQueueIds: [a.queue.id, 'unknown-real-looking-id']};
  const failed = await f.server.handle({method: 'POST', path: '/v1/queues', headers: {'Idempotency-Key': key}, body});
  assert.equal(failed.status, 404);
  for (const retainQueueIds of [[a.queue.id, a.queue.id], ['a', 'b', 'c', 'd'], [a.queue.id, 'invalid/queue'], [a.queue.id, b.queue.id + '\n']]) {
    assert.equal((await f.server.handle({method: 'POST', path: '/v1/queues', headers: {'Idempotency-Key': key}, body: {...body, retainQueueIds}})).status, 400);
  }
  const badSelector = await f.server.handle({method: 'POST', path: '/v1/queues', headers: {'Idempotency-Key': key}, body: {segments: [{source: {}}], retainQueueIds: [a.queue.id]}});
  assert.equal(badSelector.status, 422);
  assert.deepEqual(await f.client.header(a.queue.id), a.queue); assert.deepEqual(await f.client.header(b.queue.id), b.queue);
  assert.equal(f.server.queue(b.queue.id)!.sessionId, b.session!.id); assert.equal(f.server.session(b.session!.id)!.ended, false);
  // The failed key was never committed, so a corrected body can use it.
  await new QueueClient(f.http, () => key).create(items('c'), {retainQueueIds: [a.queue.id, b.queue.id]});
});

for (const change of ['device', 'profile', 'account', 'authority', 'retired', 'staged', 'group'] as const) {
  test(`fake retention rejects ${change} queue ownership without changing current data`, async () => {
    const f = fixture(), a = await f.client.create(items('a'));
    const current = await f.client.create(items('b'), {retainQueueIds: [a.queue.id], startPlayback: {state: 'playing'}});
    const candidate = f.server.queue(a.queue.id)!;
    if (change === 'device') Object.assign(candidate, {deviceId: 'another-device'});
    else if (change === 'profile') Object.assign(candidate, {profileId: 'another-profile'});
    else if (change === 'account') Object.assign(candidate, {accountId: 'another-account'});
    else if (change === 'authority') Object.assign(candidate, {authority: 'hosted'});
    else if (change === 'group') Object.assign(candidate, {ownerKind: 'group'});
    else Object.assign(candidate, {deviceId: (change === 'staged' ? '~' : '!') + candidate.deviceId});
    await assert.rejects(f.client.create(items('c'), {retainQueueIds: [a.queue.id]}), {status: 404});
    assert.deepEqual(await f.client.header(current.queue.id), current.queue);
    assert.equal(f.server.queue(current.queue.id)!.sessionId, current.session!.id);
  });
}

test('another real device of the same viewer can read but cannot retain a queue', async () => {
  const f = fixture(), a = await f.client.create(items('a'));
  const other = new QueueClient(fakeHttp(f.server.asDevice('other-device')), () => 'retention-other-00001');
  assert.equal((await other.header(a.queue.id)).id, a.queue.id);
  await assert.rejects(other.create(items('b'), {retainQueueIds: [a.queue.id]}), {status: 404});
  const otherQueue = await other.create(items('b'));
  assert.notEqual(otherQueue.queue.id, a.queue.id);
  await f.client.create(items('c'));
  await assert.rejects(f.client.header(a.queue.id), {status: 404});
  await other.header(otherQueue.queue.id);
});

for (const mode of ['omitted', 'empty', 'retained'] as const) for (const field of ['authority', 'accountId', 'profileId'] as const) {
  test(`a ${mode} retention receipt never exposes a previous ${field} on the same device`, async () => {
    const f = fixture(), a = await f.client.create(items('a'));
    const key = 'retention-viewer-0001', body = {segments: items('b'), ...(mode === 'omitted' ? {} : {retainQueueIds: mode === 'empty' ? [] : [a.queue.id]})};
    assert.equal((await f.server.handle({method: 'POST', path: '/v1/queues', headers: {'Idempotency-Key': key}, body})).status, 201);
    const changed = f.server.asViewer({...viewer, [field]: field === 'authority' ? 'hosted' : 'other'});
    const response = await changed.handle({method: 'POST', path: '/v1/queues', headers: {'Idempotency-Key': key}, body});
    assert.equal(response.status, 422);
    assert.equal(Object.hasOwn(response.body as object, 'queue'), false);
    assert.equal((await changed.handle({method: 'GET', path: `/v1/queues/${a.queue.id}`})).status, 404);
  });
}

test('retained queue data outlives receipts but remains subject to discard and idle expiry', async () => {
  let now = 1_000;
  const f = fixture({now: () => now}), a = await f.client.create(items('a'));
  const b = await f.client.create(items('b'), {retainQueueIds: [a.queue.id]});
  now += 2 * 24 * 60 * 60 * 1000;
  f.server.sweepQueues();
  // Expired create receipts are not a requirement for retaining real queue rows.
  await f.client.create(items('c'), {retainQueueIds: [a.queue.id, b.queue.id]});
  await f.client.discard(a.queue.id); await assert.rejects(f.client.header(a.queue.id), {status: 404});
  now += 91 * 24 * 60 * 60 * 1000;
  f.server.sweepQueues();
  await assert.rejects(f.client.header(b.queue.id), {status: 404});
  assert.equal(f.server.queue(b.queue.id), undefined);
});
