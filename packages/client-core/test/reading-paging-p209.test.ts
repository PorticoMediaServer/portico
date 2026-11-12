import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, parseReadingOfflineRefresh, readingOfflineRefreshPath, type Viewer} from '../src/index.ts';

const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const item = {id: 'book', libraryId: 'library', title: 'Stored title', sourceId: 'asset', format: 'pdf', inventoryAvailable: true};
const response = (value: unknown) => new Response(JSON.stringify(value), {headers: {'Content-Type': 'application/json'}});
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;
const client = (answer: (url: URL) => unknown) => {
  const calls: URL[] = [];
  const api = new HttpLocalApi('https://server.example', 'token', async input => {const url = new URL(String(input)); calls.push(url); return response(answer(url));});
  return {calls, reading: api.reading({viewer, isCurrent: () => true})};
};
const issue = (date: string, id: string) => ({issueDate: date, item: {...item, id, format: 'epub', publicationType: {value: 'magazine', source: 'owner'}, series: {id: 'g'.repeat(43), name: 'Monthly', source: 'owner'}}});

test('Periodical issues ask for oldest first and check that order', async () => {
  const page = (items: unknown[]) => ({libraryId: 'library', seriesId: 'g'.repeat(43), publicationType: 'magazine', order: 'oldest', items, years: [], days: []});
  const {calls, reading} = client(() => page([issue('1930-01', 'a'), issue('1930-02', 'b')]));
  const out = await reading.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'oldest'});
  assert.equal(calls[0].searchParams.get('order'), 'oldest');
  assert.deepEqual(out.items.map(entry => entry.item.id), ['a', 'b']);
  const reversed = client(() => page([issue('1930-02', 'b'), issue('1930-01', 'a')])).reading;
  await assert.rejects(reversed.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'oldest'}), coded('invalid_document'));
  await assert.rejects(reading.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'sideways' as 'oldest'}), coded('invalid_request'));
});

test('Offline refresh results match the receipts sent, in order', () => {
  assert.equal(readingOfflineRefreshPath, '/v1/reading/offline/progress/read');
  const revision = 'a'.repeat(64);
  const sent = [{receiptId: 'r1', itemId: 'book', sourceId: 'asset', sourceRevision: revision}, {receiptId: 'r2', itemId: 'other', sourceId: 'asset2', sourceRevision: revision}];
  const progress = {sourceId: 'asset', sourceRevision: revision, location: {format: 'pdf', pageIndex: 3, progression: .5}, revision: 2, observedAt: 10, stale: false};
  const parsed = parseReadingOfflineRefresh({results: [
    {receiptId: 'r1', outcome: 'current', itemId: 'book', sourceId: 'asset', sourceRevision: revision, progress},
    {receiptId: 'r2', outcome: 'refused', code: 'source_unavailable'},
  ]}, sent);
  assert.equal(parsed[0].outcome, 'current');
  assert.equal(parsed[1].outcome === 'refused' && parsed[1].code, 'source_unavailable');
  for (const results of [
    [{receiptId: 'r1', outcome: 'current', itemId: 'book', sourceId: 'asset', sourceRevision: revision, progress: null}],
    [{receiptId: 'r2', outcome: 'current', itemId: 'book', sourceId: 'asset', sourceRevision: revision, progress: null}, {receiptId: 'r1', outcome: 'refused', code: 'receipt_rejected'}],
    [{receiptId: 'r1', outcome: 'current', itemId: 'book', sourceId: 'asset', sourceRevision: revision, progress: {...progress, stale: true}}, {receiptId: 'r2', outcome: 'refused', code: 'receipt_rejected'}],
  ]) assert.throws(() => parseReadingOfflineRefresh({results}, sent));
});
