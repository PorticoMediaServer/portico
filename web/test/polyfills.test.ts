import test from 'node:test';
import assert from 'node:assert/strict';
import {installAbortSignalPolyfills} from '../src/platform/polyfills.ts';

function bare() {
  return {} as typeof AbortSignal & {any?: (s: AbortSignal[]) => AbortSignal; timeout?: (ms: number) => AbortSignal};
}

test('AbortSignal.any aborts when any input aborts, with that reason', () => {
  const target = bare();
  installAbortSignalPolyfills(target);
  const a = new AbortController(), b = new AbortController();
  const combined = target.any!([a.signal, b.signal]);
  assert.equal(combined.aborted, false);
  b.abort('second');
  assert.equal(combined.aborted, true);
  assert.equal(combined.reason, 'second');
  a.abort('first');
  assert.equal(combined.reason, 'second');
});

test('AbortSignal.any is already aborted when an input is', () => {
  const target = bare();
  installAbortSignalPolyfills(target);
  const a = new AbortController();
  a.abort('gone');
  const combined = target.any!([a.signal, new AbortController().signal]);
  assert.equal(combined.aborted, true);
  assert.equal(combined.reason, 'gone');
});

test('AbortSignal.timeout aborts with a TimeoutError', async () => {
  const target = bare();
  installAbortSignalPolyfills(target);
  const signal = target.timeout!(5);
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(signal.aborted, true);
  assert.equal((signal.reason as DOMException).name, 'TimeoutError');
});

test('native implementations are left alone', () => {
  const any = () => new AbortController().signal, timeout = any;
  const target = Object.assign(bare(), {any, timeout});
  installAbortSignalPolyfills(target);
  assert.equal(target.any, any);
  assert.equal(target.timeout, timeout);
});
