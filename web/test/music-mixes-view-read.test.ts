import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate as settle} from 'node:timers/promises';
import type {MusicMixesSnapshot} from '../src/screens/browse/music-mixes';
import {readMusicMixesForView} from '../src/screens/browse/music-mixes-view-read.ts';

// Authored UNRUN vectors for the production lifetime helper, with held source-port fixtures.
function binding() {
  let state: MusicMixesSnapshot = Object.freeze({phase: 'idle', rows: Object.freeze([])});
  let reject: (error: unknown) => void = () => {throw new Error('read not dispatched');};
  let reads = 0;
  return {getSnapshot: () => state, load: () => {
    reads++; state = Object.freeze({phase: 'loading', rows: Object.freeze([])});
    return new Promise<void>((_, fail) => {reject = fail;});
  }, fail: (error: unknown) => {
    if (state.phase !== 'retired') state = Object.freeze({phase: 'error', rows: Object.freeze([]), error});
    reject(error);
  }, rejectOnly: (error: unknown) => reject(error), ready: () => {state = Object.freeze({phase: 'ready', rows: Object.freeze([])});},
  retire: () => {state = Object.freeze({phase: 'retired', rows: Object.freeze([])});},
  get reads() {return reads;}};
}
test('a row retired before dispatch makes zero I/O', async () => {
  const source = binding(), errors: unknown[] = [];
  const release = readMusicMixesForView(source, error => errors.push(error)); release();
  await settle(); assert.equal(source.reads, 0); assert.deepEqual(errors, []);
});

test('Discover to Artists retirement cannot report its late rejected mixes read', async () => {
  const source = binding(), errors: unknown[] = [];
  const release = readMusicMixesForView(source, error => errors.push(error)); await settle();
  release(); source.fail(new Error('late read')); await settle();
  assert.equal(source.reads, 1); assert.deepEqual(errors, []);
});

test('an expected cancellation while the row is still mounted produces no shell error', async () => {
  const source = binding(), errors: unknown[] = [];
  const release = readMusicMixesForView(source, error => errors.push(error)); await settle();
  source.fail(Object.assign(new Error('cancelled'), {name: 'AbortError'})); await settle();
  assert.deepEqual(errors, []); release();
});

test('a retired binding suppresses settlement before the row cleanup runs', async () => {
  const source = binding(), errors: unknown[] = [];
  const release = readMusicMixesForView(source, error => errors.push(error)); await settle();
  source.retire(); source.fail(new Error('retired source')); await settle();
  assert.deepEqual(errors, []); release();
});

test('a genuine current-view failure keeps its original error and captured reporter', async () => {
  const source = binding(), errors: unknown[] = [], failure = new Error('real failure');
  const release = readMusicMixesForView(source, error => errors.push(error)); await settle();
  source.fail(failure); await settle(); assert.deepEqual(errors, [failure]); release();
});

test('a synchronous failure before the original idle state changes is still reported', async () => {
  const state: MusicMixesSnapshot = Object.freeze({phase: 'idle', rows: Object.freeze([])});
  const failure = new Error('synchronous failure'), errors: unknown[] = [];
  const release = readMusicMixesForView({getSnapshot: () => state, load: () => {throw failure;}}, error => errors.push(error));
  await settle(); assert.deepEqual(errors, [failure]); release();
});

test('a superseding ready result in the same binding cannot lend its state to an old failure', async () => {
  const source = binding(), errors: unknown[] = [];
  const release = readMusicMixesForView(source, error => errors.push(error)); await settle();
  source.ready(); source.rejectOnly(new Error('old flight')); await settle();
  assert.deepEqual(errors, []); release();
});

test('cleanup/replay and source replacement do not lend old errors to the successor row', async () => {
  const a = binding(), b = binding(), oldErrors: unknown[] = [], nextErrors: unknown[] = [];
  const old = readMusicMixesForView(a, error => oldErrors.push(error)); await settle(); old();
  const next = readMusicMixesForView(b, error => nextErrors.push(error)); await settle();
  a.fail(new Error('A')); const failure = new Error('B'); b.fail(failure); await settle();
  assert.deepEqual(oldErrors, []); assert.deepEqual(nextErrors, [failure]); next();
});
