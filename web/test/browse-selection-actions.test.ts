import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionActions, personalTargetIsLeaf, personalTargetSupportsJob, SelectionActionError} from '../src/screens/browse/selection-actions.ts';

const item = (kind: string, id: string) => ({kind, id, libraryId: 'a', capabilities: {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: true, refresh: true, delete: true}});
const explicit = (kinds: readonly string[]) => ({mode: 'explicit', context: {}, items: kinds.map((kind, index) => ({key: `${kind}:${index}`, item: item(kind, `id${index}`)})), displayItems: []});
const query = {mode: 'query', context: {}, queryId: 'q', selector: {query: {libraryId: 'a', pivot: 'movies'}}, total: 10, sourceTotal: 10, refinement: {kind: null, excludedItems: []}, summary: {}, expected: {catalogRevision: 'r1'}};

test('leaf classification stays independent of canonical parent job support', () => {
  assert.equal(personalTargetIsLeaf(explicit(['movie', 'episode', 'song', 'audiobook_file'])), true);
  assert.equal(personalTargetIsLeaf(explicit(['movie', 'show'])), false);
  assert.equal(personalTargetIsLeaf(explicit(['show'])), false);
  assert.equal(personalTargetIsLeaf(explicit(['album', 'artist', 'book', 'collection'])), false);
  assert.equal(personalTargetIsLeaf(explicit([])), false);
  // A whole view is already restricted to leaf pivots by the server descriptor.
  assert.equal(personalTargetIsLeaf(query), true);
  assert.equal(personalTargetSupportsJob(explicit(['movie', 'show', 'album'])), true);
  assert.equal(personalTargetSupportsJob(explicit(['person'])), false);
});

test('one job command carries exactly one personal fact', async () => {
  const actions = createSelectionActions({api: {request: async () => { throw new Error('no request expected'); }}, events: {on: () => () => {}},
    requestId: async () => 'op', isCurrent: () => true, reportError: () => {}});
  const signal = new AbortController().signal;
  await assert.rejects(actions.personal({target: explicit(['person'])} as never, {watched: true}, signal), (error: unknown) => error instanceof SelectionActionError && error.code === 'selection-personal-state-unsupported');
  await assert.rejects(actions.personal({target: explicit(['movie'])} as never, {}, signal), (error: unknown) => error instanceof SelectionActionError && error.code === 'empty-command');
  await assert.rejects(actions.personal({target: explicit(['movie'])} as never, {watched: true, favorite: true}, signal), (error: unknown) => error instanceof SelectionActionError && error.code === 'mixed-personal-command');
  await assert.rejects(actions.personal({target: explicit(['movie'])} as never, {watched: true}, AbortSignal.abort()), (error: unknown) => error instanceof SelectionActionError && error.code === 'cancelled');
});

const ports = {events: {on: () => () => {}}, isCurrent: () => true, reportError: () => {}};

test('picked titles: two taps share one run, and a title the server did not take is counted as failed while the rest is done', async () => {
  const paths: string[] = [];
  const actions = createSelectionActions({...ports, requestId: async () => 'op-1', api: {request: async (path: string) => {
    paths.push(path);
    if (path === '/v1/items/id0/state') throw Object.assign(new Error('lost response'), {code: 'network'});
    return {};
  }} as never});
  const operation = {target: explicit(['movie', 'movie'])} as never;
  const [first, second] = await Promise.all([actions.personal(operation, {watched: true}), actions.personal(operation, {watched: true})]);
  assert.deepEqual([first.updated, first.failed], [1, 1]);
  assert.equal(second, first, 'the second tap is the same run');
  assert.deepEqual(paths, ['/v1/items/id0/state', '/v1/items/id1/state']);
});

test('a granted displayed parent is marked as itself, never a child', async () => {
  const paths: string[] = [];
  const actions = createSelectionActions({...ports, requestId: async () => 'parent-operation', api: {request: async (path: string) => {paths.push(path); return {};}} as never});
  await actions.personal({target: explicit(['show'])} as never, {watched: true});
  assert.deepEqual(paths, ['/v1/items/id0/state']);
});

test('J-14: My List already under way finishes when its own write ends the selection, and its outcome is said', async () => {
  let selectionLive = true;
  const notices: string[] = [], paths: string[] = [];
  const actions = createSelectionActions({...ports, requestId: async () => 'op-1', notify: message => notices.push(message), api: {request: async (path: string) => {
    paths.push(path); selectionLive = false; return {};
  }} as never});
  const result = await actions.personal({target: explicit(['episode', 'episode'])} as never, {watchlist: true}, undefined, () => selectionLive);
  assert.equal(result.updated, 2);
  assert.equal(paths.length, 2, 'the run already sent carries on to its end');
  assert.deepEqual(notices.length, 1);
  assert.match(notices[0]!, /My List/);
});
test('J-14: nothing is sent once the selection has ended', async () => {
  const sent: unknown[] = [];
  const actions = createSelectionActions({...ports, requestId: async () => 'op-1', api: {request: async (_path: string, _method?: string, body?: unknown) => {sent.push(body); return {};}} as never});
  await assert.rejects(actions.personal({target: explicit(['episode'])} as never, {watchlist: true}, undefined, () => false), (error: unknown) => error instanceof SelectionActionError && error.code === 'cancelled' || /cancel/.test(String(error)));
  assert.equal(sent.length, 0);
});
