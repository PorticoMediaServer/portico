import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../src/index.ts';

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
  const page = (items: ReturnType<typeof issue>[]) => (url: URL) => url.pathname === '/v1/facets' ? {facet: 'issueYear', values: []}
    : url.pathname.startsWith('/v1/items/') ? {id: 'g'.repeat(43), title: 'Monthly'}
    : {items: items.map(entry => ({...entry.item, kind: 'book', releaseDate: entry.issueDate, images: {}, state: {watched: false}}))};
  const {calls, reading} = client(page([issue('1930-01', 'a'), issue('1930-02', 'b')]));
  const out = await reading.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'oldest'});
  assert.equal(calls[0].searchParams.get('order'), 'asc');
  assert.deepEqual(out.items.map(entry => entry.item.id), ['a', 'b']);
  const reversed = client(page([issue('1930-02', 'b'), issue('1930-01', 'a')])).reading;
  await assert.rejects(reversed.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'oldest'}), coded('invalid_document'));
  await assert.rejects(reading.periodicalIssues('library', 'g'.repeat(43), 'magazine', {order: 'sideways' as 'oldest'}), coded('invalid_request'));
});
