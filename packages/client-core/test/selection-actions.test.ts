import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSelectionActionPreparation, parseSelectionActionJob, selectionActionInput,
  prepareSelectionAction, getSelectionActionPreparation, createSelectionAction, getSelectionAction, cancelSelectionAction,
  releaseSelectionActionPreparation, type SelectionActionInput, type SelectionActionsApi} from '../src/selection-actions.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

const basis = 'a'.repeat(64);
const input: SelectionActionInput = {action: 'delete', selector: {query: {libraryId: 'library', pivot: 'movies', selectionBasis: basis,
  filter: {variant:'predicate',field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]}},
  expected: {catalogRevision: 'browse_' + 'b'.repeat(64)}, options: {deleteFiles: true}};
const ready = {kind: 'selection-action-preview', preparationId: 'prep', action: 'delete', basis, total: 48_000,
  files: 48_000, bytes: 100_000, bytesKnown: true, confirmation: '48000', confirmationKind: 'count',
  expiresAt: '2030-01-01T00:00:00Z', sample: [{itemId: 'item', title: 'Film', kind: 'movie'}]};
const job = {operationId: 'operation', action: 'delete', state: 'capturing', total: 48_000,
  admitted: 20, done: 0, failed: 0, bytesDeleted: 0, filesDeleted: 0, retryAfterMs: 1000};

test('selection action input keeps the exact leaf query and freezes independent options/filter/sort', () => {
  const raw = structuredClone(input), result = selectionActionInput(raw);
  assert.deepEqual(result, input);
  (raw.selector.query.filter as {value: number}).value = 1990;
  assert.equal((result.selector.query.filter as {value: number}).value, 2000);
  assert.ok(Object.isFrozen(result.selector.query.filter));
  assert.ok(Object.isFrozen(result.selector.query.sort?.[0]));
  for (const pivot of ['movies', 'episodes', 'songs', 'parts']) assert.equal(selectionActionInput({...input, selector: {query: {...input.selector.query, pivot}}}).selector.query.pivot, pivot);
});
test('selection action input rejects unsupported pivots, absent basis, refinement/options keys and unknown filters', () => {
  for (const raw of [
    {...input, selector: {query: {...input.selector.query, pivot: 'movie'}}},
    {...input, selector: {query: {...input.selector.query, pivot: 'audiobook_files'}}},
    {...input, expected: {catalogRevision: 'browse_revision'}},
    {...input, selector: {query: {...input.selector.query, libraryId: '../other'}}},
    {...input, selector: {query: {...input.selector.query, selectionBasis: undefined}}},
    {...input, selector: {...input.selector, exclude: ['extra']}},
    {...input, selector: {query: {...input.selector.query, excludedItems: []}}},
    {...input, options: {...input.options, deviceId: 'device'}},
    {...input, selector: {query: {...input.selector.query, filter: {variant:'group',all: [], ignored: true}}}},
    {...input, selector: {query: {...input.selector.query, sort: [{field: 'title', direction: 'asc', seed: 2}]}}},
  ]) assert.throws(() => selectionActionInput(raw));
});
test('selection action parsers accept only bounded previews and truthful aggregate jobs', () => {
  assert.deepEqual(parseSelectionActionPreparation(ready), ready);
  assert.deepEqual(parseSelectionActionJob(job), job);
  assert.deepEqual(parseSelectionActionPreparation({kind: 'selection-action-preparing', preparationId: 'prep', retryAfterMs: 0}),
    {kind: 'selection-action-preparing', preparationId: 'prep', retryAfterMs: 0});
  for (const patch of [{total: 0}, {total: Number.MAX_SAFE_INTEGER + 1}, {bytesKnown: false}, {confirmation: '48,000'}, {confirmationKind: 'title'},
    {expiresAt: 'invalid'}, {sample: Array(21).fill(ready.sample[0])}, {sample: [ready.sample[0], ready.sample[0]]}]) {
    assert.throws(() => parseSelectionActionPreparation({...ready, ...patch}));
  }
  for (const patch of [{done: 1}, {state: 'complete'}, {admitted: 48_001}, {done: 48_000, failed: 1}, {bytesDeleted: 1},
    {state: 'running'}, {retryAfterMs: -1}]) assert.throws(() => parseSelectionActionJob({...job, ...patch}));
  assert.equal(parseSelectionActionJob({...job, state: 'partial', done: 1, failed: 1, errorCode: 'interrupted'}).state, 'partial');
  const cancelledPartial = parseSelectionActionJob({...job, state: 'partial', done: 0, failed: 1, filesDeleted: 1, bytesDeleted: 5000, errorCode: 'cancelled'});
  assert.equal(cancelledPartial.done, 0); assert.equal(cancelledPartial.filesDeleted, 1); assert.equal(cancelledPartial.bytesDeleted, 5000);
});

test('selection action transports use registered routes and real parser runtime without member requests', async () => {
  const requests: {path: string; method: string | undefined; body: unknown; signal?: AbortSignal}[] = [];
  let response: unknown = ready;
  const api: SelectionActionsApi = {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) {
    requests.push({path, method, body, signal}); return response as T;
  }};
  const signal = new AbortController().signal;
  await prepareSelectionAction(api, input, signal);
  assert.deepEqual(requests[0], {path: routePath('create_selection_action_preparation', {id: 'library'}), method: 'POST', body: input, signal});
  await getSelectionActionPreparation(api, 'library', 'prep', signal);
  response = {...ready, preparationId: 'foreign'};
  await assert.rejects(getSelectionActionPreparation(api, 'library', 'prep', signal));
  response = job;
  const body = {operationId: 'operation', preparationId: 'prep', confirmation: '48000'};
  await createSelectionAction(api, 'library', body, signal);
  await getSelectionAction(api, 'operation', signal);
  await cancelSelectionAction(api, 'operation', signal);
  response = undefined;
  await releaseSelectionActionPreparation(api, 'library', 'prep', signal);
  assert.equal(requests.length, 7);
  assert.equal(requests.filter(r => r.method === 'DELETE').length, 2);
  assert.deepEqual(requests[3].body, body);
  assert.ok(requests.every(r => r.signal === signal));
});
