import test from 'node:test';
import assert from 'node:assert/strict';
import type {ReadingClient} from '@portico/client-core';
import {resolveItemRead} from '../src/screens/detail/read-resolution.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
function setup(kind: string) {
  const paths: string[] = [];
  const queries = testQueries({request: async <T,>(path: string) => {
    paths.push(path);
    if (new URL(path, 'https://unit.invalid').pathname === '/v1/items/title') return {id: 'title', kind, title: 'Title', libraryId: 'library', addedAt: '2026-01-01T00:00:00Z', images: {}, state: {}} as T;
    throw Object.assign(new Error('Not found'), {status: 404});
  }}, scope);
  const asked: string[] = [];
  const client = {versions: async (itemId: string) => { asked.push(itemId); return {chosenSourceId: 'v1', versions: [{sourceId: 'v1', format: 'epub', size: 1, inventoryAvailable: true}]}; }} as unknown as ReadingClient;
  return {queries, client, asked, paths};
}

test('a film opens its title page without asking how to read it', async () => {
  for (const kind of ['movie', 'episode', 'audiobook', 'track']) {
    const f = setup(kind);
    assert.equal(await resolveItemRead(f.client, f.queries, 'title', new AbortController().signal, () => true), null, kind);
    assert.deepEqual(f.asked, [], `${kind} is not asked for reading information`);
    assert.ok(!f.paths.some(path => path.includes('/book')));
  }
});

test('a book and a comic still ask the reader which version to open', async () => {
  for (const kind of ['book', 'comic', 'issue']) {
    const f = setup(kind);
    const read = await resolveItemRead(f.client, f.queries, 'title', new AbortController().signal, () => true);
    assert.equal(read?.chosenSourceId, 'v1', kind); assert.deepEqual(f.asked, ['title']);
  }
});

test('a title whose page cannot be read here is decided by the reading lookup, as before', async () => {
  const f = setup('movie');
  await resolveItemRead(f.client, f.queries, 'missing', new AbortController().signal, () => true);
  assert.deepEqual(f.asked, ['missing']);
});
