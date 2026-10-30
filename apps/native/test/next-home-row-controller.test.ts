import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate} from 'node:timers/promises';
import {NativeHomeRowController} from '../src-next/screens/home/row-controller';

const row = {id: 'continue', viewerFence: 'home-viewer', title: 'Continue Watching', kind: 'continue', artworkShape: 'landscape',
  endpoint: '/v1/home/rows/continue', privacySensitivity: 'viewer', policyState: 'available', priority: 0, required: true, hideable: false, reorderable: true,
  critical: true, cursorCapable: true, entries: [{id: 'episode-real', libraryId: 'shows', kind: 'episode', title: 'Actual episode', playback: {itemId: 'episode-real'}}],
  total: 87, start: 0, limit: 60, hasMore: true, nextCursor: 'next', revision: {catalog: 1, viewer: 1}};
const pivot = {id: 'titles', labelKey: 'library.titles', entityKinds: ['movie', 'show'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['groupOrder', 'title'], filters: ['homeRow', 'library', 'playState'], paging: 'offset'};
const capabilities = {library: {}, pivots: [pivot], resolvedPivot: pivot,
  fields: [
    {id: 'homeRow', labelKey: 'browse.field.homeRow', type: 'string', operators: ['equals'], controlHint: 'text', complexity: 'advanced', cost: 'indexed', applicableKinds: ['movie', 'show']},
    {id: 'library', labelKey: 'browse.field.library', type: 'enum', operators: ['in'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show']},
    {id: 'playState', labelKey: 'browse.field.playState', type: 'enum', operators: ['equals'], allowedValues: ['in-progress'], controlHint: 'select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show']},
  ], sorts: ['groupOrder', 'title'].map(id => ({id, labelKey: `sort.${id}`, directions: ['asc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie', 'show'], requiresGroup: id === 'groupOrder'})),
  quickFilters: [], queryLimits: {maximumDepth: 8, maximumClauses: 32, maximumBytes: 32768, maximumSorts: 3, defaultLimit: 60, maximumLimit: 200, cursorTtlMs: 300000}};
const settle = async () => {for (let i = 0; i < 10; i++) await setImmediate();};

function fixture() {
  let current = true;
  const calls: {path: string; body: any}[] = [];
  const api = {async request<T>(path: string, _method?: string, body?: any): Promise<T> {
    calls.push({path, body});
    if (path.startsWith('/v1/home/rows/')) return row as T;
    if (path.startsWith('/v1/browse-capabilities')) return capabilities as T;
    if (path === '/v1/browse/counts') return {counts: body.queries.map((query: any) => ({id: query.id, total: query.id === 'all' ? 87 : 16, totalIsLowerBound: false, revision: 'count-revision'}))} as T;
    if (path === '/v1/browse') return {pivot: 'titles', applied: {query: body.query, sort: body.sort}, entries: body.range.start === 0 ? row.entries : [],
      displayTargets: body.range.start === 0 ? [{membershipId: 'show-membership', entryId: 'episode-real'}] : [],
      pageInfo: {start: body.range.start, total: 87, totalIsLowerBound: false, revision: 'browse-revision', hasMore: true, nextCursor: 'next'}, positionIndex: []} as T;
    throw new Error('unexpected_fixture_request');
  }};
  const controller = new NativeHomeRowController({api, rowId: 'continue', viewerFence: 'home-viewer', current: () => current,
    kinds: [{id: 'tv', libraryIds: ['shows']} ]});
  return {controller, calls, retire: () => {current = false;}};
}

test('See all preserves requested Continue membership, actual episode display identity and authoritative counts', async () => {
  const f = fixture();
  try {
    await f.controller.load(); assert.ifError(f.controller.getSnapshot().error); f.controller.collection.ensureRange(0, 0); await settle();
    assert.equal(f.controller.getSnapshot().row?.id, 'continue');
    assert.equal(f.controller.getSnapshot().counts[0].total, 87, 'the one loaded card never becomes the count');
    assert.deepEqual(f.controller.collection.itemAt(0), {membershipId: 'show-membership', entry: row.entries[0]});
    const page = f.calls.find(call => call.path === '/v1/browse')!.body;
    assert.equal(page.homeRowDisplay, 'continue'); assert.equal(page.limit, 60);
    assert.deepEqual(page.query.all[0], {field: 'homeRow', operator: 'equals', value: 'continue'});
    assert.equal(page.sort[0].field, 'groupOrder');
    assert.equal(f.controller.wholeGroup()?.pivot, 'items', 'real episode kinds choose the canonical items queue selector');
  } finally {f.controller.dispose();}
});

test('sort, filters and kind chips keep the immutable row and use actual authorized library IDs', async () => {
  const f = fixture();
  try {
    await f.controller.load(); assert.ifError(f.controller.getSnapshot().error);
    assert.equal(f.controller.selectKind('invented-library-kind'), false);
    f.controller.selectKind('tv');
    f.controller.setFilters([{field: 'playState', operator: 'equals', value: 'in-progress'}]);
    f.controller.setSort({field: 'title', direction: 'asc'});
    const source = f.controller.wholeGroup()!;
    assert.deepEqual(source.query, {all: [{field: 'homeRow', operator: 'equals', value: 'continue'}, {field: 'playState', operator: 'equals', value: 'in-progress'}, {field: 'library', operator: 'in', value: ['shows']}]});
    assert.deepEqual(source.sort, [{field: 'title', direction: 'asc'}]);
    assert.throws(() => f.controller.setFilters([{field: 'not-offered', operator: 'equals', value: true}]));
  } finally {f.controller.dispose();}
});

test('retired screens cannot start reads, expose a selector or publish another row', async () => {
  const f = fixture(); f.retire();
  await f.controller.load();
  assert.equal(f.calls.length, 0);
  assert.equal(f.controller.wholeGroup(), undefined);
  assert.equal(f.controller.selectKind('tv'), false);
  f.controller.dispose();
});

test('filter preview counts the real draft while leaving the retained grid unchanged until Apply', async () => {
  const f = fixture();
  try {
    await f.controller.load(); f.controller.collection.ensureRange(0, 0); await settle();
    const item = f.controller.collection.itemAt(0), before = f.calls.length;
    const draft = [{field: 'playState' as const, operator: 'equals' as const, value: 'in-progress'}];
    const counts = await f.controller.previewCounts(draft);
    assert.equal(counts[0].total, 87);
    const requests = f.calls.slice(before);
    assert.equal(requests.every(call => call.path === '/v1/browse/counts'), true);
    assert.deepEqual(requests[0].body.queries[0].query, {all: [{field: 'homeRow', operator: 'equals', value: 'continue'}, ...draft]});
    assert.deepEqual(f.controller.getSnapshot().filters, []);
    assert.strictEqual(f.controller.collection.itemAt(0), item, 'closing a draft needs no scroll or collection restoration');
    f.controller.setFilters(draft);
    assert.deepEqual(f.controller.getSnapshot().filters, draft);
    const cancelled = new AbortController(); cancelled.abort();
    await assert.rejects(f.controller.previewCounts(draft, cancelled.signal), {name: 'AbortError'});
  } finally {f.controller.dispose();}
});
