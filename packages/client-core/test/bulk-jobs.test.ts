import test from 'node:test';
import assert from 'node:assert/strict';
import {
  bulkFailureCounts, createJob, getAllJobFailures, getJob, getJobFailures, groupMetadataTargets, isJobTerminal, itemsJobRequests, itemsSelector,
  jobRequestBody, parseJob, parseJobFailures, parseJobUpdatedEvent, parseJobUpdatedFrame, pollJob, runBulkJobs, runBulkRequests,
  IDEMPOTENCY_REUSED, REVISION_MISMATCH,
} from '../src/bulk-jobs.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

function job(over: Record<string, unknown> = {}): any {
  return {
    jobId: 'job-1', command: 'personal-state', state: 'running',
    total: 0, totalKnown: false, done: 0, failed: 0, ...over,
  };
}

function fakeApi(routes: Record<string, (body?: unknown) => unknown>): {request<T>(path: string, method?: string, body?: unknown): Promise<T>; calls: {path: string; method?: string; body?: unknown}[]} {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  return {
    calls,
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      calls.push({path, method, body});
      const route = routes[method + ' ' + path.split('?')[0]];
      if (!route) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      return route(body) as T;
    },
  };
}

test('a bulk receipt starts indeterminate and parses terminal states', () => {
  const created = parseJob(job());
  assert.equal(created.totalKnown, false);
  assert.equal(isJobTerminal(created.state), false);
  for (const state of ['complete', 'partial', 'failed']) assert.equal(isJobTerminal(parseJob(job({state, total: 10, totalKnown: true, done: 9, failed: 1})).state), true);
  assert.equal(parseJob(job({state: 'failed', total: 4, totalKnown: true, done: 1, failed: 1, errorCode: 'selection_changed'})).errorCode, 'selection_changed');
});

test('a job rejects an unknown state or command, an unreadable id, count or cursor', () => {
  for (const change of [
    (v: any) => { v.state = 'done'; },
    (v: any) => { v.command = 'reindex'; },
    (v: any) => { v.jobId = ''; },
    (v: any) => { v.jobId = 'job\n1'; },
    (v: any) => { v.jobId = 'j'.repeat(257); },
    (v: any) => { v.total = -1; },
    (v: any) => { v.done = 1.5; },
    (v: any) => { v.failed = '0'; },
    (v: any) => { v.totalKnown = 'yes'; },
    (v: any) => { v.failuresCursor = ''; },
    (v: any) => { v.failuresCursor = 'c'.repeat(257); },
  ]) {
    const raw = job({total: 0, totalKnown: false});
    change(raw);
    assert.throws(() => parseJob(raw));
  }
  for (const raw of [null, [], 'job']) assert.throws(() => parseJob(raw));
});

test('a job keeps its progress past a failure code it does not know, a stray code, a field it does not know and a count over the total', () => {
  resetSkippedParts();
  // An unknown failure code is left out, so the caller's general wording is used.
  const unknownCode = parseJob(job({state: 'failed', total: 1, totalKnown: true, errorCode: 'made_up'}));
  assert.equal(unknownCode.state, 'failed');
  assert.equal('errorCode' in unknownCode, false);
  // A failure code on a job that has not failed is left out.
  const stray = parseJob(job({errorCode: 'selection_changed'}));
  assert.equal(stray.state, 'running');
  assert.equal('errorCode' in stray, false);
  assert.equal('errorCode' in parseJob(job({state: 'failed', errorCode: 7})), false);
  // A field from a newer server is ignored and never carried along.
  const extra = parseJob(job({state: 'partial', total: 3, totalKnown: true, done: 2, failed: 1, failuresCursor: 'next', pausedBy: 'owner', estimate: {seconds: 4}}));
  assert.deepEqual(extra, {jobId: 'job-1', command: 'personal-state', state: 'partial', total: 3, totalKnown: true, done: 2, failed: 1, failuresCursor: 'next'});
  // A finished count over a known total reads as the total; while the total is unknown it is kept.
  assert.equal(parseJob(job({totalKnown: true, total: 2, done: 3})).done, 2);
  assert.equal(parseJob(job({totalKnown: false, total: 0, done: 3})).done, 3);
  assert.deepEqual(skippedParts().map(p => [p.part, p.count]), [['job.errorCode', 3], ['job.done', 1]]);
  assert.ok(skippedParts().every(p => p.document === 'bulk-jobs'));
});

