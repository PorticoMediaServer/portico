import assert from 'node:assert/strict';
import test from 'node:test';
import {parseHomeDestinationReceipt} from '../src/home-selection/destination-receipt.ts';

test('picker receipt preserves distinct durable counters instead of counting processed encounters', () => {
  const request = {kind: 'playlist' as const, destinationId: 'destination', jobIds: ['show-job', 'leaf-job']};
  const jobs = request.jobIds.map((jobId, index) => ({jobId, command: 'playlist-add', state: 'complete', total: index === 0 ? 3 : 2, totalKnown: true, done: index === 0 ? 3 : 2, failed: 0}));
  const raw = {serverId: 'server', viewerFence: 'fence', ...request, jobs, added: 2, alreadyPresent: 1, failed: 0, unknown: 0, complete: true, pending: false, unresolvedJobIds: [], failures: [], failureEntries: [], nextCursor: ''};
  const receipt = parseHomeDestinationReceipt(raw, request, 'server', 'fence');
  assert.equal(receipt.added, 2); assert.equal(receipt.alreadyPresent, 1); assert.equal(receipt.jobs.reduce((sum, job) => sum + job.done, 0), 5);
  assert.throws(() => parseHomeDestinationReceipt({...raw, jobIds: ['another-job']}, request, 'server', 'fence'), /invalid-destination-receipt/);
});

test('failure display requires the genuine authorized expanded leaf and never substitutes a selected parent', () => {
  const request = {kind: 'collection' as const, destinationId: 'destination', jobIds: ['album-job']};
  const raw = {serverId: 'server', viewerFence: 'fence', ...request, jobs: [{jobId: 'album-job', command: 'collection-add', state: 'partial', total: 2, totalKnown: true, done: 1, failed: 1}], added: 1, alreadyPresent: 0, failed: 1, unknown: 0, complete: true, pending: false, unresolvedJobIds: [], failures: [{itemId: 'song', code: 'not_found'}], failureEntries: [{id: 'song', kind: 'song', title: 'Actual failed song', libraryId: 'music'}], nextCursor: ''};
  const receipt = parseHomeDestinationReceipt(raw, request, 'server', 'fence');
  assert.equal(receipt.failureEntries[0].title, 'Actual failed song');
  assert.throws(() => parseHomeDestinationReceipt({...raw, failureEntries: [{id: 'album', kind: 'album', title: 'Selected album'}]}, request, 'server', 'fence'), /invalid-destination-receipt/);
  assert.throws(() => parseHomeDestinationReceipt({...raw, unknown: 1}, request, 'server', 'fence'), /invalid-destination-receipt/);
});
