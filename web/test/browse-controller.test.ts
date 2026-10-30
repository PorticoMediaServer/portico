import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseController} from '../src/screens/browse/controller.ts';
const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true, paging: 'offset'};
const caps = {library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot,
 fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
 queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}};
const tick = () => new Promise<void>(resolve => setTimeout(resolve, 0));
async function settle() {for (let n = 0; n < 8; n++) await tick();}
function payload(start: number, total = 50_000, revision = 'revision-one') {
 return {pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null},
 entries: Array.from({length: Math.min(60, total - start)}, (_, n) => ({id: `movie-${start + n}`, kind: 'movie', title: `Movie ${start + n}`, libraryId: 'lib', navigation: {view: 'item'}})),
 pageInfo: {start, total, totalIsLowerBound: false, revision, hasMore: start + 60 < total, nextCursor: ''}, positionIndex: [{key: 'Z', index: 49_980}]};
}
test('Save view requires a real filter or sort change, not merely loaded capabilities', async () => {
 const predicate = {variant:'predicate',field: 'watched', operator: 'equals' as const, value: false};
 const scoped = {...caps, fields: [{id: 'watched', labelKey: 'watched', type: 'boolean', operators: ['equals'], controlHint: 'toggle', applicableKinds: ['movie']}]};
 let current = true;
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  return (path.includes('capabilities') ? scoped : payload((body as {range: {start: number}}).range.start, 1)) as T;
 }}, {libraryId: 'lib', pivot: 'movies', base: [predicate]}, () => current);
 try {
  assert.equal(controller.canSaveView(), false);
  await controller.load();
  assert.equal(controller.canSaveView(), false, 'fixed source membership is not a user filter');
  controller.setQuery([predicate]);assert.equal(controller.canSaveView(), true);
  controller.setQuery([]);assert.equal(controller.canSaveView(), false);
  controller.setQuery([], {field: 'title', direction: 'desc'});assert.equal(controller.canSaveView(), true);
  controller.setQuery([], {field: 'title', direction: 'asc'});assert.equal(controller.canSaveView(), false);
  controller.setQuery([predicate]);current = false;assert.equal(controller.canSaveView(), false);
 } finally {controller.dispose();}
});
test('Save view retains a changed secondary sort even when the first sort is the default', async () => {
 const scoped = {...caps, sorts: [...caps.sorts, {id: 'year', labelKey: 'sort.year', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}]};
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  return (path.includes('capabilities') ? scoped : payload((body as {range: {start: number}}).range.start, 1)) as T;
 }}, {libraryId: 'lib', pivot: 'movies', sorts: [{field: 'title', direction: 'asc'}, {field: 'year', direction: 'desc'}]}, () => true);
 try {await controller.load();assert.equal(controller.canSaveView(), true);} finally {controller.dispose();}
});
test('linked predicates and all server sorts survive paging and filter edits', async () => {
 const predicate = {variant:'predicate',field: 'watched', operator: 'equals' as const, value: false};
 const sort = [{field: 'title', direction: 'asc' as const}, {field: 'year', direction: 'desc' as const}];
 const scoped = {...caps, fields: [{id: 'watched', labelKey: 'watched', type: 'boolean', operators: ['equals'], controlHint: 'toggle', applicableKinds: ['movie']}],
  sorts: [...caps.sorts, {id: 'year', labelKey: 'sort.year', directions: ['asc', 'desc'], defaultDirection: 'desc', applicableKinds: ['movie']}]};
 const queries: unknown[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return scoped as T;
  const request = body as {range: {start: number}; sort: unknown; query: unknown};queries.push(request);
  return {...payload(request.range.start, 1), applied: {query: request.query, sort: request.sort, seek: null}} as T;
 }}, {libraryId: 'lib', pivot: 'movies', predicates: [predicate], sorts: sort}, () => true);
 try {
  await controller.load();await settle();
  assert.deepEqual(controller.query().query, predicate);
  assert.deepEqual(controller.query().sort, sort);
  controller.setQuery([]);await settle();
  assert.deepEqual(controller.query().sort, sort);
  assert.equal(controller.query().query, undefined);
  controller.setQuery([], {field: 'year', direction: 'asc'});await settle();
  assert.deepEqual(controller.query().sort, [{field: 'year', direction: 'asc'}]);
  assert.ok(queries.length <= 3);
 } finally {controller.dispose();}
});
test('sparse distant windows never scan intermediate pages and later ranges carry the actual revision', async () => {
 const requests: {start: number; revision?: string}[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;
  const range = (body as {range: {start: number; revision?: string}}).range; requests.push(range);return payload(range.start) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {await controller.load();await settle();
  assert.equal(controller.collection.getSnapshot().total, 50_000);assert.equal(controller.collection.itemAt(0)?.entry.id, 'movie-0');
  controller.collection.ensureRange(49_980, 49_990);await settle();
  assert.equal(controller.collection.itemAt(49_980)?.entry.id, 'movie-49980');assert.ok(requests.length <= 5);
  assert.ok(requests.every(r => r.start < 120 || r.start > 49_800));assert.ok(requests.slice(1).every(r => r.revision === 'revision-one'));
  assert.ok(controller.collection.resident().items <= 360);
 } finally {controller.dispose();}
});
test('an authority change discards an in-flight result and disposal cancels requests', async () => {
 let current = true;let resolve!: (v: unknown) => void;let signal: AbortSignal | undefined;
 const controller = new BrowseController({async request<T>(path: string, _method?: string, _body?: unknown, provided?: AbortSignal) {
  if (path.includes('capabilities')) return caps as T;signal = provided;return await new Promise<unknown>(r => {resolve = r;}) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => current);
 await controller.load();await tick();current = false;controller.dispose();assert.equal(signal?.aborted, true);resolve(payload(0));await tick();
 assert.equal(controller.collection.itemAt(0), undefined);
});
test('a different revision cannot enter the displayed collection', async () => {
 let changed = false;
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;const start = (body as {range: {start: number}}).range.start;
  return payload(start, 500, changed ? 'revision-two' : 'revision-one') as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {await controller.load();await settle();const old = controller.collection.itemAt(0)!.entry;assert.equal(old.id, 'movie-0');assert(controller.contains(old));
  changed = true;controller.collection.ensureRange(120, 125);await settle();assert.equal(controller.collection.itemAt(120), undefined);assert.equal(controller.collection.getSnapshot().status, 'error');
  assert.equal(controller.getSnapshot().revision, undefined);assert.equal(controller.contains(old), false);
 } finally {controller.dispose();}
});

test('applied query changes retire old borrowed rows before the replacement page arrives', async () => {
 let resolve!: (value: unknown) => void;
 const controller = new BrowseController({async request<T>(path: string) {
  return path.includes('capabilities') ? caps as T : await new Promise<unknown>(r => {resolve = r;}) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  assert.equal(controller.getSnapshot().revision, undefined);
  await controller.load();await tick();assert.equal(controller.getSnapshot().revision, undefined);
  resolve(payload(0, 1, 'opaque:first'));await settle();
  const old = controller.collection.itemAt(0)!.entry, generation = controller.getSnapshot().queryRevision;
  assert.equal(controller.getSnapshot().revision, 'opaque:first');assert(controller.contains(old));assert.equal(controller.contains({...old}), false);
  controller.setQuery([], {field: 'title', direction: 'desc'});await tick();
  assert.equal(controller.getSnapshot().revision, undefined);
  assert(controller.getSnapshot().queryRevision > generation);
  assert.equal(controller.collection.itemAt(0)!.entry, old, 'retained paint is not current action membership');
  assert.equal(controller.contains(old), false);
  resolve(payload(0, 1, 'opaque:second'));await settle();
  const current = controller.collection.itemAt(0)!.entry;
  assert.notEqual(current, old);assert(controller.contains(current));assert.equal(controller.contains(old), false);
  assert.equal(controller.getSnapshot().revision, 'opaque:second');
 } finally {controller.dispose();}
});

test('stale continuation closes old actions until explicit refresh and never drops the token on retry', async () => {
 let fresh = false;
 const ranges: {start: number; revision?: string}[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;
  const range = (body as {range: {start: number; revision?: string}}).range;ranges.push(range);
  if (!fresh && range.start > 0) throw Object.assign(new Error('stale'), {code: 'stale_continuation'});
  return payload(range.start, 180, fresh ? 'new-token' : 'old-token') as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await settle();const old = controller.collection.itemAt(0)!.entry;
  controller.collection.ensureRange(60, 65);await settle();
  assert.equal(controller.getSnapshot().revision, undefined);assert.equal(controller.contains(old), false);
  const reads = ranges.length;controller.collection.retry();await settle();assert.equal(ranges.length, reads, 'same stale source must not issue an unversioned read');
  fresh = true;controller.retry();await settle();
  assert.equal(ranges[reads].revision, undefined, 'only explicit refresh opens a new source');
  assert.equal(controller.getSnapshot().revision, 'new-token');assert.equal(controller.contains(old), false);
 } finally {controller.dispose();}
});

test('captured source and applied predicates cannot change through caller-owned draft objects', async () => {
 const value = ['Action'], predicate = {variant:'predicate',field: 'genre', operator: 'in' as const, value};
 const source = {libraryId: 'lib', pivot: 'movies', predicates: [predicate]};
 const scoped = {...caps, fields: [{id: 'genre', labelKey: 'genre', type: 'enum', operators: ['in'], controlHint: 'facet-multi-select', applicableKinds: ['movie']}]};
 let current = true;
 const controller = new BrowseController({async request<T>(path: string) {return (path.includes('capabilities') ? scoped : payload(0, 1)) as T;}}, source, () => current);
 try {
  value.push('Comedy');source.libraryId = 'other-library';
  await controller.load();await settle();
  assert.equal(controller.source.libraryId, 'lib');assert.deepEqual(controller.query().query, {variant:'predicate',field: 'genre', operator: 'in', value: ['Action']});
  const staged = {variant:'predicate',field: 'genre', operator: 'in' as const, value: ['Drama']};controller.setQuery([staged]);staged.value.push('Horror');await settle();
  assert.deepEqual(controller.query().query, {variant:'predicate',field: 'genre', operator: 'in', value: ['Drama']});
  const entry = controller.collection.itemAt(0)!.entry;assert(controller.contains(entry));current = false;assert.equal(controller.contains(entry), false);
 } finally {controller.dispose();}
});
test('Retry after a revision change obtains a fresh source window rather than repeating the old revision', async () => {
 let fresh = false;
 const requests: {start: number; revision?: string; fresh: boolean; signal?: AbortSignal}[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown, signal?: AbortSignal) {
  if (path.includes('capabilities')) return caps as T;
  const range = (body as {range: {start: number; revision?: string}}).range;requests.push({...range, fresh, signal});
  const page = payload(range.start, 180, fresh || range.start !== 0 ? 'revision-two' : 'revision-one');
  return {...page, entries: page.entries.map(entry => fresh ? {...entry, id: 'fresh-' + entry.id} : entry)} as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {await controller.load();await settle();controller.collection.ensureRange(60, 65);await settle();
  assert.equal(controller.collection.getSnapshot().status, 'error');fresh = true;controller.retry();await settle();
  assert.equal(requests.find(request => request.fresh)?.revision, undefined);
  assert.equal(controller.collection.itemAt(0)?.entry.id, 'fresh-movie-0');
  controller.collection.ensureRange(60, 65);await settle();assert.equal(controller.collection.itemAt(60)?.entry.id, 'fresh-movie-60');
  const continuations = requests.filter(request => request.fresh && request.start > 0 && !request.signal?.aborted);
  assert.ok(continuations.length > 0);assert.ok(continuations.every(request => request.revision === 'revision-two'));
 } finally {controller.dispose();}
});
test('Home display pages, facet queries and batch counts keep the immutable source membership', async () => {
 const membership = {variant:'predicate',field: 'homeRow', operator: 'equals' as const, value: 'continue'};
 const scopedCaps = {...caps, library: {id: '', kind: '', name: ''}, fields: [
  {id: 'homeRow', labelKey: 'browse.field.homeRow', type: 'identity-set', operators: ['equals'], controlHint: 'text', applicableKinds: ['movie']},
  {id: 'library', labelKey: 'browse.field.library', type: 'enum', operators: ['in'], controlHint: 'facet-multi-select', applicableKinds: ['movie'], facetSource: {endpoint: '/ignored', field: 'library'}}],
  sorts: [...caps.sorts, {id: 'groupOrder', labelKey: 'sort.groupOrder', directions: ['asc'], defaultDirection: 'asc', requiresGroup: true, applicableKinds: ['movie']}]};
 const requests: {path: string; body: unknown}[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  requests.push({path, body});
  if (path.includes('capabilities')) return scopedCaps as T;
  if (path.includes('facets')) return {field: 'library', total: 1, values: [{value: 'lib', label: 'Movies', count: 80}]} as T;
  if (path.includes('counts')) return {counts: [{id: 'draft', total: 80, totalIsLowerBound: false, revision: 'actual-count-revision'}]} as T;
  return payload((body as {range: {start: number}}).range.start, 80) as T;
 }}, {libraryId: '*', pivot: 'movies', homeRowId: 'continue', base: [membership], sort: {field: 'groupOrder', direction: 'asc'}}, () => true);
 try {await controller.load();await settle();const stop = new AbortController();
  await controller.facets('library', '', [], stop.signal);await controller.count([], stop.signal);
  const page = requests.find(r => r.path.endsWith('/browse'))!.body as {homeRowDisplay: string;query: unknown};
  assert.equal(page.homeRowDisplay, 'continue');assert.deepEqual(page.query, membership);
  const facet = requests.find(r => r.path.includes('facets'))!;assert.deepEqual(JSON.parse(new URL(facet.path,'https://local.invalid').searchParams.get('query')!), membership);
  const count = requests.find(r => r.path.includes('counts'))!.body as {queries: {query: unknown}[]};assert.deepEqual(count.queries[0].query, membership);
 } finally {controller.dispose();}
});
test('type counts are one server batch with exact lower bounds, never sums of library facets', async () => {
 const membership = {variant:'predicate',field: 'homeRow', operator: 'equals' as const, value: 'continue'};
 const type = {variant:'predicate',field: 'library', operator: 'in' as const, value: ['one', 'two']};
 const scoped = {...caps, fields: [
  {id: 'homeRow', labelKey: 'homeRow', type: 'identity-set', operators: ['equals'], controlHint: 'text', applicableKinds: ['movie']},
  {id: 'library', labelKey: 'library', type: 'enum', operators: ['in'], controlHint: 'facet-multi-select', applicableKinds: ['movie']}],
  sorts: [...caps.sorts, {id: 'groupOrder', labelKey: 'sort.groupOrder', directions: ['asc'], defaultDirection: 'asc', requiresGroup: true, applicableKinds: ['movie']}]};
 const counts: unknown[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return scoped as T;
  if (path.includes('counts')) {counts.push(body);return {counts: [{id: 'all', total: 10_007, totalIsLowerBound: false, revision: 'actual'}, {id: 'movie', total: 10_000, totalIsLowerBound: true, revision: 'actual'}]} as T;}
  return payload((body as {range: {start: number}}).range.start, 80) as T;
 }}, {libraryId: '*', pivot: 'movies', base: [membership], sort: {field: 'groupOrder', direction: 'asc'}}, () => true);
 try {
  await controller.load();await settle();
  const result = await controller.counts([{id: 'all', predicates: []}, {id: 'movie', predicates: [type]}], new AbortController().signal);
  assert.equal(counts.length, 1);
  assert.deepEqual((counts[0] as {queries: unknown}).queries, [{id: 'all', query: membership}, {id: 'movie', query: {variant:'group',all: [membership, type]}}]);
  assert.deepEqual(result.map(count => [count.total, count.totalIsLowerBound]), [[10_007, false], [10_000, true]]);
 } finally {controller.dispose();}
});

// Authored UNRUN: a delayed filter draft is admitted by the original query,
// while an ordinary first-page publication is not a replacement opening/query.
test('a retained query callback cannot overwrite a successor query or a forged witness', async () => {
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;
  const request = body as {range: {start: number}; sort: unknown; query?: unknown};
  return {...payload(request.range.start, 1), applied: {query: request.query ?? null, sort: request.sort, seek: null}} as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await settle();const original = controller.getSnapshot();
  assert.equal(controller.setQuery([], {field: 'title', direction: 'desc'}, {...original}), false, 'only an issued query can witness a draft');
  assert.equal(controller.setQuery([], {field: 'title', direction: 'desc'}, original), true);
  assert.equal(controller.setQuery([], {field: 'title', direction: 'asc'}, original), false);
  assert.deepEqual(controller.query().sort, [{field: 'title', direction: 'desc'}]);
 } finally {controller.dispose();}
});

test('a valid original draft survives an ordinary accepted-page publication', async () => {
 let resolve!: (value: unknown) => void;
 const controller = new BrowseController({async request<T>(path: string) {
  return path.includes('capabilities') ? caps as T : await new Promise<unknown>(value => {resolve = value;}) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await tick();const original = controller.getSnapshot();
  assert.equal(original.revision, undefined);resolve(payload(0, 1, 'accepted:original'));await settle();
  assert.notEqual(controller.getSnapshot(), original);assert.equal(controller.getSnapshot().revision, 'accepted:original');
  assert.equal(controller.acceptsQuery(original), true);
  assert.equal(controller.setQuery([], {field: 'title', direction: 'desc'}, original), true);
 } finally {controller.dispose();}
});

test('query input capture cannot re-enter and replace the query accepted in its getter', async () => {
 const scoped = {...caps, fields: [{id: 'genre', labelKey: 'genre', type: 'enum', operators: ['in'], controlHint: 'facet-multi-select', applicableKinds: ['movie']}]};
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return scoped as T;
  const request = body as {range: {start: number}; sort: unknown; query?: unknown};
  return {...payload(request.range.start, 1), applied: {query: request.query ?? null, sort: request.sort, seek: null}} as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await settle();const original = controller.getSnapshot();
  const input = {variant:'predicate',field: 'genre', operator: 'in' as const, get value() {
   controller.setQuery([], {field: 'title', direction: 'desc'}, original);return ['Drama'];
  }};
  assert.equal(controller.setQuery([input], undefined, original), false);
  assert.deepEqual(controller.query().sort, [{field: 'title', direction: 'desc'}]);
  assert.equal(controller.query().query, undefined);
 } finally {controller.dispose();}
});

test('retiring the original browse opening refuses a query before inspecting its input', async () => {
 let live = true, reads = 0;
 const controller = new BrowseController({async request<T>(path: string) {return (path.includes('capabilities') ? caps : payload(0, 1)) as T;}},
  {libraryId: 'lib', pivot: 'movies'}, () => live);
 try {
  await controller.load();await settle();const original = controller.getSnapshot();live = false;
  const input = {variant:'predicate',field: 'genre', operator: 'in' as const, get value() {reads++;return ['Drama'];}};
  assert.equal(controller.setQuery([input], undefined, original), false);assert.equal(reads, 0);
 } finally {controller.dispose();}
});

test('publication listeners cannot replace a newer accepted query with the older reset', async () => {
 const requested: string[] = [];
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;
  const request = body as {range: {start: number}; sort: {direction: string}[]; query?: unknown};
  requested.push(request.sort[0]!.direction);
  return {...payload(request.range.start, 1), applied: {query: request.query ?? null, sort: request.sort, seek: null}} as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 let unsubscribe = () => {};
 try {
  await controller.load();await settle();requested.length = 0;let replaced = false;
  unsubscribe = controller.subscribe(() => {
   const shown = controller.getSnapshot();
   if (!replaced && shown.sort?.direction === 'desc') {replaced = true;controller.setQuery([], {field: 'title', direction: 'asc'}, shown);}
  });
  controller.setQuery([], {field: 'title', direction: 'desc'}, controller.getSnapshot());await settle();
  assert.equal(replaced, true);assert.deepEqual(requested, ['asc']);
  assert.deepEqual(controller.query().sort, [{field: 'title', direction: 'asc'}]);
 } finally {unsubscribe();controller.dispose();}
});


test('search-unavailable publication reaches the live controller and clears immediately on query or viewer retirement', async () => {
 let current = true, refuse = true, published = 0;
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if(path.includes('capabilities')) return caps as T;
  const request = body as {range: {start:number};query?:unknown;sort?:unknown};
  return {...payload(request.range.start, 1), applied:{query:request.query ?? null,sort:request.sort,seek:null},
    ...(refuse ? {wholeSelectionUnavailable:{reason:'search_not_bounded'}} : {})} as T;
 }}, {libraryId:'lib',pivot:'movies'}, () => current);
 const off = controller.subscribe(() => {published++;});
 try {
  await controller.load(); await settle();
  assert.deepEqual(controller.wholeSelectionUnavailable(), {reason:'search_not_bounded'});
  assert.equal(controller.collection.itemAt(0)?.entry.id, 'movie-0'); assert.ok(published > 2);
  refuse = false; controller.setQuery([], {field:'title',direction:'desc'});
  assert.equal(controller.wholeSelectionUnavailable(), undefined); await settle();
  assert.equal(controller.wholeSelectionUnavailable(), undefined);
  refuse = true; controller.setQuery([], {field:'title',direction:'asc'}); await settle();
  assert.ok(controller.wholeSelectionUnavailable()); current = false;
  assert.equal(controller.wholeSelectionUnavailable(), undefined);
 } finally {off();controller.dispose();}
});

