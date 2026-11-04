import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {EntryPickerProducer} from '../src/shell/entry-picker.ts';
import {LibraryPickerProducer} from '../src/screens/browse/selection-picker.ts';
import {createSelectionEditor, SelectionEditorError} from '../src/screens/browse/selection-editor.ts';
import {createSelectionActions} from '../src/screens/browse/selection-actions.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// Walk J-11 to J-14 (2 Nov): every selection action answers against the fence its own domain issues and sends
// picked titles as their own IDs. The detail, Home and destination reads each carry a different fence; checking
// one domain's answer against another's made Add to playlist and Add to collection say "That list could not be
// updated." and Edit fail before it asked the server.
const fixture = JSON.parse(readFileSync(new URL('../../packages/client-core/test/compat-corpus/main/item-detail.json', import.meta.url), 'utf8')).body;
const serverId = fixture.scope.serverId as string, detailFence = fixture.scope.viewerFence as string, destinationFence = 'b'.repeat(64);
const leaf = {kind: fixture.item.kind as 'song', id: fixture.item.id as string, libraryId: fixture.item.libraryId as string, title: 'Song'};
const destinationPage = (kind: string) => ({serverId, viewerFence: destinationFence, kind, query: '', revision: {catalog: 1, viewer: 0},
  rows: [{id: 'destination-1', name: 'Mix', count: 0, revision: 1, canAdd: true, actions: [kind === 'playlist' ? 'add' : 'entries'], artwork: []}], nextCursor: ''});
