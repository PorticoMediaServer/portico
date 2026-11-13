import test from 'node:test';
import assert from 'node:assert/strict';
import {LiveSourceController} from '../src/screens/live/owner/source-controller.ts';
import {rebuiltSource as source, rebuiltDirectory as document} from './live-owner-fixtures.ts';
test('owner actions use displayed revision and encoded source, then read back', async () => {
  const calls: unknown[][] = [];
  const controller = new LiveSourceController({async request<T>(...args: unknown[]) {calls.push(args);return document as T;}, async requestRaw(path, method, options) {calls.push([path,method,options]);return {status:200,headers:{},body:{...source,paused:true,revision:'8'}};}}, 'server', () => true, () => {}, () => 'operation-id');
  await controller.load();assert.equal(await controller.act(source, 'toggle'), true);
  assert.deepEqual(calls[1]?.slice(0, 3), [`/v1/admin/live/sources/${source.id}`, 'PATCH', {body:{paused:true},headers:{'If-Match':'\"7\"'},signal:calls[1]?.[2] && (calls[1][2] as any).signal,singleAttempt:true}]);
  assert.equal(calls.length, 3);controller.dispose();
});
test('retired identity discards a late list and prevents writes', async () => {
  let resolve!: (value: unknown) => void;let current = true;let calls = 0;
  const controller = new LiveSourceController({request<T>() {calls++;return new Promise(done => {resolve = done;}) as Promise<T>;}}, 'server', () => current, () => {});
  const pending = controller.load();current = false;resolve(document);await pending;
  assert.equal(controller.getSnapshot().document, undefined);assert.equal(await controller.act(source, 'remove'), false);assert.equal(calls, 1);controller.dispose();
});
test('stale displayed rows cannot remove a replacement source revision', async () => {
  let calls = 0;
  const controller = new LiveSourceController({async request<T>() {calls++;return {...document, items: [{...source, revision: '8'}]} as T;}}, 'server', () => true, () => {});
  await controller.load();assert.equal(await controller.act(source, 'remove'), false);assert.equal(calls, 1);controller.dispose();
});
test('conflicting removal refreshes facts without replaying the command', async () => {
  let reads = 0, writes = 0;
  const conflict = {status: 409};
  const controller = new LiveSourceController({async request<T>(_path: string, method?: string) {if (method === 'DELETE') {writes++;throw conflict;}reads++;return document as T;}}, 'server', () => true, () => {});
  await controller.load();assert.equal(await controller.act(source, 'remove'), false);assert.equal(writes, 1);assert.equal(reads, 2);assert.deepEqual(controller.getSnapshot().error, {status: 409, code: 'invalid_response'});controller.dispose();
});
test('authorization failure retires the controller and reports the boundary', async () => {
  const forbidden = {status: 403};let reported: unknown;
  const controller = new LiveSourceController({async request<T>(): Promise<T> {throw forbidden;}}, 'server', () => true, error => {reported = error;});
  await controller.load();assert.deepEqual(reported, {status: 403, code: 'invalid_response'});assert.equal(await controller.act(source, 'refresh'), false);
});

test('removal uses the idempotent rebuilt DELETE route without an invented receipt', async () => {
  let writes = 0;
  const controller = new LiveSourceController({async request<T>(path: string, method?: string) {
    if (method === 'DELETE') {writes++;assert.equal(path, '/v1/admin/live/sources/lsr_saved');return undefined as T;}
    return document as T;
  }}, 'server', () => true, () => {});
  await controller.load();assert.equal(await controller.act(source, 'remove'), true);assert.equal(writes, 1);controller.dispose();
});
test('a superseded list response cannot replace the newer result', async () => {
  const resolves: ((value: unknown) => void)[] = [];
  const controller = new LiveSourceController({request<T>() {return new Promise(done => resolves.push(done)) as Promise<T>;}}, 'server', () => true, () => {});
  const first = controller.load(), second = controller.load();
  resolves[1]({...document, items: [{...source, revision: '9'}]});await second;
  resolves[0](document);await first;
  assert.equal(controller.getSnapshot().document?.sources[0]?.revision, '9');controller.dispose();
});
test('disposing during a command aborts it and does not launch a readback', async () => {
  let complete!: (value: unknown) => void, signal: AbortSignal | undefined;let calls = 0;
  const controller = new LiveSourceController({request<T>(_path: string, method?: string, _body?: unknown, active?: AbortSignal) {
    calls++;if (method === 'DELETE') {signal = active;return new Promise(done => {complete = done;}) as Promise<T>;}
    return Promise.resolve(document as T);
  }}, 'server', () => true, () => {});
  await controller.load();const action = controller.act(source, 'remove');controller.dispose();complete({});
  assert.equal(await action, false);assert.equal(signal?.aborted, true);assert.equal(calls, 2);
});

test('a quiet read cannot overwrite a newer owner action', async () => {
  let quiet = false, finish!: (value: unknown) => void;
  const controller = new LiveSourceController({async request<T>(_path: string, method?: string) {
    if (method === 'DELETE') return undefined as T;
    if (quiet) {quiet = false;return new Promise(resolve => {finish = resolve;}) as Promise<T>;}
    return document as T;
  }}, 'server', () => true, () => {});
  await controller.load();quiet = true;const pending = controller.refreshQuietly();await controller.act(source, 'remove');
  const before = controller.getSnapshot();finish({items: [{...source, name: 'Stale'}]});
  assert.deepEqual(await pending, {kind: 'deferred'});assert.equal(controller.getSnapshot(), before);controller.dispose();
});

test('an uploaded source can refresh its saved playlist and guide now', async () => {
  const uploaded = {...source, config: {uploaded: true, guideUploaded: true}};
  const calls: string[] = [];
  const controller = new LiveSourceController({async request<T>(path: string, method?: string) {
    calls.push(`${method ?? 'GET'} ${path}`);
    return (method === 'POST' ? uploaded : {items: [uploaded]}) as T;
  }}, 'server', () => true, () => {});
  await controller.load();
  const row = controller.getSnapshot().document!.sources[0]!;
  assert.equal(row.acquisition, 'upload');
  assert.equal(await controller.act(row, 'refresh'), true);
  assert.deepEqual(calls, ['GET /v1/admin/live/sources', `POST /v1/admin/live/sources/${source.id}/refresh`, 'GET /v1/admin/live/sources']);
  controller.dispose();
});
