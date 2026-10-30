import assert from 'node:assert/strict';
import test from 'node:test';
import {createTVMenuPolicy} from '../src-next/platform/tv-menu-policy';

test('Home yields Menu, but an overlay keeps Back even if the shell reaches its starting focus', () => {
  const applied: boolean[] = [], policy = createTVMenuPolicy(value => applied.push(value));
  const home = policy.acquire(false);
  const overlay = policy.acquire();
  home();
  const returnedHome = policy.acquire(false);
  assert.deepEqual(applied, [false, true, true]);
  overlay();
  assert.deepEqual(applied, [false, true, true, false]);
  returnedHome();
  assert.deepEqual(applied, [false, true, true, false]);
});

test('releasing a covered page or closing a nested sheet cannot release the current Back owner', () => {
  const applied: boolean[] = [], policy = createTVMenuPolicy(value => applied.push(value));
  const page = policy.acquire(), player = policy.acquire(), panel = policy.acquire();
  page(); panel(); panel();
  assert.deepEqual(applied, [true, true, true]);
  player();
  assert.deepEqual(applied, [true, true, true, false]);
});

test('a new shell reasserts capture after a legacy account recognizer is removed', () => {
  let nativeCapture = false;
  const policy = createTVMenuPolicy(value => {nativeCapture = value;});
  const overlay = policy.acquire();
  nativeCapture = false; // The outgoing account surface disables its native recognizer.
  const shell = policy.acquire();
  assert.equal(nativeCapture, true);
  overlay();
  assert.equal(nativeCapture, true);
  shell();
  assert.equal(nativeCapture, false);
});
