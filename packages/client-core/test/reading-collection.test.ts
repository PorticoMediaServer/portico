import test from 'node:test';
import assert from 'node:assert/strict';
import {ReadingCollection, type ReadingCollectionItem} from '../src/reading-collection.ts';

function fixture(count = 2, canCreate = false) {
  let current = true, grant = true, canAdd = true, revision = 7, fence = 'home-fence', lost = false, partial = false, pickerFailure = false, ids = 0;
  const items: ReadingCollectionItem[] = Array.from({length: count}, (_, n) => ({id: `book-${n}`, libraryId: 'books', kind: n % 2 ? 'comic' : 'ebook', title: `Title ${n}`}));
  const calls: {path: string; method?: string; body?: any}[] = [];
  const jobs = new Map<string, any>();
  const life = new AbortController();
  const resource = () => ({serverId: 'server', viewerFence: fence, id: 'collection', kind: 'collection', name: 'Reading', summary: '', visibility: 'private', revision,
    role: 'editor', entryCount: 3, actions: ['entries'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []});
  const api = {request: async <T>(path: string, method?: string, body?: any): Promise<T> => {
    calls.push({path, method, body: structuredClone(body)});
    if (path.includes('/detail')) {
      const item = items.find(item => path.includes(`/${item.id}/`))!;
      return {scope: {serverId: 'server', libraryId: item.libraryId, itemId: item.id, viewerFence: 'detail-fence'}, revision: {catalog: 1, viewer: 0},
        item: {...item, available: true}, personal: {watchlisted: false, favorite: false, rating: null, revision: 0, watched: false, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []},
        actions: grant ? [{id: 'add_to_collection', labelKey: 'add', enabled: true}] : [], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}} as T;
    }
    if (path === '/v1/home/destination-picker' && pickerFailure) {pickerFailure = false; throw new Error('picker lost');}
    if (path === '/v1/home/destination-picker') return {serverId: 'server', viewerFence: fence, ...body, revision: {catalog: 1, viewer: 0},
      rows: [{id: 'collection', name: 'Reading', count: 3, revision: 7, canAdd, actions: canAdd ? ['entries'] : [], artwork: []}], nextCursor: ''} as T;
    if (path.startsWith('/v1/saved-resources?')) return {serverId: 'server', viewerFence: fence, revision: 1, resources: [], nextCursor: ''} as T;
    if (path === '/v1/saved-resources' && method === 'POST') {
      if (lost) {lost = false; throw new Error('lost create response');}
      return {serverId: 'server', viewerFence: fence, receipt: {operationId: body.operationId, resourceId: 'collection', revision: 7, deleted: false}} as T;
    }
    if (path.includes('/content')) return {resource: resource(), entries: [], nextCursor: '', query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
      sorts: [{id: 'position', labelKey: 'sort.position', directions: ['asc']}], filters: [{id: 'filter', labelKey: 'saved.filter', options: [{id: 'all', label: 'All', count: 0}]}], totalCount: 0, start: 0} as T;
    if (path === '/v1/jobs') {
      const existing = jobs.get(body.operationId);
      const size = body.selector.items.ids.length, failed = partial && !existing && jobs.size === 0 ? 1 : 0;
      const job = existing ?? {jobId: `job-${jobs.size}`, command: 'collection-add', state: failed ? 'partial' : 'complete', total: size, totalKnown: true, done: size - failed, failed};
      jobs.set(body.operationId, job);
      if (lost) {lost = false; throw new Error('lost response');}
      return job as T;
    }
    if (path === '/v1/home/destination-receipt') {
      const failed = partial && jobs.size === 1 ? 1 : 0;
      return {serverId: 'server', viewerFence: fence, ...body, jobs: [...jobs.values()], added: count - failed, alreadyPresent: 0, failed, unknown: 0,
        complete: true, pending: false, unresolvedJobIds: [], failures: failed ? [{itemId: items.at(-1)!.id, code: 'not_found'}] : [],
        failureEntries: failed ? [items.at(-1)!] : [], nextCursor: ''} as T;
    }
    throw new Error(`unexpected ${method} ${path}`);
  }};
  const controller = new ReadingCollection({api, scope: {serverId: 'server', viewerId: '["local","member","profile"]'}, requestId: async () => `00000000-0000-4000-8000-${String(++ids).padStart(12, '0')}`,
    signal: life.signal, isCurrent: () => current, canCreate}, items);
  return {controller, items, calls, life, retire: () => {current = false;}, revoke: () => {grant = false;}, noDestinationGrant: () => {canAdd = false;},
    drift: () => {revision++;}, changeFence: () => {fence = 'other';}, lose: () => {lost = true;}, partial: () => {partial = true;}, failPicker: () => {pickerFailure = true;}};
}