test('a failures page keeps its readable rows, the first row of a repeated item and its cursor', () => {
  resetSkippedParts();
  const page = parseJobFailures({items: [
    {itemId: 'a', code: 'trash_conflict', detail: {reason: 'newer field'}},
    {itemId: 'b', code: ''},
    {itemId: '', code: 'trash_conflict'},
    'not a row',
    {itemId: 'a', code: 'later_repeat'},
    {itemId: 'c', code: 'a_code_this_app_has_never_seen'},
  ], nextCursor: 'page-2', total: 6});
  assert.deepEqual(page, {items: [{itemId: 'a', code: 'trash_conflict'}, {itemId: 'c', code: 'a_code_this_app_has_never_seen'}], nextCursor: 'page-2'});
  assert.deepEqual(skippedParts().map(p => [p.document, p.part, p.count]), [['bulk-jobs', 'failures', 4]]);
  // The page's shape, its size bound and its cursor still refuse the whole page.
  assert.throws(() => parseJobFailures(null));
  assert.throws(() => parseJobFailures({items: {}}));
  assert.throws(() => parseJobFailures({items: new Array(101).fill(0).map((_, i) => ({itemId: 'id-' + i, code: 'x'}))}));
  assert.throws(() => parseJobFailures({items: [], nextCursor: 7}));
  assert.throws(() => parseJobFailures({items: [], nextCursor: 'c'.repeat(4097)}));
  assert.equal(parseJobFailures({items: new Array(100).fill(0).map((_, i) => ({itemId: 'id-' + i, code: 'x'}))}).items.length, 100);
});

test('requests validate selectors, args and fences', () => {
  const base = {operationId: 'op-1', command: 'personal-state' as const, selector: itemsSelector(['a', 'b']), args: {watched: true}};
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
    const placed = jobRequestBody({operationId: 'op-4', command: 'playlist-add', selector: itemsSelector(['a']), args: {playlistId: 'p', expectedRevision: 3, placement: placement as any}});
    assert.deepEqual(placed.args.placement, placement);
  }
  assert.throws(() => jobRequestBody({operationId: 'op-4', command: 'playlist-add', selector: itemsSelector(['a']), args: {playlistId: 'p', expectedRevision: 0, placement: 'end'}}));
  // Ratings are whole numbers from 0 to 10 (two points to a star).
  assert.throws(() => jobRequestBody({...base, args: {rating: 2.5}}));
  assert.throws(() => jobRequestBody({...base, args: {rating: 11}}));
  jobRequestBody({...base, args: {rating: 5}});
});

test('explicit selections chunk into one 200-item request each', () => {
  assert.throws(() => itemsSelector([]));
  assert.throws(() => itemsSelector(new Array(201).fill('x').map((_, i) => 'id-' + i)));
  const ids = new Array(450).fill(0).map((_, i) => 'id-' + i);
  const requests = itemsJobRequests(['op-a', 'op-b', 'op-c'], 'personal-state', {watched: true}, ids);
  assert.equal(requests.length, 3);
  assert.equal((requests[0].selector as {items: {ids: string[]}}).items.ids.length, 200);
  assert.equal((requests[2].selector as {items: {ids: string[]}}).items.ids.length, 50);
  assert.throws(() => itemsJobRequests(['only'], 'personal-state', {watched: true}, ids));
});

test('create is idempotent on the same operationId and maps the shared error codes', async () => {
  const api = fakeApi({'POST /v1/jobs': () => job({totalKnown: false})});
  const created = await createJob(api, {operationId: 'op-1', command: 'personal-state', selector: itemsSelector(['a']), args: {watched: true}});
  assert.equal(created.jobId, 'job-1');
  assert.deepEqual(api.calls[0].body, {operationId: 'op-1', command: 'personal-state', selector: {items: {ids: ['a']}}, args: {watched: true}});
  const reused = fakeApi({'POST /v1/jobs': () => { throw Object.assign(new Error('reused'), {status: 422, code: 'idempotency_key_reused'}); }});
  await assert.rejects(createJob(reused, {operationId: 'op-1', command: 'personal-state', selector: itemsSelector(['a']), args: {watched: true}}), (e: any) => e.code === IDEMPOTENCY_REUSED);
  assert.equal(IDEMPOTENCY_REUSED, 'idempotency_key_reused');
  assert.equal(REVISION_MISMATCH, 'revision_mismatch');
  // A bare 409 still means a stale revision, never a reused key.
  const conflict = fakeApi({'POST /v1/jobs': () => { throw Object.assign(new Error('stale'), {status: 409}); }});
  await assert.rejects(createJob(conflict, {operationId: 'op-1', command: 'personal-state', selector: itemsSelector(['a']), args: {watched: true}}), (e: any) => e.code === 'revision_mismatch');
});

