import test from 'node:test';
import assert from 'node:assert/strict';
import {createJob, getJob, isJobTerminal, jobRequestBody, jobViewFilter} from '../src/bulk-jobs.ts';
import {runSelectionJobs, type HomeJobTask} from '../src/home-selection/execution.ts';
type Call = {path: string; method?: string; body?: unknown};
function fakeApi(answer: (call: Call) => unknown): {request<T>(path: string, method?: string, body?: unknown): Promise<T>; calls: Call[]} {
  const calls: Call[] = [];
  return {calls, async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {const call = {path, method, body}; calls.push(call); return answer(call) as T;}};
}
const refused = (code: string) => {throw Object.assign(new Error(code), {status: 404, code});};

test('requests validate selectors, args and fences', () => {
  const base = {operationId: 'op-1', command: 'personal-state' as const, selector: {items: {ids: ['a', 'b']}}, args: {watched: true}};
  assert.deepEqual(jobRequestBody(base).selector, {items: {ids: ['a', 'b']}});
  assert.throws(() => jobRequestBody({...base, operationId: 'has spaces!'}));
  assert.throws(() => jobRequestBody({...base, selector: {items: {ids: []}} as any}));
  assert.throws(() => jobRequestBody({...base, selector: {container: {kind: 'show', id: 's'}, items: {ids: ['a']}} as any}));
  assert.throws(() => jobRequestBody({...base, args: {}}));
  assert.throws(() => jobRequestBody({...base, args: {watched: true, playlistId: 'p'}}));
  assert.throws(() => jobRequestBody({...base, command: 'refresh' as const, args: {watched: true}}));
  const query = jobRequestBody({
    operationId: 'op-2', command: 'personal-state', args: {favorite: true},
    selector: {query: {libraryId: 'lib', pivot: 'movies', filter: {all: []}, sort: [{field: 'title', direction: 'asc'}]}},
    expected: {catalogRevision: 'rev-9'},
  });
  assert.equal(query.expected?.catalogRevision, 'rev-9');
  const container = jobRequestBody({operationId: 'op-3', command: 'personal-state', args: {watched: false}, selector: {container: {kind: 'season', id: 's1'}}});
  assert.deepEqual(container.selector, {container: {kind: 'season', id: 's1'}});
  // playlist-add: placement next is kept verbatim (it means prepend); after is explicit.
  for (const placement of ['end', 'next', {after: 'entry-1'}]) {
    const placed = jobRequestBody({operationId: 'op-4', command: 'playlist-add', selector: {items: {ids: ['a']}}, args: {playlistId: 'p', expectedRevision: 3, placement: placement as any}});
    assert.deepEqual(placed.args.placement, placement);
  }
  assert.throws(() => jobRequestBody({operationId: 'op-4', command: 'playlist-add', selector: {items: {ids: ['a']}}, args: {playlistId: 'p', expectedRevision: 0, placement: 'end'}}));
  // Ratings are whole numbers from 0 to 10 (two points to a star).
  assert.throws(() => jobRequestBody({...base, args: {rating: 2.5}}));
  assert.throws(() => jobRequestBody({...base, args: {rating: 11}}));
  jobRequestBody({...base, args: {rating: 5}});
});

test('chosen titles are marked one at a time on their own route: what failed is listed with its reason and the rest is done (06 rule 41)', async () => {
  const api = fakeApi(call => call.path === '/v1/items/b/state' ? refused('not_found') : {watched: true});
  const done = await createJob(api, {operationId: 'op1', command: 'personal-state', selector: {items: {ids: ['a', 'b', 'c']}}, args: {watchlist: true}});
  assert.deepEqual(api.calls.map(call => [call.method, call.path, call.body]), [
    ['PUT', '/v1/items/a/state', {inList: true}], ['PUT', '/v1/items/b/state', {inList: true}], ['PUT', '/v1/items/c/state', {inList: true}]]);
  assert.deepEqual([done.state, done.done, done.failed, done.failures], ['partial', 2, 1, [{itemId: 'b', code: 'not_found'}]]);
  assert.equal(isJobTerminal(done.state), true, 'nothing is left to wait for');
  // A show, season or album is one title on the same route: the server marks what it holds.
  const show = fakeApi(() => ({}));
  await createJob(show, {operationId: 'op2', command: 'personal-state', selector: {container: {kind: 'show', id: 's1'}}, args: {watched: false}});
  assert.deepEqual(show.calls.map(call => [call.path, call.body]), [['/v1/items/s1/state', {watched: false}]]);
});

