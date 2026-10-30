import test from 'node:test';
import assert from 'node:assert/strict';
import {capacityRetryHttp, type WebPlaybackCapacityWait} from '../src/platform/playback/capacity-retry.ts';
import type {V1Request, V1Response} from '../../packages/client-core/src/playback-v1/http.ts';

const refusal: V1Response = {status: 429, headers: {'Retry-After': '1'}, body: {error: {code: 'conversion_capacity', retry: 'same_request'}}};
const success: V1Response = {status: 201, headers: {}, body: {id: 'session'}};
const request: V1Request = {method: 'POST', path: '/v1/playback/sessions', headers: {'Idempotency-Key': 'original-key-0001'}, body: {itemId: 'one'}};
const flush = async () => {for (let i = 0; i < 8; i++) await Promise.resolve();};
function fixture(response: (index: number) => V1Response = index => index ? success : refusal) {
  let now = 0, hidden = false, current = true, wait: WebPlaybackCapacityWait | undefined;
  const life = new AbortController(), listeners = new Set<() => void>(), timers = new Map<object, () => void>(), sent: V1Request[] = [];
  const http = capacityRetryHttp({http: {async send(value) {sent.push(value); return response(sent.length - 1);}}, life: life.signal,
    isCurrent: () => current, captureCurrent: () => () => current, publish: value => {wait = value;},
    timers: {now: () => now, setTimer: callback => {const key = {}; timers.set(key, callback); return key;}, clearTimer: key => {timers.delete(key as object);}},
    visibility: {hidden: () => hidden, onVisible: callback => {listeners.add(callback); return () => {listeners.delete(callback);};}}});
  return {http, sent, life, timers, listeners, get wait() {return wait;}, setNow(value: number) {now = value;},
    setHidden(value: boolean) {hidden = value; if (!hidden) for (const callback of [...listeners]) callback();}, retire() {current = false;},
    fire() {const callback = timers.values().next().value; assert.ok(callback, 'retry timer exists'); callback();}};
}
test('a timer delivered after the 30-second budget returns the original refusal without resending', async () => {
  const f = fixture(), pending = f.http.send(request); await flush();
  assert.equal(f.sent.length, 1); f.setNow(31_000); f.fire();
  assert.equal(await pending, refusal); assert.equal(f.sent.length, 1); assert.equal(f.wait, undefined);
});
test('a suspended hidden tab waits for foreground and rechecks elapsed time after that wake', async () => {
  const f = fixture(), pending = f.http.send(request); await flush(); f.setHidden(true); f.setNow(1000); f.fire(); await flush();
  assert.equal(f.sent.length, 1); assert.equal(f.listeners.size, 1);
  f.setNow(31_000); f.setHidden(false);
  assert.equal(await pending, refusal); assert.equal(f.sent.length, 1); assert.equal(f.listeners.size, 0);
});
test('foreground recovery inside the budget repeats the captured body and original request key', async () => {
  const f = fixture(), body = {itemId: 'one'}, headers = {'Idempotency-Key': 'original-key-0001'};
  const pending = f.http.send({...request, body, headers}); await flush(); body.itemId = 'changed'; headers['Idempotency-Key'] = 'changed';
  f.setHidden(true); f.setNow(1000); f.fire(); await flush(); assert.equal(f.sent.length, 1);
  f.setNow(2000); f.setHidden(false); assert.equal(await pending, success);
  assert.deepEqual(f.sent[1].body, {itemId: 'one'}); assert.equal(f.sent[1].headers?.['Idempotency-Key'], 'original-key-0001');
  assert.equal(f.listeners.size, 0); assert.equal(f.wait, undefined);
});
test('cancellation during the foreground wait cleans up the subscription and never sends', async () => {
  const f = fixture(), pending = f.http.send(request); await flush(); f.setHidden(true); f.fire(); await flush();
  f.life.abort(); await assert.rejects(pending, {name: 'AbortError'}); assert.equal(f.sent.length, 1); assert.equal(f.listeners.size, 0);
});
test('retirement while hidden prevents the foreground continuation', async () => {
  const f = fixture(), pending = f.http.send(request); await flush(); f.setHidden(true); f.fire(); await flush();
  f.retire(); f.setHidden(false); await assert.rejects(pending, {name: 'AbortError'}); assert.equal(f.sent.length, 1);
});
test('only a directed refusal is replayable and the existing five-retry limit remains', async () => {
  const ordinary = {...refusal, body: {error: {code: 'rate_limited', retry: 'same_request'}}};
  const unchanged = fixture(() => ordinary); assert.equal(await unchanged.http.send(request), ordinary); assert.equal(unchanged.timers.size, 0);
  const f = fixture(() => refusal), pending = f.http.send(request); await flush();
  for (let i = 1; i <= 5; i++) {f.setNow(i * 1000); f.fire(); await flush();}
  assert.equal(await pending, refusal); assert.equal(f.sent.length, 6); assert.equal(f.wait, undefined);
});
