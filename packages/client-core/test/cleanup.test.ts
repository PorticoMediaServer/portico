import test from 'node:test';
import assert from 'node:assert/strict';
import {releaseAll, releaseQuietly, releaseQuietlyAsync, releaseThenRethrow, setCleanupLog} from '../src/cleanup.ts';

function capture() {
  const logged: string[] = [];
  setCleanupLog(label => {logged.push(label);});
  return logged;
}

test('a throwing or rejecting release is logged, never propagated', async () => {
  const logged = capture();
  releaseQuietly('sync', () => {throw new Error('x');});
  releaseQuietly('async', () => Promise.reject(new Error('y')));
  await new Promise(resolve => setTimeout(resolve, 0));
  await releaseQuietlyAsync('awaited', async () => {throw new Error('z');});
  assert.deepEqual(logged, ['sync', 'async', 'awaited']);
});

test('every release runs even after one throws', () => {
  const logged = capture(), ran: number[] = [];
  releaseAll('routes', [() => {ran.push(0);}, () => {ran.push(1); throw new Error('x');}, () => {ran.push(2);}]);
  assert.deepEqual(ran, [0, 1, 2]);
  assert.deepEqual(logged, ['routes[1]']);
});

test('the original error survives a failing release', async () => {
  const logged = capture(), original = new Error('the real failure');
  await assert.rejects(releaseThenRethrow(original, 'retire', () => {throw new Error('retire failed');}), error => error === original);
  assert.deepEqual(logged, ['retire']);
});

test('a failing log does not throw either', () => {
  setCleanupLog(() => {throw new Error('log down');});
  assert.doesNotThrow(() => releaseQuietly('x', () => {throw new Error('x');}));
  setCleanupLog(() => {});
});