test('polling ends at a terminal state and failures page through cursors', async () => {
  let polls = 0;
  const api = fakeApi({
    'GET /v1/jobs/job-1': () => (++polls < 3 ? job({state: 'running', total: 4, totalKnown: true, done: polls}) : job({state: 'partial', total: 4, totalKnown: true, done: 3, failed: 1})),
    'GET /v1/jobs/job-1/failures': () => ({items: [{itemId: 'a', code: 'trash_conflict'}], nextCursor: ''}),
  });
  const finished = await pollJob(api, 'job-1', {intervalMs: 250, timeoutMs: 10000});
  assert.equal(finished.state, 'partial');
  assert.equal((await getJobFailures(api, 'job-1')).items.length, 1);
  assert.deepEqual(parseJobFailures({items: [{itemId: 'a', code: ''}]}).items, []);
});

test('failure pages collect across cursors and stop on a repeated cursor', async () => {
  const api = fakeApi({
    'GET /v1/jobs/job-1/failures': (body?: unknown) => {
      void body;
      return {items: [{itemId: 'a', code: 'trash_conflict'}], nextCursor: 'stuck'};
    },
  });
  // A cursor that never advances is a contract break, not an infinite loop.
  await assert.rejects(getAllJobFailures(api, 'job-1'));
});

test('job.updated frames parse progress and ignore other events', () => {
  const event = parseJobUpdatedFrame('job.updated', JSON.stringify(job({total: 5, totalKnown: true, done: 2})));
  assert.equal(event?.done, 2);
  assert.equal(parseJobUpdatedFrame('queue.updated', '{}'), null);
  assert.throws(() => parseJobUpdatedFrame('job.updated', 'not json'));
  assert.deepEqual(parseJobUpdatedEvent(job({command: 'refresh'})).command, 'refresh');
});

test('a job.updated frame in a state or for a command this app does not know is passed over, and an unreadable one still fails', () => {
  resetSkippedParts();
  assert.equal(parseJobUpdatedFrame('job.updated', JSON.stringify(job({state: 'cancelled'}))), null);
  assert.equal(parseJobUpdatedFrame('job.updated', JSON.stringify(job({command: 'reindex'}))), null);
  assert.deepEqual(skippedParts().map(p => [p.document, p.part, p.count]), [['bulk-jobs', 'event', 2]]);
  assert.equal(parseJobUpdatedFrame('job.updated', JSON.stringify(job({state: 'failed', errorCode: 'made_up', note: 'newer field'})))?.state, 'failed');
  // The single job document has no neutral state, so it still refuses one it does not know.
  assert.throws(() => parseJobUpdatedEvent(job({state: 'cancelled'})));
  for (const raw of [job({jobId: ''}), job({state: 'cancelled', jobId: 7}), job({state: 5}), job({done: -1}), job({failuresCursor: ''}), []]) assert.throws(() => parseJobUpdatedFrame('job.updated', JSON.stringify(raw)));
});

test('M25-4: runBulkRequests runs one job per request with preparing/working progress', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const state: Record<string, {polls: number}> = {};
  let n = 0;
  const api = {
    calls,
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      calls.push({path, method, body});
      if (method === 'POST' && path === '/v1/jobs') {
        const id = 'job-' + (++n);
        state[id] = {polls: 0};
        return {jobId: id, command: (body as {command: string}).command, state: 'running', total: 0, totalKnown: false, done: 0, failed: 0} as T;
      }
      const poll = path.match(/^\/v1\/jobs\/([^/]+)$/);
      if (poll && method === 'GET') {
        const polls = state[poll[1]]!.polls++;
        const terminal = polls > 0;
        return {jobId: poll[1], command: 'metadata-edit', state: terminal ? 'complete' : 'running', total: terminal ? 2 : 0, totalKnown: terminal, done: terminal ? 2 : 0, failed: 0} as T;
      }
      const failures = path.match(/^\/v1\/jobs\/([^/]+)\/failures/);
      if (failures && method === 'GET') return {items: [], nextCursor: ''} as T;
      throw new Error('unexpected ' + method + ' ' + path);
    },
  };
  const seen: string[] = [];
  const outcome = await runBulkRequests(api, 'metadata-edit', {lockEdited: true}, [{selector: {items: {ids: ['a', 'b']}}, operationId: 'op-1', ids: ['a', 'b']}], p => seen.push(p.phase));
  assert.equal(outcome.ok, 2);
  assert.equal(outcome.jobs, 1);
  assert.ok(seen.includes('preparing') && seen.includes('working'), seen.join(','));
});