test('a first page refused as stale while a library imports is read once more, and a second refusal is the error, never a loop', async () => {
 let refusals = 2, pages = 0;
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  if (path.includes('capabilities')) return caps as T;
  pages++;
  if (refusals > 0) {refusals--; throw Object.assign(new Error('Reading projection changed'), {status: 412, code: 'revision_mismatch'});}
  return payload((body as {range: {start: number}}).range.start, 1) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await settle();
  assert.equal(pages, 2, 'exactly one automatic re-read');
  assert.ok(controller.getSnapshot().error, 'the second refusal is shown as this library\'s error');
  await settle();assert.equal(pages, 2, 'no loop');
  controller.retry();await settle();
  assert.equal(controller.getSnapshot().revision, 'revision-one', 'an explicit Try again reads it');
  refusals = 1;controller.setQuery([], {field: 'title', direction: 'desc'});await settle();
  assert.equal(controller.getSnapshot().revision, 'revision-one', 'a new query earns its own single re-read');
 } finally {controller.dispose();}
});
test('P2-74 E-web-02: after the list re-reads under a press, the same title is found in the fresh list', async () => {
 const predicate = {variant:'predicate',field: 'watched', operator: 'equals' as const, value: false};
 const scoped = {...caps, fields: [{id: 'watched', labelKey: 'watched', type: 'boolean', operators: ['equals'], controlHint: 'toggle', applicableKinds: ['movie']}]};
 const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
  return (path.includes('capabilities') ? scoped : payload((body as {range: {start: number}}).range.start, 3)) as T;
 }}, {libraryId: 'lib', pivot: 'movies'}, () => true);
 try {
  await controller.load();await settle();
  const pressed = controller.collection.itemAt(1)!.entry;
  assert.equal(controller.contains(pressed), true);
  controller.setQuery([predicate]);
  const waiting = controller.settled(new AbortController().signal, 5000);
  await settle();await waiting;
  assert.equal(controller.contains(pressed), false, 'the row object the press held is gone');
  const fresh = controller.resident(pressed);
  assert.ok(fresh && fresh !== pressed && fresh.id === pressed.id, 'the same title, from the fresh list');
  assert.equal(controller.contains(fresh!), true, 'and it is current, so Play acts on it');
  assert.equal(controller.resident({...pressed, id: 'gone'}), undefined, 'a title that left the list is not invented');
 } finally {controller.dispose();}
});