test('chosen titles are added to a playlist or collection one at a time, and the selection runner reports the failures it was given', async () => {
  const api = fakeApi(call => (call.body as {items: string[]}).items[0] === 'x' ? refused('not_permitted') : {added: 1, already: 0});
  const tasks: HomeJobTask[] = [{request: {operationId: 'op3', command: 'collection-add', selector: {items: {ids: ['x', 'y']}}, args: {collectionId: 'lst_1', expectedRevision: 2}}, failures: [], observed: false}];
  await runSelectionJobs(api as never, tasks, new AbortController().signal, () => true, () => {});
  assert.deepEqual(api.calls.map(call => [call.method, call.path, call.body]), [['POST', '/v1/lists/lst_1/entries', {items: ['x']}], ['POST', '/v1/lists/lst_1/entries', {items: ['y']}]]);
  assert.deepEqual([tasks[0]!.job?.done, tasks[0]!.failures], [1, [{itemId: 'x', code: 'not_permitted'}]]);
});

test('a whole view is one job on the server over the view\'s own list query, read until it ends (06 rules 39 and 42)', async () => {
  const query = {libraryId: 'lib_1', pivot: 'movies', filter: {all: [{field: 'genre', operator: 'in', value: ['Drama']}, {field: 'playState', operator: 'equals', value: 'unplayed'}]}, sort: [{field: 'title', direction: 'asc' as const}]};
  assert.equal(jobViewFilter(query as never), 'library=lib_1&kind=movie&genre=Drama&watched=false');
  let reads = 0;
  const api = fakeApi(call => call.method === 'POST' ? {id: 'job_9', kind: 'set_state', state: 'waiting'}
    : ++reads < 2 ? {id: 'job_9', kind: 'set_state', state: 'running', progress: {done: 40}} : {id: 'job_9', kind: 'set_state', state: 'done', progress: {done: 120, total: 120}});
  const started = await createJob(api, {operationId: 'op4', command: 'personal-state', selector: {query} as never, args: {watched: true}});
  assert.deepEqual(api.calls[0], {path: '/v1/jobs', method: 'POST', body: {kind: 'set_state', target: {filter: 'library=lib_1&kind=movie&genre=Drama&watched=false'}, params: {watched: true}}});
  assert.deepEqual([started.jobId, started.state, started.totalKnown], ['job_9', 'queued', false]);
  const running = await getJob(api, 'job_9');
  assert.deepEqual([running.state, running.done, running.totalKnown], ['running', 40, false]);
  const ended = await getJob(api, 'job_9');
  assert.deepEqual([ended.state, ended.done, ended.total], ['complete', 120, 120]);
  assert.equal(api.calls[1]!.path, '/v1/jobs/job_9');
  // Adding the view to a list names the list; a job that failed or was cancelled fails as a whole.
  const add = fakeApi(() => ({id: 'job_10', kind: 'add_view_to_list', state: 'failed', error: 'x'}));
  const failed = await createJob(add, {operationId: 'op5', command: 'playlist-add', selector: {query} as never, args: {playlistId: 'lst_2', expectedRevision: 1, placement: 'end'}});
  assert.deepEqual((add.calls[0]!.body as {kind: string; params: unknown}).params, {list: 'lst_2'});
  assert.deepEqual([failed.state, failed.errorCode], ['failed', 'execution_failed']);
});

test('Refresh metadata on chosen titles is the owner\'s refresh_items job', async () => {
  const api = fakeApi(() => ({id: 'job_11', kind: 'refresh_items', state: 'waiting'}));
  await createJob(api, {operationId: 'op6', command: 'refresh', selector: {items: {ids: ['a', 'b']}}, args: {}});
  assert.deepEqual(api.calls[0]!.body, {kind: 'refresh_items', target: {items: ['a', 'b']}});
});
