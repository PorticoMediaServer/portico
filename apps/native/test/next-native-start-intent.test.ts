import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeStartSeconds} from '../src-next/platform/playback/start-intent.ts';

test('ordinary Play and Resume omit retained card positions so the new server start owns resume', () => {
  const target = Object.freeze({itemId: 'episode', startSeconds: 17});
  assert.equal(nativeStartSeconds({target}), undefined);
  assert.equal(nativeStartSeconds({target, startIntent: 'resume'}), undefined);
  assert.equal(nativeStartSeconds({target: {...target, startSeconds: 450}, startIntent: 'resume'}), undefined);
  assert.equal(target.startSeconds, 17, 'display facts are not mutated');
});

test('Start over and explicit chapters or handoffs preserve their deliberate positions', () => {
  assert.equal(nativeStartSeconds({target: {itemId: 'episode', startSeconds: 450}, startIntent: 'start-over'}), 0);
  for (const seconds of [0, 17, 240.5]) assert.equal(nativeStartSeconds({target: {itemId: 'episode', startSeconds: seconds}, startIntent: 'explicit'}), seconds);
});

test('explicit starts require a finite bounded non-negative position', () => {
  for (const startSeconds of [undefined, NaN, Infinity, -1, 1e9 + 1]) {
    assert.throws(() => nativeStartSeconds({target: {itemId: 'episode', startSeconds}, startIntent: 'explicit'}), /Invalid playback target/);
  }
});
