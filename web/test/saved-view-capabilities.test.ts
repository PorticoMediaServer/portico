import test from 'node:test';
import assert from 'node:assert/strict';
import type {SavedDefinition} from '@core/personal-saved.ts';
import {createSavedViewCapabilitiesReader} from '../src/screens/saved/view-capabilities.ts';

const definition = (libraryId = 'lib'): SavedDefinition => ({libraryId, pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'});
const wire = (libraryId = 'lib') => ({library: {id: libraryId, name: 'Movies', kind: 'movie', defaultView: 'browse'},
  pivots: [{id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true}],
  fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 10000}});
function deferred<T>() {let done: (value: T) => void = () => {};const promise = new Promise<T>(resolve => {done = resolve;});return {promise, resolve: (value: T) => done(value)};}
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

test('constructor is inert; matching reads coalesce and one caller abort does not cancel another', async () => {
  let requests = 0, transport: AbortSignal | undefined;
  const pending = deferred<unknown>();
  const reader = createSavedViewCapabilitiesReader({request: async<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    requests++;transport = signal;assert.equal(method, 'GET');assert.equal(body, undefined);assert.equal(new URL(path, 'http://test').searchParams.get('pivot'), 'movies');
    return (requests === 1 ? await pending.promise : wire()) as T;
  }}, () => true);
  try {
    assert.equal(requests, 0);
    const one = new AbortController(), two = new AbortController();
    const first = reader.read(definition(), one.signal), second = reader.read(definition(), two.signal);
    await tick();assert.equal(requests, 1);
    const cancelled = assert.rejects(first, {name: 'AbortError'});one.abort();await cancelled;
    assert.equal(transport?.aborted, false);
    pending.resolve(wire());const result = await second;assert.equal(result.library.id, 'lib');assert(Object.isFrozen(result));
    await reader.read(definition(), new AbortController().signal);assert.equal(requests, 2, 'later renders reread current capabilities rather than retaining a permanent cache');
  } finally {pending.resolve(wire());reader.dispose();}
});

test('last caller cancellation aborts actual transport and rejects before a late response', async () => {
  const pending = deferred<unknown>();let transport: AbortSignal | undefined;
  const reader = createSavedViewCapabilitiesReader({request: async<T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => {transport = signal;return await pending.promise as T;}}, () => true);
  const controller = new AbortController(), read = reader.read(definition(), controller.signal);
  try {await tick();const cancelled = assert.rejects(read, {name: 'AbortError'});controller.abort();await cancelled;assert.equal(transport?.aborted, true);} finally {pending.resolve(wire());reader.dispose();await tick();}
});

test('captured authority retirement cannot publish a response for the next viewer', async () => {
  let current = true;const pending = deferred<unknown>();
  const reader = createSavedViewCapabilitiesReader({request: async<T>(): Promise<T> => await pending.promise as T}, () => current);
  const read = reader.read(definition(), new AbortController().signal);
  try {await tick();current = false;pending.resolve(wire());await assert.rejects(read, {name: 'AbortError'});await assert.rejects(reader.read(definition(), new AbortController().signal), {name: 'AbortError'});} finally {reader.dispose();}
});

test('another library or resolved pivot is rejected, never used to label a saved view', async () => {
  for (const response of [wire('other-library'), {...wire(), resolvedPivot: {...wire().pivots[0], id: 'shows'}}]) {
    const reader = createSavedViewCapabilitiesReader({request: async<T>(): Promise<T> => response as T}, () => true);
    try {await assert.rejects(reader.read(definition(), new AbortController().signal), {code: 'invalid_browse'});} finally {reader.dispose();}
  }
});

test('only four real requests run at once; disposal rejects queued and active consumers', async () => {
  const pending: {library: string; reply: ReturnType<typeof deferred<unknown>>}[] = [];
  let active = 0, peak = 0;
  const reader = createSavedViewCapabilitiesReader({request: async<T>(path: string): Promise<T> => {
    active++;peak = Math.max(peak, active);
    const library = new URL(path, 'http://test').pathname.split('/')[3], reply = deferred<unknown>();pending.push({library, reply});
    try {return await reply.promise as T;} finally {active--;}
  }}, () => true);
  const reads = Array.from({length: 6}, (_, index) => reader.read(definition(`lib_${index}`), new AbortController().signal));
  // Attach all rejection observers before cancelling the captured reader.
  const results = Promise.allSettled(reads);
  try {
    await tick();assert.equal(pending.length, 4);assert.equal(peak, 4);
    pending[0].reply.resolve(wire(pending[0].library));await tick();assert.equal(pending.length, 5);assert.equal(peak, 4);
    reader.dispose();const settled = await results;
    assert.equal(settled.filter(value => value.status === 'fulfilled').length, 1);
    assert(settled.filter(value => value.status === 'rejected').every(value => value.status === 'rejected' && value.reason.name === 'AbortError'));
  } finally {reader.dispose();for (const value of pending) value.reply.resolve(wire(value.library));await tick();}
});
