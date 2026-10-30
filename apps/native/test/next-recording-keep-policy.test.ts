import test from 'node:test';
import assert from 'node:assert/strict';
import {keepPolicyFor} from '../src-next/screens/settings-live/recording-owner-model.ts';

test('choosing a limited keep mode from Keep all carries a count the server accepts', () => {
  const all = {mode: 'keep-all' as const, keepCount: 0, keepDays: 0, keepUntilWatched: false};
  assert.deepEqual(keepPolicyFor(all, 'keep-count'), {...all, mode: 'keep-count', keepCount: 5});
  assert.deepEqual(keepPolicyFor(all, 'keep-days'), {...all, mode: 'keep-days', keepDays: 30});
  const kept = {mode: 'keep-count' as const, keepCount: 3, keepDays: 14, keepUntilWatched: true};
  assert.deepEqual(keepPolicyFor(kept, 'keep-days'), {...kept, mode: 'keep-days'}, 'an existing value is never replaced');
});
