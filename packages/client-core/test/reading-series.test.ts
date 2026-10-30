import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../src/index.ts';
import {routePath} from '../../contracts/src/index.ts';

const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const seriesId = 's'.repeat(43), cursor = 'a'.repeat(171) + '.' + 'b'.repeat(43);
const item = {id: 'book', libraryId: 'library', title: 'Stored title', sourceId: 'asset', format: 'epub', inventoryAvailable: false, series: {id: seriesId, name: 'Stored series', source: 'owner', number: 2.5}};
const json = (body: unknown) => new Response(JSON.stringify(body), {headers: {'Content-Type': 'application/json'}});
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;

test('Reading series uses the exact generated library route and retains the opaque signed cursor', async () => {
  let calls = 0;
  const api = new HttpLocalApi('https://server.example', 'captured', async (input, init) => {
    calls++;
    const url = new URL(String(input));
    assert.equal(url.pathname, routePath('get_reading_series_items', {libraryId: 'library', seriesId}));
    assert.equal(url.searchParams.get('cursor'), cursor);
    assert.equal(url.searchParams.get('limit'), '1');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer captured');
    return json({items: [item], nextCursor: cursor});
  });
  const client = api.reading({viewer, isCurrent: () => true});
  const page = await client.series('library', seriesId, {cursor, limit: 1});
  assert.equal(page.items[0].inventoryAvailable, false);
  assert.equal(page.items[0].title, 'Stored title');
  assert.equal(page.nextCursor, cursor);
  api.setAccessToken('replacement');
  await assert.rejects(client.series('library', seriesId), coded('cancelled'));
  assert.equal(calls, 1);
  client.dispose();
});

test('Reading series refuses foreign scope, unsigned continuations and duplicate items', async () => {
  for (const body of [
    {items: [{...item, libraryId: 'other'}]},
    {items: [{...item, series: {...item.series, id: 'x'.repeat(43)}}]},
    {items: [item, item]},
    {items: [item], nextCursor: '17'},
  ]) {
    const api = new HttpLocalApi('https://server.example', 'token', async () => json(body));
    const client = api.reading({viewer, isCurrent: () => true});
    await assert.rejects(client.series('library', seriesId), coded('invalid_document'));
    client.dispose();
  }
  let calls = 0;
  const api = new HttpLocalApi('https://server.example', 'token', async () => {calls++; return json({items: []});});
  const client = api.reading({viewer, isCurrent: () => true});
  await assert.rejects(client.series('library', seriesId, {cursor: '17'}), coded('invalid_request'));
  await assert.rejects(client.series('library', seriesId, {limit: 101}), coded('invalid_request'));
  assert.equal(calls, 0);
  client.dispose();
});
