import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSession} from '../src/browse-session.ts';
import {createJob} from '../src/bulk-jobs.ts';

const predicate = {variant: 'predicate' as const, field: 'genre', operator: 'equals', value: 'Drama'};
const query = {libraryId: 'lib', pivot: 'movies', capabilities: {library: {kind: 'movie'}} as never, nodes: [predicate], sorts: [{field: 'title', direction: 'asc' as const}]};
const controller = () => new AbortController();

test('a whole selection carries the applied filter and exact count, not only the loaded page', async () => {
  const session = new BrowseSession({query, current: () => true, item: entry => entry,
    request: async path => new URL(path, 'http://test').searchParams.has('total') ? {items: [], total: 5000} : {items: [{id: 'one', kind: 'movie', title: 'One'}], nextCursor: 'next'}});
  await session.fetchPage(0, 60, controller().signal);
  const whole = session.wholeSelection()!;
  assert.equal(whole.total, 5000); assert.equal(whole.catalogRevision, session.revision);
  assert.deepEqual(whole.filter, predicate); assert.deepEqual(whole.sort, query.sorts);
  assert.equal(whole.types[0].capabilities.play, 5000);
  assert.equal(whole.types[0].capabilities.delete, 0); assert.equal(whole.types[0].capabilities.download, 0);
  // The whole view offers exactly what each title's own read offers, or the selection model refuses every window
  // (a library's Select did nothing on a phone, 22 Nov): favorite is offered per title, refresh is not.
  assert.equal(whole.types[0].capabilities.favorite, 5000);
  assert.equal(whole.types[0].capabilities.refresh, 0);
  session.retire(); assert.equal(session.wholeSelection(), undefined);
});

test('a late count cannot publish a whole selection after the session is retired', async () => {
  let finish!: (value: unknown) => void; let published = 0;
  const session = new BrowseSession({query, current: () => true, item: entry => entry, onWhole: () => published++,
    request: async path => new URL(path, 'http://test').searchParams.has('total') ? new Promise(resolve => {finish = resolve;}) : {items: [{id: 'one', kind: 'movie', title: 'One'}], nextCursor: 'next'}});
  await session.fetchPage(0, 60, controller().signal); session.retire(); finish({items: [], total: 5000});
  await Promise.resolve(); await Promise.resolve(); assert.equal(session.wholeSelection(), undefined); assert.equal(published, 0);
});

test('a whole show, album, artist or audiobook view offers playback of all its matches', async () => {
  for (const [pivot, libraryKind, wireKind, kind] of [['shows', 'tv', 'show', 'show'], ['albums', 'music', 'album', 'album'], ['artists', 'music', 'artist', 'artist'], ['books', 'audiobook', 'audiobook', 'book']] as const) {
    const session = new BrowseSession({query: {...query, pivot, capabilities: {library: {kind: libraryKind}} as never}, current: () => true, item: entry => entry,
      request: async path => new URL(path, 'http://test').searchParams.has('total') ? {items: [], total: 5000} : {items: [{id: 'one', kind: wireKind, title: 'One'}], nextCursor: 'next'}});
    await session.fetchPage(0, 60, controller().signal);
    const whole = session.wholeSelection()!;
    assert.equal(whole.types[0].kind, kind); assert.equal(whole.types[0].capabilities.play, 5000); assert.equal(whole.types[0].capabilities.queue, 5000);
    session.retire();
  }
});

test('whole-view metadata jobs carry the complete filter and only edited fields', async () => {
  const sent: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    assert.equal(path, '/v1/jobs'); assert.equal(method, 'POST'); sent.push(body);
    return {id: 'job', kind: (body as {kind: string}).kind, state: 'done', progress: {done: 5000, total: 5000}} as T;
  }};
  const selector = {query: {libraryId: 'lib', pivot: 'movies', filter: predicate}};
  await createJob(api, {operationId: 'edit', command: 'metadata-edit', selector, args: {fields: {year: {value: '1984'}}, lists: {tags: {add: ['Noir'], remove: ['Old']}}, lockEdited: true}});
  await createJob(api, {operationId: 'refresh', command: 'refresh', selector, args: {}});
  assert.deepEqual(sent, [{kind: 'edit_items', target: {filter: 'library=lib&kind=movie&genre=Drama'}, params: {year: 1984, add: {labels: ['Noir']}, remove: {labels: ['Old']}, lock: true}},
    {kind: 'refresh_items', target: {filter: 'library=lib&kind=movie&genre=Drama'}}]);
});
