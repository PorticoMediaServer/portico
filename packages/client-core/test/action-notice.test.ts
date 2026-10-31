import test from 'node:test';
import assert from 'node:assert/strict';
import {actionNotice, type UserActionSnapshot} from '../src/user-action.ts';
import {enUS} from '../../i18n/src/index.ts';

test('the inline failure shows only for failed and unconfirmed actions', () => {
  for (const snapshot of [{phase: 'idle'}, {phase: 'working', slow: true, attempt: 1}, {phase: 'done', value: 1}] as UserActionSnapshot<number>[])
    assert.equal(actionNotice(snapshot), undefined, snapshot.phase);
  assert.deepEqual(actionNotice({phase: 'unconfirmed', attempt: 2}), {message: 'next.states.actionUnconfirmed', retryable: true, retryAfterMs: 0});
});

test('a failure uses its own line, else the generic one, and holds Try again for Retry-After', () => {
  assert.deepEqual(actionNotice({phase: 'failed', attempt: 1, failure: {retryable: true, retryAfterMs: 4000, cause: null}}), {message: 'next.states.actionFailed', retryable: true, retryAfterMs: 4000});
  assert.deepEqual(actionNotice({phase: 'failed', attempt: 1, failure: {message: 'next.states.offline', retryable: true, cause: null}}), {message: 'next.states.offline', retryable: true, retryAfterMs: 0});
  assert.deepEqual(actionNotice({phase: 'failed', attempt: 1, failure: {retryable: false, retryAfterMs: 4000, cause: null}}), {message: 'next.states.actionFailed', retryable: false, retryAfterMs: 0}, 'no wait without Try again');
});

test('the generic lines exist and say little', () => {
  for (const key of ['next.states.actionFailed', 'next.states.actionUnconfirmed', 'next.states.tryAgain'] as const) {
    assert.ok(enUS[key], key);
    assert.ok(enUS[key].length <= 30, key);
  }
});
