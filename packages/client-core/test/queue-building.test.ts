import test from 'node:test';
import assert from 'node:assert/strict';
import {QueueClient} from '../src/playback-v1/queue.ts';
import {parseEntry} from '../src/playback-v1/types.ts';
import type {V1Http, V1Request} from '../src/playback-v1/http.ts';

const header = (over: Record<string, unknown> = {}) => ({id: 'q_1', revision: '1', total: 1024, repeat: 'off', segments: [{id: 'g1', kind: 'selector', label: 'Everything', count: 1024, state: 'building'}], ...over});

// P16: a large queue's window has placeholders without an item: pending (still being
// snapshotted) and unavailable (restricted after the snapshot). Both parse.
test('queue placeholders without an item parse', () => {
  const pending = parseEntry({entryId: 's1-2000', position: 7, itemId: '', kind: 'pending', title: '', available: false});
  assert.equal(pending.kind, 'pending');
  assert.equal(pending.itemId, '');
  assert.equal(pending.available, false);
  assert.throws(() => parseEntry({entryId: 's1-1', position: 0, itemId: '', kind: 'movie', title: 'A', available: true}));
});

// P16: when a create can't choose its first entry in time, the header has no current entry and a
// segment is building; awaitCurrent resolves once the server has chosen it, and wake() checks at once.
test('a start that waits for its snapshot resolves when the server chooses the first entry', async () => {
  let reads = 0;
  const http: V1Http = {async send(r: V1Request) {
    assert.equal(r.path, '/v1/queues/q_1');
    reads++;
    const done = reads >= 3;
    return {status: 200, headers: {}, body: done ? header({revision: '4', total: 20000, current: {entryId: 's1-731', position: 0}, shuffle: {seed: '5', lap: 0}, segments: [{id: 'g1', kind: 'selector', label: 'Everything', count: 20000, state: 'ready'}]}) : header()};
  }};
  const client = new QueueClient(http, () => 'key-0000000000000001');
  assert.equal(QueueClient.startPending({queue: (await client.header('q_1')), window: []}), true);
  const waiting = client.awaitCurrent('q_1', {intervalMs: 60_000});
  setTimeout(() => waiting.wake(), 10);
  const ready = await waiting.done;
  assert.equal(ready.current?.entryId, 's1-731');
  assert.equal(reads, 3);
});

test('awaiting a start stops when the build ends without one, or on abort', async () => {
  const http: V1Http = {async send() { return {status: 200, headers: {}, body: header({segments: [{id: 'g1', kind: 'selector', label: 'x', count: 0, state: 'failed'}]})}; }};
  await assert.rejects(new QueueClient(http).awaitCurrent('q_1').done, (e: {code?: string}) => e.code === 'queue_ended');
  const slow: V1Http = {async send() { return {status: 200, headers: {}, body: header()}; }};
  const abort = new AbortController();
  const waiting = new QueueClient(slow).awaitCurrent('q_1', {signal: abort.signal, intervalMs: 60_000});
  setTimeout(() => abort.abort(), 10);
  await assert.rejects(waiting.done, (e: Error) => e.name === 'AbortError');
});

test('a complete browse selector is sent once without client enumeration', async () => {
 const sent: {body?: unknown}[] = [];
 const http: V1Http = {send: async request => {
  sent.push(request);
  return {status: 201, headers: {}, body: {queue: {id: 'q_1', revision: '1', total: 256, repeat: 'off', segments: [{id: 'g_1', kind: 'selector', count: 256, label: 'Movies', state: 'building', sourceRevision: 'browse_frozen'}], current: {entryId: 's_1-0', position: 0}}, window: []}};
 }};
 const source = {query: {libraryId: 'movies', pivot: 'movies', query: {field: 'genre', operator: 'contains-any' as const, value: ['Drama']}, sort: [{field: 'year', direction: 'desc' as const}]}};
 const result = await new QueueClient(http, () => 'whole-view-00001').createSelector(source, {shuffle: true, shuffleSeed: '42'});
 assert.equal(sent.length, 1);
 assert.deepEqual(sent[0].body, {owner: 'device', segments: [{source, order: {mode: 'shuffle', seed: '42'}}]});
 assert.equal(result.queue.segments[0].sourceRevision, 'browse_frozen');
});