function api(requests: {path: string; method?: string; body?: any}[]) {
  return {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    requests.push({path, method, body});
    if (path.includes('/detail')) return structuredClone(fixture) as T;
    if (path === '/v1/home/destination-picker') return destinationPage((body as {kind: string}).kind) as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
}
const identity = {authority: 'local' as const, accountId: 'account', profileId: 'profile', events: {on: () => () => {}}};
const ports = (requests: {path: string; method?: string; body?: any}[]) => {const value = api(requests); return {api: value, queries: testQueries(value, {serverId, viewerId: 'viewer'}), serverId, viewerId: '["local","account","profile"]',
  isCurrent: () => true, requestId: async () => 'request-1', pickerIdentity: identity};};
assert.notEqual(detailFence, destinationFence, 'the fixture must exercise two different fences');

test('J-11 Add to playlist from a title page: the picker holds the destination read\'s fence and lists the playlists', async () => {
  const requests: {path: string; method?: string; body?: any}[] = [], states: any[] = [];
  const picker = new EntryPickerProducer({libraryId: leaf.libraryId, itemId: leaf.id, kind: leaf.kind, title: leaf.title}, 'playlist', ports(requests) as never, true, state => states.push(state));
  try {
    await picker.open();
    assert.deepEqual(states.at(-1)?.rows?.map((row: {id: string}) => row.id), ['destination-1']);
    assert.ok(requests.some(request => request.path.includes('/detail')), 'the title\'s own permission is still read');
  } finally {picker.close();}
});

test('J-13 Add to collection from a selection of picked titles: the same, never the detail\'s fence', async () => {
  const requests: {path: string; method?: string; body?: any}[] = [], states: any[] = [];
  const item = {kind: leaf.kind, id: leaf.id, libraryId: leaf.libraryId, capabilities: {play: true, queue: true, playlist: true, collection: true, myList: true, watched: true, favorite: true, download: false, edit: true, refresh: true, delete: true}};
  const picker = new LibraryPickerProducer([item] as never, 'collection', ports(requests) as never, true, state => states.push(state));
  try {
    await picker.open();
    assert.deepEqual(states.at(-1)?.rows?.map((row: {id: string}) => row.id), ['destination-1']);
  } finally {picker.close();}
});

test('J-12 Edit of picked titles: the preview issues the editing fence, with no page fence needed; a preview under a moved fence is newer and an older one cannot save', async () => {
  const preview = {variant: 'ready', serverId, viewerFence: 'c'.repeat(64), contentKind: 'movie', kind: 'item', total: 1,
    schema: [{field: 'studio', label: 'Studio', group: 'general', type: 'text', bulk: true, maxLength: 300}], singleItemFields: [], excludedFields: [],
    fields: {studio: {state: 'shared', distinctCount: 1, value: '', values: [{value: '', count: 1}], entries: [], locked: 0, truncated: false}},
    targets: [{kind: 'item', id: 'movie-1', libraryId: 'library', expectedRevision: 'd'.repeat(64)}]};
  let answer: unknown = preview;
  const bodies: any[] = [];
  const editor = createSelectionEditor({api: {request: async (path: string, _method?: string, body?: unknown) => {
    bodies.push({path, body});
    if (path === '/v1/metadata/bulk/preview') return structuredClone(answer);
    return {operationId: (body as {operationId: string}).operationId, results: [{kind: 'item', id: 'movie-1', ok: true, revision: 'e'.repeat(64)}], updated: 1, failed: 0};
  }} as never, events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId});
  const pick = {key: 'movie-1', item: {kind: 'movie', id: 'movie-1', libraryId: 'library', capabilities: {edit: true}}};
  const operation = {target: {mode: 'explicit', items: [pick], displayItems: [pick], context: {}}} as never;
  const ready = await editor.preview(operation);
  assert.deepEqual(bodies[0].body, {targets: [{kind: 'item', contentKind: 'movie', id: 'movie-1', libraryId: 'library'}]}, 'the picked title is sent as itself');
  const receipts = await editor.save(operation, ready, {fields: {studio: 'FX Studio'}, lists: {}, lockEdited: true});
  assert.equal(receipts[0]?.updated, 1, 'the edit is written');
  assert.deepEqual(bodies.find(entry => entry.path === '/v1/metadata/bulk')?.body.targets, [{kind: 'item', id: 'movie-1', expectedRevision: 'd'.repeat(64)}]);
  // Step 7: a preview under a moved fence is the newer answer; a save from the preview read before it is refused.
  answer = {...preview, viewerFence: 'f'.repeat(64)};
  assert.equal((await editor.preview(operation)).viewerFence, 'f'.repeat(64));
  await assert.rejects(editor.save(operation, ready, {fields: {studio: 'FX Studio'}, lists: {}, lockEdited: true}), (error: unknown) => error instanceof SelectionEditorError && error.code === 'metadata-preview-not-current');
});

for (const [name, args] of [['J-14 My List', {watchlist: true}], ['Watched', {watched: true}]] as const) {
  test(`${name} on picked titles sends their own IDs, never a prepared selection`, async () => {
    const bodies: any[] = [];
    const actions = createSelectionActions({events: {on: () => () => {}}, isCurrent: () => true, reportError: () => {}, requestId: async () => 'op-1',
      api: {request: async (_path: string, _method?: string, body?: unknown) => {bodies.push(body); return {jobId: 'job-1', command: 'personal-state', state: 'complete', total: 2, totalKnown: true, done: 2, failed: 0};}} as never});
    const item = (id: string) => ({key: id, item: {kind: 'episode', id, libraryId: 'library', capabilities: {myList: true, watched: true}}});
    const result = await actions.personal({target: {mode: 'explicit', context: {}, items: [item('e1'), item('e2')], displayItems: []}} as never, args);
    assert.equal(result.updated, 2);
    assert.deepEqual(bodies[0].selector, {items: {ids: ['e1', 'e2']}});
    assert.deepEqual(bodies[0].args, args);
  });
}

// Ruling B (step 7 item 4): the picked leaves' detail reads must agree. A leaf read under an older fence (the viewer's
// access was decided again while they were read) is read again once from the server; if the leaves still disagree,
// the picker shows picker-selection-changed. Before, any disagreement ended the open at once.
function leafApi(requests: {path: string; method?: string; body?: any}[], fenceFor: (read: number) => string) {
  let reads = 0;
  return {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    requests.push({path, method, body});
    if (path.includes('/detail')) {
      const id = decodeURIComponent(path.split('/')[3]!), answer = structuredClone(fixture);
      answer.item.id = id; answer.scope.itemId = id; answer.scope.viewerFence = fenceFor(++reads);
      return answer as T;
    }
    if (path === '/v1/home/destination-picker') return destinationPage((body as {kind: string}).kind) as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
}
const pickedLeaf = (id: string) => ({kind: leaf.kind, id, libraryId: leaf.libraryId, capabilities: {play: true, queue: true, playlist: true, collection: true, myList: true, watched: true, favorite: true, download: false, edit: true, refresh: true, delete: true}});
const leafPorts = (value: ReturnType<typeof leafApi>) => ({api: value, queries: testQueries(value, {serverId, viewerId: 'viewer'}), serverId, viewerId: '["local","account","profile"]',
  isCurrent: () => true, requestId: async () => 'request-1', pickerIdentity: identity});
test('B: a leaf read under an older fence is read again once, and the picker opens on the newer answer', async () => {
  const requests: {path: string; method?: string; body?: any}[] = [], states: any[] = [];
  const picker = new LibraryPickerProducer([pickedLeaf(leaf.id), pickedLeaf('second')] as never, 'collection',
    leafPorts(leafApi(requests, read => read === 1 ? 'c'.repeat(64) : 'd'.repeat(64))) as never, true, state => states.push(state));
  try {
    await picker.open();
    assert.deepEqual(states.at(-1)?.rows?.map((row: {id: string}) => row.id), ['destination-1']);
    assert.deepEqual(requests.filter(request => request.path.includes('/detail')).map(request => decodeURIComponent(request.path.split('/')[3]!)), [leaf.id, 'second', leaf.id]);
  } finally {picker.close();}
});
test('B: leaves that still disagree after the one re-read show picker-selection-changed, with no second re-read', async () => {
  const requests: {path: string; method?: string; body?: any}[] = [];
  const picker = new LibraryPickerProducer([pickedLeaf(leaf.id), pickedLeaf('second')] as never, 'collection',
    leafPorts(leafApi(requests, read => String(read % 10).repeat(64))) as never, true, () => {});
  try {
    await assert.rejects(picker.open(), /picker-selection-changed/);
    assert.equal(requests.filter(request => request.path.includes('/detail')).length, 3);
    assert.equal(requests.some(request => request.path === '/v1/home/destination-picker' || request.path.includes('destination')), false);
  } finally {picker.close();}
});
