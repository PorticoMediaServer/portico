import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSelectionModel} from '../../packages/client-core/src/selection-model/index.ts';
import {wholeViewQuery} from '../src/screens/browse/selection-query.ts';
import {createQuerySelectionEditor} from '../src/screens/browse/selection-query-editor.ts';
import {browserQueryActionOffered} from '../src/screens/browse/selection-action-host.tsx';
import {resolveQueryDelete} from '../src/screens/browse/selection-delete-query.ts';
import {catalogueQuerySelectionSegments} from '../../packages/client-core/src/catalogue-query-selection.ts';
import {queueSource} from '../../packages/client-core/src/playback-v1/queue.ts';
import type {SelectionKind} from '../../packages/client-core/src/selection-model/types.ts';
import {readWholeMetadataPreview} from '../../packages/client-core/src/metadata-bulk-rebuilt.ts';
import {createJob} from '../../packages/client-core/src/bulk-jobs.ts';
import {createSelectionActions} from '../src/screens/browse/selection-actions.ts';
import {rebuiltMetadata} from '../../packages/client-core/test/fixtures/rebuilt-metadata.ts';

function held(kind: SelectionKind = 'movie', pivot = 'movies') {
  const revision = 'reading-whole';
  const capabilities = {play: 5000, queue: 5000, myList: 5000, watched: ['movie', 'show', 'episode'].includes(kind) ? 5000 : 0, favorite: 5000, playlist: 5000, collection: 5000, edit: 5000, refresh: 5000, delete: 0, download: 0};
  const query = wholeViewQuery({libraryId: 'lib', pivot, filter: {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'}, sort: [], catalogRevision: revision, total: 5000,
    types: [{kind, count: 5000, capabilities, libraries: [{libraryId: 'lib', count: 5000}]}]});
  const model = new BrowseSelectionModel({scope: {serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'view'}, revision: {pageRevision: revision}});
  assert.equal(model.registerQuery(model.captureContext(), query).ok, true); assert.equal(model.selectWholeQuery(query.id).ok, true);
  return {model, operation: model.captureOperation()!};
}

test('a whole-view editor opens unchanged fields, and saves the view without enumerating its titles', async () => {
  const selected = held(), fake = rebuiltMetadata({one: {fields: {year: 2000}}});
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const editor = createQuerySelectionEditor({api: {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    if (path.startsWith('/v1/items?')) return {items: [{id: 'one', libraryId: 'lib', kind: 'movie', title: 'One'}], nextCursor: 'more'} as T;
    if (path === '/v1/jobs') return {id: 'job', kind: 'edit_items', state: 'done', progress: {done: 5002, total: 5002}} as T;
    const value = fake.answer(path, method, body); if (value === undefined) throw new Error('unexpected ' + path); return value as T;
  }}, events: {on: () => () => {}}, requestId: async () => 'operation', isCurrent: () => true, serverId: 'server'});
  try {
    const preview = await editor.preview(selected.operation);
    assert.equal(preview.total, 5000); assert.deepEqual(preview.targets, []);
    assert.equal(preview.fields.year.value, undefined); assert.deepEqual(preview.fields.year.values, []);
    const result = await editor.save(selected.operation, preview, {fields: {year: '1984'}, lists: {tags: {add: ['Noir'], remove: ['Old']}}, lockEdited: true}, () => {});
    assert.equal(result.updated, 5002, 'the job reports the view at execution, not a stale count');
    const writes = calls.filter(call => call.method === 'POST');
    assert.deepEqual(writes, [{path: '/v1/jobs', method: 'POST', body: {kind: 'edit_items', target: {filter: 'library=lib&kind=movie&genre=Drama'}, params: {year: 1984, add: {labels: ['Noir']}, remove: {labels: ['Old']}, lock: true}}}]);
    assert.equal(calls.filter(call => call.path.startsWith('/v1/items?')).length, 1);
    assert.deepEqual(fake.reads, ['one']);
  } finally {selected.model.dispose();}
});

test('whole-view Delete and Download refuse before any preparation or enumeration', async () => {
  const selected = held(); let calls = 0;
  try {
    for (const action of ['delete', 'download'] as const) assert.equal(browserQueryActionOffered(selected.operation.target as never, action, {isCurrent: () => true} as never), false);
    await assert.rejects(resolveQueryDelete(selected.operation.target as never, {api: {request: async () => {calls++;}}} as never, new AbortController().signal), /query-delete-unavailable/);
    assert.equal(calls, 0);
  } finally {selected.model.dispose();}
});


test('Refresh of a whole selection submits one server job for the view, not its loaded cards', async () => {
  const selected = held(), sent: unknown[] = [];
  const actions = createSelectionActions({api: {request: async <T>(path: string, _method?: string, body?: unknown): Promise<T> => {
    assert.equal(path, '/v1/jobs'); sent.push(body);
    return {id: 'refresh', kind: 'refresh_items', state: 'done', progress: {done: 5000, total: 5000}} as T;
  }}, events: {on: () => () => {}}, requestId: async () => 'refresh', isCurrent: () => true, reportError: () => {}});
  try {
    assert.equal((await actions.refresh(selected.operation)).updated, 5000);
    assert.deepEqual(sent, [{kind: 'refresh_items', target: {filter: 'library=lib&kind=movie&genre=Drama'}}]);
  } finally {selected.model.dispose();}
});


