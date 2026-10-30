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

const completeJob = {jobId: 'job-1', command: 'personal-state', state: 'complete', total: 1, totalKnown: true, done: 1, failed: 0};
const ports = {events: {on: () => () => {}}, isCurrent: () => true, reportError: () => {}};

test('concurrent planning shares one submission and uncertain retry preserves its body', async () => {
  const bodies: unknown[] = [];
  let ids = 0;
  const actions = createSelectionActions({...ports, requestId: async () => `op-${++ids}`, api: {request: async (_path, _method, body) => {
    bodies.push(body);
    if (bodies.length === 1) throw new Error('lost response');
    return completeJob;
  }}});
  const operation = {target: explicit(['movie'])} as never;
  const first = await Promise.allSettled([actions.personal(operation, {watched: true}), actions.personal(operation, {watched: true})]);
  assert.deepEqual(first.map(result => result.status), ['rejected', 'rejected']);
  assert.equal(bodies.length, 1);
  assert.equal((await actions.personal(operation, {watched: true})).updated, 1);
  assert.deepEqual(bodies[1], bodies[0]);
  assert.equal(ids, 1);
});

test('known job resumes with GET and a retired operation performs zero retry I/O', async () => {
  const calls: string[] = [];
  let current = true;
  const actions = createSelectionActions({...ports, requestId: async () => 'op', api: {request: async (_path, method = 'GET') => {
    calls.push(method);
    if (calls.length === 1) return {...completeJob, state: 'running', done: 0};
    if (calls.length === 2) throw new Error('poll lost');
    return completeJob;
  }}});
  const operation = {target: explicit(['movie'])} as never;
  await assert.rejects(actions.personal(operation, {favorite: true}, undefined, () => current), /poll lost/);
  current = false;
  await assert.rejects(actions.personal(operation, {favorite: true}, undefined, () => current), /cancelled/);
  assert.deepEqual(calls, ['POST', 'GET']);
  current = true;
  await actions.personal(operation, {favorite: true}, undefined, () => current);
  assert.deepEqual(calls, ['POST', 'GET', 'GET']);
});


test('a new caller guard cannot revive a retired retained attempt', async () => {
  let originalCurrent = true, requests = 0;
  const actions = createSelectionActions({...ports, requestId: async () => 'op', api: {request: async () => {requests++; throw new Error('lost');}}});
  const operation = {target: explicit(['movie'])} as never;
  await assert.rejects(actions.personal(operation, {watched: true}, undefined, () => originalCurrent), /lost/);
  originalCurrent = false;
  await assert.rejects(actions.personal(operation, {watched: true}, undefined, () => true), /cancelled/);
  assert.equal(requests, 1);
});

test('a granted displayed parent plans its own container selector, never a child', async () => {
  const requests: unknown[] = [];
  const actions = createSelectionActions({...ports, requestId: async () => 'parent-operation', api: {request: async (_path, _method, body) => {requests.push(body); return completeJob;}}});
  await actions.personal({target: explicit(['show'])} as never, {watched: true});
  assert.deepEqual((requests[0] as {selector: unknown}).selector, {container: {kind: 'show', id: 'id0'}});
});
