import test from 'node:test';
import assert from 'node:assert/strict';
import {LiveSourceController, type OwnerLiveSource} from '../src/screens/live/owner/source-controller.ts';
import {storedSource as source, document, actionReceipt} from './live-owner-fixtures.ts';
test('owner actions use displayed revision and encoded source, then read back', async () => {
  const calls: unknown[][] = [];
  const controller = new LiveSourceController({async request<T>(...args: unknown[]) {calls.push(args);return (args[1] === 'POST' ? actionReceipt : document) as T;}}, 'server', () => true, () => {}, () => 'operation-id');
  await controller.load();assert.equal(await controller.act(source, 'toggle'), true);
  assert.deepEqual(calls[1]?.slice(0, 3), [`/v1/admin/live-sources/${source.id}/enabled`, 'POST', {expectedRevision: 7, requestId: 'operation-id', enabled: false}]);
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
  const controller = new LiveSourceController({async request<T>() {calls++;return {...document, sources: [{...source, revision: 8}]} as T;}}, 'server', () => true, () => {});
  await controller.load();assert.equal(await controller.act(source, 'remove'), false);assert.equal(calls, 1);controller.dispose();
});
test('conflicting removal refreshes facts without replaying the command', async () => {
  let reads = 0, writes = 0;
  const conflict = {status: 409};
  const controller = new LiveSourceController({async request<T>(_path: string, method?: string) {if (method === 'POST') {writes++;throw conflict;}reads++;return document as T;}}, 'server', () => true, () => {});
  await controller.load();assert.equal(await controller.act(source, 'remove'), false);assert.equal(writes, 1);assert.equal(reads, 2);assert.deepEqual(controller.getSnapshot().error, {status: 409, code: 'invalid_response'});controller.dispose();
});
test('authorization failure retires the controller and reports the boundary', async () => {
  const forbidden = {status: 403};let reported: unknown;
  const controller = new LiveSourceController({async request<T>(): Promise<T> {throw forbidden;}}, 'server', () => true, error => {reported = error;});
  await controller.load();assert.deepEqual(reported, {status: 403, code: 'invalid_response'});assert.equal(await controller.act(source, 'refresh'), false);
});

test('transport retry retains the logical write id', async () => {
  const ids: unknown[] = [];let attempt = 0, serial = 0;
  const controller = new LiveSourceController({async request<T>(_path: string, method?: string, body?: unknown) {
    if (method === 'POST') {ids.push((body as {requestId: string}).requestId);if (++attempt === 1) throw {status: 503};}
    return (method === 'POST' ? actionReceipt : document) as T;
  }}, 'server', () => true, () => {}, () => String(++serial));
  await controller.load();await controller.act(source, 'remove');await controller.act(source, 'remove');
  assert.deepEqual(ids, ['1', '1']);controller.dispose();
});
test('a response for another server cannot populate the page', async () => {
  const controller = new LiveSourceController({async request<T>() {return {...document, serverId: 'other'} as T;}}, 'server', () => true, () => {});
  await controller.load();assert.equal(controller.getSnapshot().document, undefined);assert.ok(controller.getSnapshot().error);controller.dispose();
});
test('a superseded list response cannot replace the newer result', async () => {
  const resolves: ((value: unknown) => void)[] = [];
  const controller = new LiveSourceController({request<T>() {return new Promise(done => resolves.push(done)) as Promise<T>;}}, 'server', () => true, () => {});
  const first = controller.load(), second = controller.load();
  resolves[1]({...document, sources: [{...source, revision: 9}]});await second;
  resolves[0](document);await first;
  assert.equal(controller.getSnapshot().document?.sources[0]?.revision, 9);controller.dispose();
});
test('disposing during a command aborts it and does not launch a readback', async () => {
  let complete!: (value: unknown) => void, signal: AbortSignal | undefined;let calls = 0;
  const controller = new LiveSourceController({request<T>(_path: string, method?: string, _body?: unknown, active?: AbortSignal) {
    calls++;if (method === 'POST') {signal = active;return new Promise(done => {complete = done;}) as Promise<T>;}
    return Promise.resolve(document as T);
  }}, 'server', () => true, () => {});
  await controller.load();const action = controller.act(source, 'remove');controller.dispose();complete({});
  assert.equal(await action, false);assert.equal(signal?.aborted, true);assert.equal(calls, 2);
});

test('quiet directory/status never cancels or resets an independent command',async()=>{
 let finish!:(value:unknown)=>void,commandSignal:AbortSignal|undefined,gets=0;const controller=new LiveSourceController({async request<T>(_path:string,method?:string,_body?:unknown,signal?:AbortSignal){if(method==='POST'){commandSignal=signal;return new Promise(resolve=>{finish=resolve;}) as Promise<T>;}gets++;return {...document,sources:[{...source,name:gets>1?'Renamed':source.name}]} as T;}},'server',()=>true,()=>{});
 await controller.load();const command=controller.act(source,'toggle');const busy=controller.getSnapshot().busy;
 assert.deepEqual(await controller.refreshQuietly(),{kind:'adopted'});assert.equal(commandSignal?.aborted,false);assert.equal(controller.getSnapshot().busy,busy);assert.equal(controller.getSnapshot().document?.sources[0].name,'Renamed');finish(actionReceipt);assert.equal(await command,true);controller.dispose();
});

test('command admission fences stale quiet directory and original Retry-After survives unreadability',async()=>{
 let quiet=false,finish!:(value:unknown)=>void;const error=Object.assign(new Error('original'),{status:503,retryAfterSeconds:7200});let fail=false;
 const controller=new LiveSourceController({async request<T>(_path:string,method?:string){if(method==='POST')return actionReceipt as T;if(fail)throw error;if(quiet){quiet=false;return new Promise(resolve=>{finish=resolve;}) as Promise<T>;}return document as T;}},'server',()=>true,()=>{});
 await controller.load();quiet=true;const pending=controller.refreshQuietly();await controller.act(source,'toggle');const before=controller.getSnapshot();finish({...document,sources:[{...source,name:'Stale'}]});assert.deepEqual(await pending,{kind:'deferred'});assert.equal(controller.getSnapshot(),before);
 fail=true;const answer=await controller.refreshQuietly();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,error);assert.equal(controller.getSnapshot(),before);controller.dispose();
});
