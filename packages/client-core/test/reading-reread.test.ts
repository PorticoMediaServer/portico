import test from 'node:test';
import assert from 'node:assert/strict';
import {parseReadingReread, readingRereadPath, rereadReadingSource, type ReadingRereadApi, type ReadingRereadTarget} from '../src/reading-reread.ts';

const rev = (digit: string) => digit.repeat(64);
const target: ReadingRereadTarget = {itemId: 'book-1', sourceId: 'source-1', sourceRevision: rev('a'), metadataRevision: rev('b')};
const receipt = (over: Record<string, unknown> = {}) => ({sourceId: target.sourceId, sourceRevision: target.sourceRevision, metadataRevision: rev('c'),
  refreshed: ['author', 'series', 'seriesNumber'], ...over});

test('a refreshed reread keeps exact keys and derives refreshed', () => {
  const value = parseReadingReread(receipt(), target);
  assert.deepEqual(value.refreshed, ['author', 'series', 'seriesNumber']);
  assert.deepEqual(value.unknown, []);
  assert.equal(value.status, 'refreshed');
  assert.equal(value.sourceRevision, target.sourceRevision);
  assert.equal(value.metadataRevision, rev('c'));
  assert.equal('author' in value, false, 'the caller rereads repair values');
});

test('PDF partitions stay truthful: unknown all three, or author refreshed with series unknown', () => {
  const allUnknown = parseReadingReread(receipt({refreshed: [], unknown: ['author', 'series', 'seriesNumber']}), target);
  assert.deepEqual(allUnknown.refreshed, []);
  assert.deepEqual(allUnknown.unknown, ['author', 'series', 'seriesNumber']);
  assert.equal(allUnknown.status, 'unknown');
  const authorOnly = parseReadingReread(receipt({refreshed: ['author'], unknown: ['series', 'seriesNumber']}), target);
  assert.deepEqual(authorOnly.refreshed, ['author']);
  assert.deepEqual(authorOnly.unknown, ['series', 'seriesNumber']);
  assert.equal(authorOnly.status, 'unknown');
  const noUnknownField = parseReadingReread(receipt({unknown: undefined}), target);
  assert.deepEqual(noUnknownField.unknown, []);
  assert.equal(noUnknownField.status, 'refreshed');
});

test('the source identity, metadata hash and partition are exact', () => {
  for (const bad of [
    receipt({sourceId: 'other'}),
    receipt({sourceRevision: rev('d')}),
    receipt({metadataRevision: 'not-a-hash'}),
    receipt({metadataRevision: undefined}),
    receipt({unknown: ['author']}),
    receipt({refreshed: ['author', 'author', 'series', 'seriesNumber']}),
    receipt({refreshed: ['author', 'series'], unknown: ['series']}),
    receipt({refreshed: ['author'], unknown: ['series']}),
    receipt({refreshed: ['author', 'series', 'seriesNumber', 'pages']}),
    receipt({extra: true}),
  ]) assert.throws(() => parseReadingReread(bad, target), /invalid_reading_reread/);
});

test('a cancelled reread refuses before any I/O', async () => {
  const controller = new AbortController(); controller.abort();
  let called = 0;
  const api = {request: async () => {called++; throw new Error('must not run');}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(api, target, {signal: controller.signal, isCurrent: () => true}), {name: 'AbortError'});
  assert.equal(called, 0);
});

test('a retired reread refuses before any I/O', async () => {
  let called = 0;
  const api = {request: async () => {called++; throw new Error('must not run');}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(api, target, {signal: new AbortController().signal, isCurrent: () => false}), {name: 'AbortError'});
  assert.equal(called, 0);
});

test('the parser validates its target independently of the response', () => {
  for (const bad of [
    {...target, itemId: 'bad id'},
    {...target, sourceId: 'bad id'},
    {...target, sourceRevision: 'a'},
    {...target, metadataRevision: 'B'.repeat(64)},
  ]) assert.throws(() => parseReadingReread(receipt(), bad), /invalid_reading_reread/);
});

test('before the official descriptor exists the reread refuses without I/O; once it exists the body is copied and errors/retirement are exact', async () => {
  const path = readingRereadPath(target.itemId);
  if (!path) {
    let called = 0;
    const api = {request: async () => {called++; throw new Error('must not run');}} as unknown as ReadingRereadApi;
    await assert.rejects(() => rereadReadingSource(api, target, {signal: new AbortController().signal, isCurrent: () => true}), {code: 'reading_reread_unavailable'});
    assert.equal(called, 0);
    return;
  }
  const seen: unknown[] = [], transport = Object.assign(new Error('network'), {code: 'transport'});
  const failing = {request: async (requestPath: string, method?: string, body?: unknown) => {seen.push({requestPath, method, body}); throw transport;}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(failing, target, {signal: new AbortController().signal, isCurrent: () => true}), error => error === transport);
  assert.deepEqual(seen, [{requestPath: path, method: 'POST', body: {sourceId: target.sourceId, expectedSourceRevision: target.sourceRevision, expectedMetadataRevision: target.metadataRevision}}]);
  assert(Object.isFrozen((seen[0] as {body: unknown}).body));
  const controller = new AbortController();
  const aborting = {request: async () => {controller.abort(); return receipt();}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(aborting, target, {signal: controller.signal, isCurrent: () => true}), {name: 'AbortError'});
  let current = true;
  const retiring = {request: async () => {current = false; return receipt();}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(retiring, target, {signal: new AbortController().signal, isCurrent: () => current}), {name: 'AbortError'});
  const afterAbort = new AbortController();
  const abortRejecting = {request: async () => {afterAbort.abort(); throw transport;}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(abortRejecting, target, {signal: afterAbort.signal, isCurrent: () => true}), {name: 'AbortError'});
  current = true;
  const retireRejecting = {request: async () => {current = false; throw transport;}} as unknown as ReadingRereadApi;
  await assert.rejects(() => rereadReadingSource(retireRejecting, target, {signal: new AbortController().signal, isCurrent: () => current}), {name: 'AbortError'});
});

