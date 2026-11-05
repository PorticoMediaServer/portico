import {farewellPlaybackSession} from '../src/query/playback.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import {readDiscTitles, prepareDiscTitle, chooseDiscMainTitle} from '../src/query/disc-titles.ts';
import {stopOriginalPreparingSession} from '../src/playback-v1/preparing-cleanup.ts';
import type {PreparingSessionView} from '../src/playback-v1/sessions.ts';

function pending() {let resolve!: (value: any) => void, reject!: (error: unknown) => void;const promise = new Promise<any>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}
const write = {itemId: 'item', versionId: 'version', revision: 'a'.repeat(64), titleId: 'b'.repeat(64)};

for (const operation of ['read', 'prepare', 'choose'] as const) {
  test(`disc ${operation} settles the original request and checks lifetime before reading a malformed response`, async () => {
    const read = pending(), controller = new AbortController(), order: string[] = [];
    const api = {request<T>(): Promise<T> {order.push('request');return read.promise;}};
    const life = {signal: controller.signal, isCurrent: () => {order.push('guard');return true;}};
    const task = operation === 'read' ? readDiscTitles(api, 'item', 'version', life)
      : operation === 'prepare' ? prepareDiscTitle(api, write, life) : chooseDiscMainTitle(api, write, life);
    assert.deepEqual(order, ['guard', 'request']);
    controller.abort();
    read.resolve(new Proxy({}, {get(_target, key) {if (key === 'then') return undefined;order.push('parse');throw new Error('parser ran');}}));
    assert.equal(await task, undefined);
    assert.deepEqual(order, ['guard', 'request']);
  });
}

test('disc main-title refusal retains the original stale error even after the lifetime retires', async () => {
  const read = pending(), controller = new AbortController();
  const api = {request<T>(): Promise<T> {return read.promise;}};
  const task = chooseDiscMainTitle(api, write, {signal: controller.signal, isCurrent: () => true});
  const assertion = assert.rejects(task, {code: 'disc_titles_stale'});
  controller.abort();read.reject({status: 412});await assertion;
});

test('native preparation cleanup checks original lifetime before consuming or parsing a settled response', async () => {
  const read = pending(), order: string[] = [], controller = new AbortController();let live = true;
  const fetcher: typeof fetch = async () => {order.push('request');return read.promise;};
  const offered = {id: 'original', presentation: {generation: 1}} as PreparingSessionView;
  const task = stopOriginalPreparingSession(fetcher, 'https://server.test', 'captured', () => live, offered, controller.signal, () => true, () => true);
  const assertion = assert.rejects(task, /Original preparing owner retired/);
  assert.deepEqual(order, ['request']);live = false;
  read.resolve({body: {cancel: async () => {order.push('cancel');}}, get headers() {order.push('parse');throw new Error('parser ran');}});
  await assertion;assert.deepEqual(order, ['request', 'cancel']);
});


for (const id of ['', '.', '..']) {
  test(`disc and farewell ${JSON.stringify(id)} keep original URL construction ahead of transport settlement`, async () => {
    const read = pending(), calls: unknown[][] = [], refusal = new Error('original refusal');
    const task = readDiscTitles({request<T>(...args: unknown[]) {calls.push(args);return read.promise as Promise<T>;}}, id, 'version', {signal: new AbortController().signal, isCurrent: () => true});
    const assertion = assert.rejects(task, error => error === refusal);
    assert.equal(calls[0]?.[0], `/v1/items/${encodeURIComponent(id)}/disc-titles?versionId=version`);
    read.reject(refusal);await assertion;
    farewellPlaybackSession((...args) => {calls.push(args);}, id, 2);
    assert.deepEqual(calls[1], [`/v1/playback/sessions/${encodeURIComponent(id)}`, 'DELETE', {generation: 2}]);
  });
}
