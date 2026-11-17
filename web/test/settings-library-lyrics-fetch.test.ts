import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {LibraryLyricsFetch} from '../src/screens/settings/libraries/lyrics-fetch.ts';
const receipt = {id: 'job', kind: 'fetch_lyrics', state: 'waiting'};
const operationIdFixture = 'operation-key-0001';
test('one in-flight lyrics command and uncertain manual retry retain the same operation', async () => {
  const bodies: unknown[] = []; let reject!: (error: unknown) => void; let attempt = 0; let generated = 0;
  const api = {requestRaw: async (path: string, method: string, options: any) => {
    assert.equal(path, '/v1/jobs'); assert.equal(method, 'POST'); bodies.push(options);assert.deepEqual(options.body,{kind:'fetch_lyrics',target:{library:'music'}});
    if (++attempt === 1) return new Promise((_resolve, fail) => {reject = fail;});
    return {status:202,headers:{},body:receipt};
  }} as LibraryContentApi;
  const source = new LibraryLyricsFetch({api, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  const first = source.run(); assert.equal(source.run(), first); reject(new Error('timeout'));
  await assert.rejects(first); assert.deepEqual(await source.run(), {id: 'job', state: 'waiting'});
  assert.deepEqual(bodies.map((call: any) => call.headers), [{'Idempotency-Key': operationIdFixture}, {'Idempotency-Key': operationIdFixture}]); assert.equal(generated, 1); source.dispose();
});
test('wrong job kind receipt is not success and retains the operationIdFixture', async () => {
  let generated = 0;
  const source = new LibraryLyricsFetch({api: {requestRaw: async () => ({status:202,headers:{},body:{...receipt,kind:'other'}})} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  await assert.rejects(source.run()); await assert.rejects(source.run()); assert.equal(generated, 1); source.dispose();
});
test('retirement aborts the request and fences its late receipt', async () => {
  let finish!: (value: unknown) => void; let signal: AbortSignal | undefined;
  const source = new LibraryLyricsFetch({api: {requestRaw: (_path: string, _method: string, options: any) => {signal = options.signal;return new Promise(resolve => {finish = resolve;});}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => operationIdFixture});
  const result = source.run(); source.dispose(); assert.equal(signal?.aborted, true); finish({status:202,headers:{},body:receipt}); assert.equal(await result, undefined); assert.equal(await source.run(), undefined);
});
test('failed operationIdFixture generation never sends the command', async () => {
  let calls = 0;
  const source = new LibraryLyricsFetch({api: {requestRaw: async () => {calls++;return {status:202,headers:{},body:receipt};}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {throw new Error('no entropy');}});
  await assert.rejects(source.run()); assert.equal(calls, 0); source.dispose();
});

test('definite refusal allows a new operation while a valid existing job is accepted', async () => {
  let calls = 0, generated = 0;
  const source = new LibraryLyricsFetch({api: {requestRaw: async () => {if (++calls === 1) return {status:403,headers:{}};return {status:202,headers:{},body:{...receipt,state:'done'}};}} as LibraryContentApi, libraryId: 'music', isCurrent: () => true, randomId: () => {generated++;return operationIdFixture;}});
  await assert.rejects(source.run()); assert.deepEqual(await source.run(), {id: 'job', state: 'done'}); assert.equal(generated, 2); source.dispose();
});