test('M25-4: runBulkRequests maps the failures page to per-target outcomes; a failed job throws', async () => {
  let n = 0;
  const api = {
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      if (method === 'POST') {
        n++;
        return {jobId: 'job-' + n, command: 'metadata-edit', state: 'running', total: 0, totalKnown: false, done: 0, failed: 0} as T;
      }
      if (path.endsWith('/failures')) return {items: [{itemId: 'b', code: 'metadata_conflict'}], nextCursor: ''} as T;
      return {jobId: 'job-1', command: 'metadata-edit', state: 'complete', total: 2, totalKnown: true, done: 1, failed: 1} as T;
    },
  };
  const outcome = await runBulkRequests(api, 'metadata-edit', {lockEdited: true}, [{selector: {items: {ids: ['a', 'b']}}, operationId: 'op-1', ids: ['a', 'b']}]);
  assert.equal(outcome.ok, 1);
  assert.deepEqual(outcome.failed, [{itemId: 'b', code: 'metadata_conflict'}]);
  assert.deepEqual(bulkFailureCounts(outcome.failed), {metadata_conflict: 1});
  const failing = {request: async <T>(): Promise<T> => ({jobId: 'job-9', command: 'refresh', state: 'failed', total: 0, totalKnown: true, done: 0, failed: 0, errorCode: 'selection_changed'}) as T};
  await assert.rejects(runBulkRequests(failing, 'refresh', {}, [{selector: {items: {ids: ['a']}}, operationId: 'op-1', ids: ['a']}]), (e: unknown) => (e as {code?: string}).code === 'selection_changed');
});

test('M25-4: runBulkJobs chunks explicit ids one job per 200', async () => {
  const posts: unknown[] = [];
  const api = {
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      if (method === 'POST') { posts.push(body); return {jobId: 'job-' + posts.length, command: 'personal-state', state: 'complete', total: 200, totalKnown: true, done: 200, failed: 0} as T; }
      if (path.endsWith('/failures')) return {items: [], nextCursor: ''} as T;
      return {jobId: 'job-1', command: 'personal-state', state: 'complete', total: 0, totalKnown: true, done: 0, failed: 0} as T;
    },
  };
  const ids = Array.from({length: 250}, (_, i) => 'id-' + i);
  const outcome = await runBulkJobs(api, 'personal-state', {watched: true}, ids, ['op-a', 'op-b']);
  assert.equal(outcome.jobs, 2);
  assert.equal(posts.length, 2);
});

test('M25-4: groupMetadataTargets chunks items at 200 with one container selector each', () => {
  let n = 0;
  const makeId = () => 'op-' + (++n);
  const items = Array.from({length: 201}, (_, i) => ({kind: 'item', id: 'id-' + i}));
  const groups = groupMetadataTargets(items, makeId);
  assert.equal(groups.length, 2);
  assert.deepEqual(groups[0]!.ids.length, 200);
  assert.deepEqual(groups[1]!.ids, ['id-200']);
  assert.ok(groups.every(g => g.operationId.startsWith('op-')));
  const mixed = groupMetadataTargets([{kind: 'item', id: 'a'}, {kind: 'show', id: 's'}, {kind: 'album', id: 'b'}], makeId);
  assert.equal(mixed.length, 3);
  assert.deepEqual(mixed[0]!.selector, {items: {ids: ['a']}});
  assert.deepEqual(mixed[1]!.selector, {container: {kind: 'show', id: 's'}});
  assert.deepEqual(mixed[2]!.selector, {container: {kind: 'album', id: 'b'}});
});
