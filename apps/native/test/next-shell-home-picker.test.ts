import assert from 'node:assert/strict';
import test from 'node:test';
import type {HomeDestinationReceipt} from '../../../packages/client-core/src/home-selection/destination-receipt';
import {nativePickerCounts} from '../src-next/shell/selection-picker';

const receipt: HomeDestinationReceipt = {kind: 'playlist', destinationId: 'destination-a', jobIds: ['job-a', 'job-b'],
  jobs: [{jobId: 'job-a', command: 'playlist-add', state: 'complete', total: 5, totalKnown: true, done: 5, failed: 0},
    {jobId: 'job-b', command: 'playlist-add', state: 'partial', total: 4, totalKnown: true, done: 4, failed: 1}],
  added: 2, alreadyPresent: 3, failed: 1, unknown: 0, complete: true, pending: false, unresolvedJobIds: [], failures: [{itemId: 'item-a', code: 'not_permitted'}],
  failureEntries: [{id: 'item-a', libraryId: 'library-a', kind: 'movie', title: 'item-a'}], nextCursor: ''};

test('picker reports authoritative distinct writes across jobs and retries, never progress as added', () => {
  assert.deepEqual(nativePickerCounts({phase: 'partial', receipt}), {inserted: 2, alreadyIn: 3, processed: 6, total: 6, complete: true});
});

test('unresolved outcomes omit a full total and cannot produce a completed success', () => {
  const unresolved = {...receipt, unknown: 4, complete: false, unresolvedJobIds: ['job-b']};
  assert.deepEqual(nativePickerCounts({phase: 'error', receipt: unresolved}), {inserted: 2, alreadyIn: 3, processed: 6, total: undefined, complete: false});
  assert.equal(nativePickerCounts({phase: 'adding', receipt})?.complete, false);
  assert.equal(nativePickerCounts({phase: 'ready', receipt: null}), undefined);
});
