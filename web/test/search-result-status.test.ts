import test from 'node:test';
import assert from 'node:assert/strict';
import {searchResultCount, scheduleSearchResultStatus} from '../src/screens/search/result-status.ts';
import type {SearchGroupResult} from '@core/search.ts';

const group = (count: number, exact = true, status = 'success') => ({totalCount: count, totalCountExact: exact, status}) as SearchGroupResult;
test('completion counts use exact settled groups and never fabricate totals from failed or partial groups', () => {
  assert.equal(searchResultCount([group(4), group(2)]), 6);
  assert.equal(searchResultCount([group(0)]), 0);
  for (const groups of [[], [group(4, false)], [group(4), group(0, true, 'error')]]) assert.equal(searchResultCount(groups), undefined);
});
test('rapid query changes announce only the last count after 300ms and cancel on unmount', () => {
  const callbacks: (() => void)[] = [], published: string[] = [], cancelled: unknown[] = [];
  const schedule = (callback: () => void, ms: number) => {assert.equal(ms, 300);callbacks.push(callback);return callbacks.length;};
  const cancel = (id: unknown) => {cancelled.push(id);};
  const first = scheduleSearchResultStatus('4 results', message => published.push(message), schedule, cancel);
  first();
  const second = scheduleSearchResultStatus('6 results', message => published.push(message), schedule, cancel);
  callbacks[0]();assert.deepEqual(published, []);
  callbacks[1]();assert.deepEqual(published, ['6 results']);
  second();callbacks[1]();assert.deepEqual(published, ['6 results']);
  assert.deepEqual(cancelled, [1, 2]);
});
