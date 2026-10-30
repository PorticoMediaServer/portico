import test from 'node:test';
import assert from 'node:assert/strict';
import {parseWholeSelection, type BrowseWholeSelection} from '../src/browse.ts';
import {parsePreparedBrowseSelection} from '../src/browse-preparation.ts';
import {browseWholeSelectionQuery} from '../src/browse-whole-selection.ts';
import {BrowseSelectionModel} from '../src/selection-model/index.ts';
import {jobRequestBody, type JobCommand, type JobArgs} from '../src/bulk-jobs.ts';
import {selectionJobTasks} from '../src/home-selection/execution.ts';
import {catalogueQuerySelectionSegments, enqueueCatalogueQuerySelection, playCatalogueQuerySelection} from '../src/catalogue-query-selection.ts';
import {QueueClient} from '../src/playback-v1/queue.ts';

const proof = '0123456789abcdef'.repeat(4), nextProof = 'f'.repeat(64), revision = 'browse_' + 'c'.repeat(64);
const sort = [{field: 'title', direction: 'asc' as const}];
const counts = {play: 2, queue: 2, playlist: 2, myList: 2, watched: 2, collection: 2, favorite: 2, download: 2, edit: 2, refresh: 2, delete: 2};
function wire(selectionBasis: unknown = proof) {return {selector: {query: {libraryId: 'lib', pivot: 'movies', sort, ...(selectionBasis === undefined ? {} : {selectionBasis})}},
  catalogRevision: revision, total: 2, types: [{kind: 'movie', count: 2, capabilityCounts: counts, libraries: [{libraryId: 'lib', count: 2}]}]};}
