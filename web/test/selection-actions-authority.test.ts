import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {EntryPickerProducer} from '../src/shell/entry-picker.ts';
import {LibraryPickerProducer} from '../src/screens/browse/selection-picker.ts';
import {createSelectionEditor} from '../src/screens/browse/selection-editor.ts';
import {rebuiltMetadata} from '../../packages/client-core/test/fixtures/rebuilt-metadata.ts';
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

test('J-12 Edit of picked titles: the preview reads each picked title as itself and the save is the edit_items job for them', async () => {
  const server = rebuiltMetadata({'movie-1': {title: 'Film', fields: {studio: ''}}});
  const editor = createSelectionEditor({api: {request: async (path: string, method?: string, body?: unknown) => server.answer(path, method, body)} as never,
    events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId});
  const pick = {key: 'movie-1', item: {kind: 'movie', id: 'movie-1', libraryId: 'library', capabilities: {edit: true}}};
  const operation = {target: {mode: 'explicit', items: [pick], displayItems: [pick], context: {}}} as never;
  const ready = await editor.preview(operation);
  assert.deepEqual(server.reads, ['movie-1'], 'the picked title is read as itself');
  const receipts = await editor.save(operation, ready, {fields: {contentRating: 'PG'}, lists: {}, lockEdited: true});
  assert.equal(receipts[0]?.updated, 1, 'the edit is written');
  assert.deepEqual(server.jobs.map(job => job.target.items), [['movie-1']]);
});

for (const [name, args] of [['J-14 My List', {watchlist: true}], ['Watched', {watched: true}]] as const) {
  test(`${name} on picked titles sends their own IDs, never a prepared selection`, async () => {
    const bodies: any[] = [];
    const actions = createSelectionActions({events: {on: () => () => {}}, isCurrent: () => true, reportError: () => {}, requestId: async () => 'op-1',
      api: {request: async (path: string, _method?: string, body?: unknown) => {bodies.push({path, body}); return {};}} as never});
    const item = (id: string) => ({key: id, item: {kind: 'episode', id, libraryId: 'library', capabilities: {myList: true, watched: true}}});
    const result = await actions.personal({target: {mode: 'explicit', context: {}, items: [item('e1'), item('e2')], displayItems: []}} as never, args);
    assert.equal(result.updated, 2);
    // Each picked title on its own route (definition 06 rule 41: the answer is title by title).
    const state = 'watchlist' in args ? {inList: true} : {watched: true};
    assert.deepEqual(bodies, [{path: '/v1/items/e1/state', body: state}, {path: '/v1/items/e2/state', body: state}]);
  });
}

// Ruling B (step 7 item 4): the picked leaves' detail reads must agree. When they don't (the viewer's access was decided
// again while they were read, or the cache held an older page), every leaf is read again once from the server and only
// then compared; if they still disagree, the picker shows picker-selection-changed. Before, any disagreement ended the
// open at once.
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
