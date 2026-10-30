import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackScopeHttp} from '../src-next/platform/playback/scope-http.ts';
import type {V1Request, V1Response} from '../../../packages/client-core/src/playback-v1/http.ts';

test('retiring an authority aborts its in-flight network and rejects a transport that returns late anyway', async () => {
  let captured: V1Request | undefined, finish!: (response: V1Response) => void;
  const http = new PlaybackScopeHttp({send: request => { captured = request; return new Promise(resolve => { finish = resolve; }); }}, () => true);
  const pending = http.send({method: 'GET', path: '/source-read'});
  http.dispose();
  assert.equal(captured?.signal?.aborted, true);
  finish({status: 200, headers: {}, body: {generation: 1}});
  await assert.rejects(pending, /authority retired/);
  await assert.rejects(http.send({method: 'GET', path: '/source-read'}), /authority retired/);
});

test('a changed source authority rejects a response even before the owner disposal callback runs', async () => {
  let active = true, finish!: (response: V1Response) => void;
  let sends = 0, captured: V1Request | undefined;
  const http = new PlaybackScopeHttp({send: request => { sends++; captured = request; return new Promise(resolve => { finish = resolve; }); }}, () => active);
  const pending = http.send({method: 'GET', path: '/source-read'});
  active = false; finish({status: 200, headers: {}});
  await assert.rejects(pending, /authority retired/);
  assert.equal(captured?.signal?.aborted, true);
  await assert.rejects(http.send({method: 'GET', path: '/source-read'}), /authority retired/);
  assert.equal(sends, 1);
});

test('a caller abort cancels its exact request without closing the current principal transport', async () => {
  let captured: V1Request | undefined, finish!: (response: V1Response) => void;
  const http = new PlaybackScopeHttp({send: request => { captured = request; return new Promise(resolve => { finish = resolve; }); }}, () => true);
  const controller = new AbortController();
  const pending = http.send({method: 'GET', path: '/source-read', signal: controller.signal});
  controller.abort(); assert.equal(captured?.signal?.aborted, true);
  finish({status: 200, headers: {}}); await assert.rejects(pending, /authority retired/);
  const next = http.send({method: 'GET', path: '/next-source-read'});
  assert.equal(captured?.signal?.aborted, false);
  finish({status: 204, headers: {}}); assert.equal((await next).status, 204);
  http.dispose();
});

test('an operation guard checks every dispatch and rejects late responses after unregistering', async () => {
  let current = true, sends = 0, finish!: (response: V1Response) => void;
  const http = new PlaybackScopeHttp({send: () => { sends++; return new Promise(resolve => { finish = resolve; }); }}, () => true);
  const controller = new AbortController();
  const unguard = http.guard(controller.signal, () => current);
  const pending = http.send({method: 'GET', path: '/operation-read', signal: controller.signal});
  current = false;
  await assert.rejects(http.send({method: 'POST', path: '/operation-write', signal: controller.signal}), /authority retired/);
  unguard(); finish({status: 200, headers: {}});
  await assert.rejects(pending, /authority retired/);
  assert.equal(sends, 1);
  const next = http.send({method: 'GET', path: '/different-operation'});
  finish({status: 204, headers: {}}); assert.equal((await next).status, 204);
  http.dispose();
});

test('a retired response is available only to owner cleanup and still rejects the original caller', async () => {
  let finish!: (response: V1Response) => void;
  const observed: unknown[] = [], controller = new AbortController();
  const http = new PlaybackScopeHttp({send: () => new Promise(resolve => {finish = resolve;})}, () => true,
    (request, response) => {observed.push([request.path, response]);});
  const pending = http.send({method:'POST',path:'/resource',signal:controller.signal});
  controller.abort(); const response = {status:201,headers:{},body:{id:'created'}};
  finish(response); await assert.rejects(pending, /authority retired/);
  assert.deepEqual(observed, [['/resource',response]]);
  http.dispose();
});