test('a member opens the canonical picker and adds exactly 200 Reading picks in one scoped CAS job', async () => {
  const f = fixture(200); try {
    await f.controller.load(); const state = f.controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(f.calls.find(call => call.path.endsWith('destination-picker'))!.body.itemIds, f.items.map(item => item.id));
    await f.controller.choose(state, 'collection');
    const writes = f.calls.filter(call => call.path === '/v1/jobs'); assert.equal(writes.length, 1);
    assert.deepEqual(writes[0].body.selector, {items: {ids: f.items.map(item => item.id)}});
    assert.deepEqual(writes[0].body.args, {collectionId: 'collection', expectedRevision: 7});
    assert.equal(f.calls.filter(call => call.path.includes('/detail')).length, 400, 'bounded exact per-item grants on opening and write');
    assert.equal(f.controller.getSnapshot().receipt?.added, 200);
    await assert.rejects(f.controller.create('Forbidden')); assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
  } finally {f.controller.dispose();}
});

test('missing item grant, revoked grant and a non-addable collection never submit a job', async () => {
  for (const when of ['opening', 'write', 'destination']) {
    const f = fixture(); try {
      if (when === 'opening') {f.revoke(); await assert.rejects(f.controller.load());}
      else {if (when === 'destination') f.noDestinationGrant(); await f.controller.load(); if (when === 'write') f.revoke(); await assert.rejects(f.controller.choose(f.controller.getSnapshot(), 'collection'));}
      assert.equal(f.calls.filter(call => call.path === '/v1/jobs').length, 0);
    } finally {f.controller.dispose();}
  }
});

test('stale rendered picker, collection CAS drift and retired selection cannot broaden or replace scope', async () => {
  for (const when of ['render', 'revision', 'selection', 'fence']) {
    const f = fixture(); try {
      await f.controller.load(); const rendered = f.controller.getSnapshot();
      if (when === 'render') await f.controller.search('new');
      if (when === 'revision') f.drift();
      if (when === 'selection') f.retire();
      if (when === 'fence') f.changeFence();
      await assert.rejects(f.controller.choose(rendered, 'collection'));
      assert.equal(f.calls.filter(call => call.path === '/v1/jobs').length, 0);
    } finally {f.controller.dispose();}
  }
});

test('uncertain write retries the identical operation, selector and destination revision', async () => {
  const f = fixture(); try {
    await f.controller.load(); f.lose();
    await assert.rejects(f.controller.choose(f.controller.getSnapshot(), 'collection'));
    assert.equal(f.controller.getSnapshot().phase, 'uncertain');
    await f.controller.retry();
    const writes = f.calls.filter(call => call.path === '/v1/jobs'); assert.equal(writes.length, 2); assert.deepEqual(writes[0].body, writes[1].body);
    assert.equal(f.controller.getSnapshot().receipt?.added, 2);
  } finally {f.controller.dispose();}
});

test('partial receipt remains visible and explicit retry names only its real failed publication', async () => {
  const f = fixture(); try {
    f.partial(); await f.controller.load(); await f.controller.choose(f.controller.getSnapshot(), 'collection');
    assert.equal(f.controller.getSnapshot().receipt?.failed, 1);
    await f.controller.retry();
    const writes = f.calls.filter(call => call.path === '/v1/jobs');
    assert.deepEqual(writes[1].body.selector.items.ids, [f.items.at(-1)!.id]);
    assert.equal(f.controller.getSnapshot().receipt?.added, 2); assert.equal(f.controller.getSnapshot().receipt?.failed, 0);
  } finally {f.controller.dispose();}
});

test('an aborted original selection refuses all later picker commands', async () => {
  const f = fixture(); await f.controller.load(); const state = f.controller.getSnapshot(); f.life.abort();
  const before = f.calls.length; await assert.rejects(f.controller.choose(state, 'collection')); assert.equal(f.calls.length, before);
});


test('creating a collection retries its exact original creation before one membership job', async () => {
  const f = fixture(2, true); try {
    await f.controller.load(); f.lose(); await assert.rejects(f.controller.create('New collection'));
    assert.equal(f.controller.getSnapshot().phase, 'uncertain');
    await f.controller.retry();
    const creates = f.calls.filter(call => call.path === '/v1/saved-resources' && call.method === 'POST');
    assert.equal(creates.length, 2); assert.deepEqual(creates[0].body, creates[1].body);
    assert.equal(f.calls.filter(call => call.path === '/v1/jobs').length, 1);
    assert.equal(f.controller.getSnapshot().receipt?.added, 2);
  } finally {f.controller.dispose();}
});

test('a failed picker search retries that literal query without submitting a command', async () => {
  const f = fixture(); try {
    await f.controller.load(); f.failPicker(); await assert.rejects(f.controller.search('New query'));
    await f.controller.retry();
    const reads = f.calls.filter(call => call.path === '/v1/home/destination-picker');
    assert.deepEqual(reads.at(-1)!.body, reads.at(-2)!.body);
    assert.equal(reads.at(-1)!.body.query, 'New query'); assert.equal(f.calls.filter(call => call.path === '/v1/jobs').length, 0);
  } finally {f.controller.dispose();}
});