const page = {revision, pivot: 'movies', query: null, sort};
const context = {scope: {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', viewId: 'view'}, revision: {pageRevision: revision}};
function selected(selectionBasis = proof) {
  const whole = parsePreparedBrowseSelection(wire(selectionBasis), 'lib', page), query = browseWholeSelectionQuery(whole)!;
  const model = new BrowseSelectionModel(context);
  assert.equal(model.registerQuery(model.captureContext(), query).ok, true); assert.equal(model.selectWholeQuery(query.id).ok, true);
  const target = model.getSnapshot().target; assert.ok(target?.mode === 'query'); assert.ok('query' in target.selector);
  return {model, target: Object.freeze({...target, selector: target.selector}), query, whole};
}

test('the opaque full witness survives parsing and distinguishes held selections under the same page revision', () => {
  const first = selected(), second = selected(nextProof);
  try {
    assert.equal(first.whole.catalogRevision, second.whole.catalogRevision); assert.notEqual(first.query.id, second.query.id);
    assert.equal(first.whole.selectionBasis, proof); assert.equal(first.target.selector.query.selectionBasis, proof);
    assert.equal(second.target.selector.query.selectionBasis, nextProof);
    assert.equal(first.model.registerQuery(first.model.captureContext(), second.query).ok, true);
    assert.equal(first.model.selectWholeQuery(second.query.id).ok, true);
    assert.equal(first.target.selector.query.selectionBasis, proof, 'the original held target keeps its own exact proof');
    const next = first.model.getSnapshot().target; assert.ok(next?.mode === 'query' && 'query' in next.selector);
    assert.equal(next.selector.query.selectionBasis, nextProof); assert.equal(first.model.getSnapshot().selectedKeys.length, 0);
  } finally {first.model.dispose(); second.model.dispose();}
});

test('legacy ordinary descriptors may omit the proof; prepared ready descriptors must carry it', () => {
  const legacy = wire(); delete (legacy.selector.query as {selectionBasis?: unknown}).selectionBasis;
  const parsed = parseWholeSelection(legacy, page); assert.ok(parsed); assert.equal(parsed.selectionBasis, undefined);
  assert.ok(browseWholeSelectionQuery(parsed));
  assert.throws(() => parsePreparedBrowseSelection(legacy, 'lib', page), {code: 'stale_continuation'});
  for (const invalid of [null, '', 'A'.repeat(64), 'a'.repeat(63), 'g'.repeat(64), 'a'.repeat(65), 1, {}]) {
    assert.equal(parseWholeSelection(wire(invalid), page), undefined);
    assert.throws(() => parsePreparedBrowseSelection(wire(invalid), 'lib', page), {code: 'stale_continuation'});
    assert.equal(browseWholeSelectionQuery({...parsed, selectionBasis: invalid} as BrowseWholeSelection), undefined);
  }
});

test('all job commands retain a supplied query basis and malformed proofs fail validation', () => {
  const held = selected();
  const commands: readonly [JobCommand, JobArgs][] = [['personal-state', {favorite: true}], ['playlist-add', {playlistId: 'playlist', expectedRevision: 1, placement: 'end'}],
    ['collection-add', {collectionId: 'collection', expectedRevision: 1}], ['metadata-edit', {fields: {title: 'Title'}}], ['refresh', {}]];
  try {
    for (const [command, args] of commands) {
      const body = jobRequestBody({operationId: 'operation', command, selector: held.target.selector, expected: held.target.expected, args});
      assert.ok('query' in body.selector); assert.equal(body.selector.query.selectionBasis, proof); assert.equal(body.expected?.catalogRevision, revision);
      assert.equal('items' in body.selector, false); assert.ok(Object.isFrozen(body.selector.query));
    }
    for (const invalid of [null, '', 'A'.repeat(64), 'a'.repeat(63)]) assert.throws(() => jobRequestBody({operationId: 'operation', command: 'refresh', args: {},
      selector: {query: {...held.target.selector.query, selectionBasis: invalid}} as never}));
    const legacy = jobRequestBody({operationId: 'operation', command: 'refresh', args: {}, selector: {query: {libraryId: 'lib', pivot: 'movies'}}});
    assert.ok('query' in legacy.selector); assert.equal(legacy.selector.query.selectionBasis, undefined);
  } finally {held.model.dispose();}
});

test('destination job planning copies the full proof into one server-captured intent', async () => {
  const held = selected();
  try {
    for (const [command, args] of [['playlist-add', {playlistId: 'playlist', expectedRevision: 1, placement: 'end'}], ['collection-add', {collectionId: 'collection', expectedRevision: 1}]] as const) {
      const tasks = await selectionJobTasks(held.target as never, command, args, async () => 'operation'); assert.equal(tasks.length, 1);
      const selector = tasks[0]!.request.selector; assert.ok('query' in selector); assert.equal(selector.query.selectionBasis, proof);
      assert.equal('items' in selector, false); assert.equal(tasks[0]!.request.expected?.catalogRevision, revision);
    }
  } finally {held.model.dispose();}
});

test('Play and Queue carry both the page revision and exact selection basis through the actual queue wire', async () => {
  const held = selected(), sources: unknown[] = [], calls: {method: string; body?: unknown}[] = [];
  try {
    const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
    const play = {playSegments: async (segments: unknown) => {sources.push(segments);}};
    const enqueue = {enqueueSegments: async (segments: unknown) => {sources.push(segments);}};
    await playCatalogueQuerySelection(play as never, held.target, lifetime);
    await enqueueCatalogueQuerySelection(enqueue as never, held.target, 'end', lifetime);
    const segments = catalogueQuerySelectionSegments(held.target, 'play'); assert.deepEqual(sources, [segments, segments]);
    assert.ok('query' in segments[0]!.source); assert.equal(segments[0]!.source.query.selectionBasis, proof); assert.equal(segments[0]!.source.query.selectionRevision, revision);
    const client = new QueueClient({send: async request => {calls.push(request); return {status: 200, headers: {}, body: {queue: {id: 'queue', revision: '1', total: 0, repeat: 'off', segments: []}, window: []}};}}, () => 'operation');
    await client.create(segments); await client.batch('queue', '1', segments[0]!.source, 'end');
    assert.equal((calls[0]!.body as any).segments[0].source.query.selectionBasis, proof);
    assert.equal((calls[1]!.body as any).source.query.selectionBasis, proof);
    assert.equal(JSON.stringify(calls).includes('"ids"'), false, 'no membership enumeration or items fallback');
    for (const invalid of [null, '', 'A'.repeat(64)]) assert.throws(() => catalogueQuerySelectionSegments({...held.target, selector: {query: {...held.target.selector.query, selectionBasis: invalid}}} as never, 'play'));
  } finally {held.model.dispose();}
});