test('Play keeps the complete held filter and reading in one queue source', () => {
  const selected = held();
  try {
    const sources = catalogueQuerySelectionSegments(selected.operation.target as never, 'play');
    assert.equal(sources.length, 1);
    assert.deepEqual(sources[0].source, {query: {libraryId: 'lib', pivot: 'movies', query: {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'}, selectionRevision: 'reading-whole'}});
  } finally {selected.model.dispose();}
});

test('whole container views keep one server kind and their full filter for Play and Add to queue', () => {
  for (const [kind, pivot, wireKind] of [['show', 'shows', 'show'], ['album', 'albums', 'album'], ['artist', 'artists', 'artist'], ['book', 'books', 'audiobook']] as const) {
    const selected = held(kind, pivot);
    try {
      for (const action of ['play', 'queue'] as const) {
        const segments = catalogueQuerySelectionSegments(selected.operation.target as never, action);
        assert.equal(segments.length, 1);
        assert.deepEqual(queueSource(segments[0].source), {view: {library: 'lib', kinds: [wireKind], filter: [{field: 'genre', values: ['Drama']}]}});
      }
      const target = selected.operation.target;
      if (target.mode !== 'query') throw new Error('Expected a whole view');
      if (kind !== 'book') assert.throws(() => catalogueQuerySelectionSegments({...target, summary: {...target.summary, types: [{kind, count: 2500}, {kind: 'movie', count: 2500}]}} as never, 'play'), /catalogue-query-selection-unavailable/);
    } finally {selected.model.dispose();}
  }
});

test('queue views retain progress and author conditions, and never silently drop an unsupported condition', () => {
  assert.deepEqual(queueSource({query: {libraryId: 'lib', pivot: 'books', query: {variant: 'group', all: [
    {variant: 'predicate', field: 'author', operator: 'contains-all', value: ['first', 'second']},
    {variant: 'predicate', field: 'playState', operator: 'equals', value: 'in-progress'},
  ]}, sort: [{field: 'title', direction: 'desc'}]}}), {view: {library: 'lib', kinds: ['audiobook'],
    filter: [{field: 'inProgress', values: ['true']}, {field: 'author', mode: 'all', values: ['first', 'second']}], sort: 'title', desc: true}});
  assert.throws(() => queueSource({query: {libraryId: 'lib', pivot: 'books', query: {variant: 'predicate', field: 'series', operator: 'equals', value: 'series'}}}), /unsupported_selector/);
});

test('an audiobook whole-view edit narrows the shared Books pivot to one server kind', async () => {
  const fake = rebuiltMetadata({audio: {kind: 'audiobook'}}), sent: any[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path.startsWith('/v1/items?')) {
      assert.equal(new URL(path, 'http://test').searchParams.get('kind'), 'audiobook');
      return {items: [{id: 'audio', kind: 'audiobook', libraryId: 'lib', title: 'Audio'}]} as T;
    }
    if (path === '/v1/libraries/lib') return {id: 'lib', kind: 'audiobooks'} as T;
    if (path === '/v1/jobs') {sent.push(body);return {id: 'edit', kind: 'edit_items', state: 'done', progress: {done: 5000, total: 5000}} as T;}
    return fake.answer(path, method, body) as T;
  }};
  const query = {libraryId: 'lib', pivot: 'books'};
  assert.equal((await readWholeMetadataPreview(api, query, 'server', 5000, 'book')).contentKind, 'book');
  await createJob(api, {operationId: 'edit', command: 'metadata-edit', selector: {query}, args: {fields: {year: {value: '1984'}}}});
  assert.deepEqual(sent, [{kind: 'edit_items', target: {filter: 'library=lib&kind=audiobook'}, params: {year: 1984, lock: true}}]);
});

test('whole-view Metadata language starts Unchanged and submits the canonical override for the view', async () => {
  const selected = held(), fake = rebuiltMetadata({one: {fields: {metadataLanguage: 'en'}}}), writes: unknown[] = [];
  const editor = createQuerySelectionEditor({api: {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path.startsWith('/v1/items?')) return {items: [{id: 'one', libraryId: 'lib', kind: 'movie', title: 'One'}]} as T;
    if (path === '/v1/admin/libraries/lib') return {metadataProvider: 'tmdb', metadataProviders: [{id: 'tmdb', languages: ['en', 'fr']}]} as T;
    if (path === '/v1/jobs') {writes.push(body);return {id: 'job', kind: 'edit_items', state: 'done', progress: {done: 5000, total: 5000}} as T;}
    const value = fake.answer(path, method, body); if (value === undefined) throw new Error('unexpected ' + path); return value as T;
  }}, events: {on: () => () => {}}, requestId: async () => 'language', isCurrent: () => true, serverId: 'server'});
  try {
    const preview = await editor.preview(selected.operation);
    assert.equal(preview.fields.metadataLanguage.value, undefined); assert.deepEqual(preview.fields.metadataLanguage.values, []);
    assert.deepEqual(preview.schema.find(spec => spec.field === 'metadataLanguage')?.allowed, ['', 'en', 'fr']);
    await editor.save(selected.operation, preview, {fields: {metadataLanguage: 'fr'}, lists: {}, lockEdited: false}, () => {});
    assert.deepEqual(writes, [{kind: 'edit_items', target: {filter: 'library=lib&kind=movie&genre=Drama'}, params: {metadataLanguage: 'fr', lock: false}}]);
    assert.deepEqual(fake.reads, ['one']);
  } finally {selected.model.dispose();}
});
