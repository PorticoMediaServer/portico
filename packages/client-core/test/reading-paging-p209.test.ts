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
const seriesCursor = 'a'.repeat(171) + '.' + 'b'.repeat(43);

test('Discover rows carry their own continuation and continue alone', async () => {
  const {calls, reading} = client(url => url.searchParams.has('row')
    ? {rows: [{key: 'recent', items: [{...item, id: 'second'}]}]}
    : {rows: [{key: 'recent', items: [item], nextCursor: '1700000000000.42'}]});
  const first = await reading.discover({libraryId: 'library', limit: 1});
  assert.equal(first.rows[0].nextCursor, '1700000000000.42');
  const next = await reading.discoverRow('recent', {libraryId: 'library', cursor: first.rows[0].nextCursor, limit: 1});
  assert.equal(calls[1].search, '?libraryId=library&cursor=1700000000000.42&limit=1&row=recent');
  assert.equal(next.row.items[0].id, 'second');
  assert.equal(next.row.nextCursor, undefined);
});

test('A short page with a cursor is ordinary; an empty page with a cursor is not', async () => {
  const {reading} = client(() => ({rows: [{key: 'series', items: [], series: [{id: 's'.repeat(43), libraryId: 'library', name: 'Saga', count: 2, readCount: 0,
    preview: [{...item, series: {id: 's'.repeat(43), name: 'Saga', source: 'owner'}}]}], nextCursor: seriesCursor}]}));
  const page = await reading.discoverRow('series', {libraryId: 'library', limit: 20});
  assert.equal(page.row.series?.length, 1);
  for (const rows of [
    [{key: 'recent', items: [], nextCursor: '1.2'}],
    [{key: 'recent', items: [item], nextCursor: 'not a cursor'}],
  ]) {
    const {reading} = client(() => ({rows}));
    await assert.rejects(reading.discoverRow(rows[0].key as 'recent', {libraryId: 'library'}), coded('invalid_document'));
  }
});

test('Discover rows refuse bad requests before IO', async () => {
  const {calls, reading} = client(() => ({rows: []}));
  await assert.rejects(reading.discoverRow('newspaper' as 'recent', {libraryId: 'library'}), coded('invalid_request'));
  await assert.rejects(reading.discoverRow('series', {}), coded('invalid_request'));
  await assert.rejects(reading.discoverRow('series', {libraryId: 'library', limit: 21}), coded('invalid_request'));
  await assert.rejects(reading.discoverRow('recent', {cursor: '../x'}), coded('invalid_request'));
  assert.equal(calls.length, 0);
});

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

test('Versions carry the server choice, which must be a listed version', async () => {
  const versions = [{sourceId: 'one', format: 'pdf', size: 1, inventoryAvailable: true}, {sourceId: 'two', format: 'epub', size: 1, inventoryAvailable: true}];
  assert.equal((await client(() => ({chosenSourceId: 'two', versions})).reading.versions('book')).chosenSourceId, 'two');
  await assert.rejects(client(() => ({chosenSourceId: 'three', versions})).reading.versions('book'), coded('invalid_document'));
  await assert.rejects(client(() => ({versions})).reading.versions('book'), coded('invalid_document'));
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
