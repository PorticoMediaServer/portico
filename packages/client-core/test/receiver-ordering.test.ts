import assert from 'node:assert/strict';
import test from 'node:test';
import {readCastMetadata} from '../src/query/cast-metadata.ts';
import {readTogetherItem, receiverLinkOperations} from '../src/query/receiver.ts';

function pending() {let resolve!: (value: any) => void, reject!: (error: unknown) => void;const promise = new Promise<any>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}
const settled = () => new Promise<void>(resolve => setImmediate(resolve));

test('Cast settles the original read before checking cancellation and never parses a retired malformed answer', async () => {
  const read = pending(), order: string[] = [], cancelled = new DOMException('retired', 'AbortError');let live = true;
  const api = {request<T>(): Promise<T> {order.push('request');return read.promise;}};
  const task = readCastMetadata(api, 'item', () => {order.push('guard');if (!live) throw cancelled;}, 'identity');
  const assertion = assert.rejects(task, error => error === cancelled);
  await settled();assert.deepEqual(order, ['guard', 'request']);
  live = false;
  read.resolve(new Proxy({}, {get(_target, key) {if (key === 'then') return undefined;order.push('parse');throw new Error('parser ran');}}));
  await assertion;assert.deepEqual(order, ['guard', 'request', 'guard']);
});

test('Cast keeps an original transport refusal ahead of a later cancellation check', async () => {
  const read = pending(), order: string[] = [], refusal = new Error('original refusal');let live = true;
  const task = readCastMetadata({request<T>() {order.push('request');return read.promise as Promise<T>;}}, 'item', () => {order.push('guard');if (!live) throw new DOMException('retired', 'AbortError');}, 'identity');
  const assertion = assert.rejects(task, error => error === refusal);
  live = false;read.reject(refusal);await assertion;
  assert.deepEqual(order, ['guard', 'request']);
});

test('Together item parsing follows the captured transport lifetime check', async () => {
  const read = pending(), cancelled = new DOMException('retired', 'AbortError');let live = true, parsed = false;
  const api = {async request<T>(): Promise<T> {const raw = await read.promise;if (!live) throw cancelled;return raw;}};
  const task = readTogetherItem(api, 'item'), assertion = assert.rejects(task, error => error === cancelled);
  live = false;read.resolve(new Proxy({}, {get(_target, key) {if (key === 'then') return undefined;parsed = true;throw new Error('parser ran');}}));
  await assertion;assert.equal(parsed, false);
});

test('Quick Connect validates the response only after the original request settles', async () => {
  const read = pending();let validated = false;
  const task = receiverLinkOperations({request<T>() {return read.promise as Promise<T>;}}).preview('code');
  const assertion = assert.rejects(task, /quick_connect_unreadable/);
  await settled();assert.equal(validated, false);
  read.resolve({get requestId() {validated = true;return null;}});
  await assertion;assert.equal(validated, true);
});
