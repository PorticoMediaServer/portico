import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {LibraryLyricsFetch} from '../src/screens/settings/libraries/lyrics-fetch.ts';
const receipt = {id: 'job', kind: 'library-lyrics-fetch', resource: 'music', state: 'queued'};
const operationIdFixture = 'operation-key-0001';
test('one in-flight lyrics command and uncertain manual retry retain the same operation', async () => {
  const bodies: unknown[] = []; let reject!: (error: unknown) => void; let attempt = 0; let generated = 0;
  const api = {request: async (path: string, method: string, body: unknown) => {
    assert.equal(path, '/v1/libraries/music/lyrics/fetch'); assert.equal(method, 'POST'); bodies.push(body);
    if (++attempt === 1) return new Promise((_resolve, fail) => {reject = fail;});
    return receipt;
  }} as LibraryContentApi;
  const source = new LibraryLyricsFetch({api, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  const first = source.run(); assert.equal(source.run(), first); reject(new Error('timeout'));
  await assert.rejects(first); assert.deepEqual(await source.run(), {id: 'job', state: 'queued'});
  assert.deepEqual(bodies, [{idempotencyKey: operationIdFixture}, {idempotencyKey: operationIdFixture}]); assert.equal(generated, 1); source.dispose();
});
test('wrong resource receipt is not success and retains the operationIdFixture', async () => {
  let generated = 0;
  const source = new LibraryLyricsFetch({api: {request: async () => ({...receipt, resource: 'other'})} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  await assert.rejects(source.run()); await assert.rejects(source.run()); assert.equal(generated, 1); source.dispose();
});
test('retirement aborts the request and fences its late receipt', async () => {
  let finish!: (value: unknown) => void; let signal: AbortSignal | undefined;
  const source = new LibraryLyricsFetch({api: {request: (_path: string, _method: string, _body: unknown, captured: AbortSignal) => {signal = captured;return new Promise(resolve => {finish = resolve;});}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => operationIdFixture});
  const result = source.run(); source.dispose(); assert.equal(signal?.aborted, true); finish(receipt); assert.equal(await result, undefined); assert.equal(await source.run(), undefined);
});
test('failed operationIdFixture generation never sends the command', async () => {
  let calls = 0;
  const source = new LibraryLyricsFetch({api: {request: async () => {calls++;return receipt;}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {throw new Error('no entropy');}});
  await assert.rejects(source.run()); assert.equal(calls, 0); source.dispose();
});

test('definite refusal allows a new operation while a valid existing job is accepted', async () => {
  let calls = 0, generated = 0;
  const source = new LibraryLyricsFetch({api: {request: async () => {if (++calls === 1) throw {status: 403};return {...receipt, state: 'succeeded'};}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  await assert.rejects(source.run()); assert.deepEqual(await source.run(), {id: 'job', state: 'succeeded'}); assert.equal(generated, 2); source.dispose();
});
