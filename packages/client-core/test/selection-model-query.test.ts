import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
  SelectionInputError, SelectionModel, selectionActions, selectionCapabilities, selectionKey, selectionQuerySelector,
  type QuerySelectionTarget, type SelectionCapabilities, type SelectionContext, type SelectionItem,
  type SelectionQuery, type SelectionQueryType, type SelectionWindow,
} from '../src/selection-model/index.ts';
import {summarizeQuerySelection} from '../src/selection-model/query-summary.ts';

const context: SelectionContext = {
  scope: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', viewId: 'filtered-movies', privacyFence: 'private'},
  revision: {catalogRevision: '10', viewerRevision: '20'},
};
const offers: SelectionCapabilities = selectionCapabilities('movie', {play: true, queue: true, playlist: true, myList: true, watched: true, edit: true, delete: true});
function fact(kind: SelectionQueryType['kind'], count: number, libraryId = 'library', capabilities = selectionCapabilities(kind, offers)): SelectionQueryType {
  return {kind, count, libraries: [{libraryId, count}], capabilityCounts: Object.fromEntries(Object.entries(capabilities).map(([name, offered]) => [name, offered ? count : 0])) as SelectionQueryType['capabilityCounts']};
}
function query(types: readonly SelectionQueryType[], id = 'query'): SelectionQuery {
  const summary = summarizeQuerySelection(types, null, []);
  return {id, selector: {query: {libraryId: '*', pivot: 'all', filter: {op: 'eq', field: 'favorite', value: true}, sort: [{field: 'title', direction: 'asc'}]}}, total: summary.count, catalogRevision: '10', summary: {types, library: summary.library, every: summary.every}};
}
const item = (id: string, kind: SelectionQueryType['kind'] = 'movie', libraryId = 'library', capabilities = selectionCapabilities(kind, offers)): SelectionItem => ({kind, id, libraryId, capabilities});
function registerQuery(model: SelectionModel, descriptor: SelectionQuery) {
  const result = model.registerQuery(model.captureContext(), descriptor); assert(result.ok); return result;
}
function registerWindow(model: SelectionModel, items: readonly SelectionItem[], options: Partial<SelectionWindow> = {}) {
  const result = model.registerWindow(model.captureContext(), {id: 'window', sectionId: 'row', sectionOrder: 0, start: 0, queryId: 'query', items, ...options});
  assert(result.ok); return result;
}
const key = (entry: SelectionItem) => selectionKey(context.scope, entry);
function target(model: SelectionModel): QuerySelectionTarget {
  const value = model.getSnapshot().target; assert(value?.mode === 'query'); return value;
}

test('50,000 results stay a query plus bounded aggregates and never become 50,000 ids', () => {
  const model = new SelectionModel(context);
  registerQuery(model, query([fact('movie', 50_000)]));
  const entries = [item('one'), item('two'), item('three')];
  registerWindow(model, entries);
  model.selectLoaded();
  assert.equal(model.getSnapshot().summary.count, 3);
  assert.equal(model.getSnapshot().target?.mode, 'explicit', 'loaded is distinct from an explicit whole-view intent');
  model.selectWholeQuery('query');
  const selected = target(model);
  assert.equal(selected.total, 50_000); assert.equal(selected.sourceTotal, 50_000);
  assert.deepEqual(selected.refinement, {kind: null, excludedItems: []});
  assert.deepEqual(selected.expected, {catalogRevision: '10'});
  assert.equal(model.getSnapshot().selectedKeys.length, 0);
  assert.equal(model.getSnapshot().registeredItemCount, 3);
  assert.equal(model.isSelected(key(entries[1])), true);
  assert.equal(model.isSelected(key(item('unloaded'))), false, 'membership is never invented for an unknown result');
  assert.equal(selectionQuerySelector(selected).ok, true);
  const actions = selectionActions(selected.summary, {platform: 'desktop-web', isOwner: true, canEditMetadata: true});
  assert.equal(actions.edit.state, 'ready'); assert.deepEqual(actions.delete, {state: 'explain', reason: 'delete-limit'});
  model.dispose();
});

test('whole-view deselection survives eviction and must not be silently serialized as the source query', () => {
  const model = new SelectionModel(context);
  registerQuery(model, query([fact('movie', 50_000)]));
  const a = item('a'), b = item('b');
  const first = registerWindow(model, [a, b]);
  model.selectWholeQuery('query');
  assert(model.toggle('window', key(a)).ok);
  assert.equal(model.isSelected(key(a)), false); assert.equal(model.isSelected(key(b)), true);
  assert.equal(target(model).total, 49_999); assert.equal(target(model).sourceTotal, 50_000);
  assert.deepEqual(target(model).refinement.excludedItems.map(entry => entry.key), [key(a)]);
  assert.deepEqual(selectionQuerySelector(target(model)), {ok: false, reason: 'query-refinement-required'});
  first.unregister();
  assert.equal(target(model).total, 49_999);
  registerWindow(model, [item('later')], {id: 'later', start: 40_000});
  registerWindow(model, [a], {id: 'returned'});
  assert.equal(model.isSelected(key(a)), false);
  model.toggle('returned', key(a));
  assert.equal(target(model).total, 50_000);
  assert.equal(target(model).refinement.excludedItems.length, 0);
  assert.equal(selectionQuerySelector(target(model)).ok, true);
  model.dispose();
});

test('Keep only narrows aggregate types/libraries/offers without enumerating or resurrecting a trimmed type', () => {
  const model = new SelectionModel(context);
  registerQuery(model, query([fact('movie', 30_000, 'movies'), fact('song', 20_000, 'music')]));
  const movie = item('m', 'movie', 'movies'), song = item('s', 'song', 'music');
  registerWindow(model, [movie, song]);
  model.selectWholeQuery('query');
  assert.equal(target(model).summary.every.watched, false);
  model.toggle('window', key(movie)); model.toggle('window', key(song));
  assert.equal(target(model).total, 49_998);
  model.keepOnly('movie');
  const selected = target(model);
  assert.equal(selected.total, 29_999); assert.equal(selected.sourceTotal, 50_000);
  assert.deepEqual(selected.summary.types, [{kind: 'movie', count: 29_999}]);
  assert.deepEqual(selected.summary.library, {kind: 'single', libraryId: 'movies'});
  assert.equal(selected.summary.every.watched, true);
  assert.deepEqual(selected.refinement.excludedItems.map(entry => entry.item.kind), ['movie']);
  assert.equal(selected.refinement.kind, 'movie');
  assert.equal(model.isSelected(key(song)), false);
  assert.deepEqual(model.toggle('window', key(song)), {ok: false, reason: 'not-query-member'});
  assert.deepEqual(selectionQuerySelector(selected), {ok: false, reason: 'query-refinement-required'});
  model.keepOnly('song');
  assert.equal(model.getSnapshot().target, null, 'Keep only trims current membership, it is not a switch to an earlier set');
  model.dispose();
});

test('excluding the only denied offer updates all-of applicability using exact counts', () => {
  const model = new SelectionModel(context);
  const row = fact('movie', 50_000);
  const types = [{...row, capabilityCounts: {...row.capabilityCounts, queue: 49_999}}];
  registerQuery(model, query(types));
  const denied = item('denied', 'movie', 'library', {...offers, queue: false});
  registerWindow(model, [denied]); model.selectWholeQuery('query');
  assert.equal(target(model).summary.every.queue, false);
  model.toggle('window', key(denied));
  assert.equal(target(model).total, 49_999); assert.equal(target(model).summary.every.queue, true);
  const rule = selectionActions(target(model).summary, {platform: 'desktop-web', isOwner: false, canEditMetadata: false});
  assert.equal(rule['play-next'].state, 'ready');
  model.dispose();
});

test('query exclusions are bounded and failed refinements do not mutate the current target', () => {
  const model = new SelectionModel(context, {maxQueryExclusions: 1});
  registerQuery(model, query([fact('movie', 50_000)]));
  const a = item('a'), b = item('b'); registerWindow(model, [a, b]); model.selectWholeQuery('query');
  model.toggle('window', key(a)); const before = model.getSnapshot();
  assert.deepEqual(model.toggle('window', key(b)), {ok: false, reason: 'exclusion-limit'});
  assert.equal(model.getSnapshot(), before);
  model.toggle('window', key(a)); assert(model.toggle('window', key(b)).ok);
  model.dispose();
});

test('bounded Shift-range restores query members and rejects holes or unrelated windows', () => {
  const model = new SelectionModel(context);
  registerQuery(model, query([fact('movie', 50_000)]));
  const entries = [item('a'), item('b'), item('c')]; registerWindow(model, entries); model.selectWholeQuery('query');
  model.toggle('window', key(entries[0])); model.toggle('window', key(entries[1]));
  assert.equal(target(model).total, 49_998);
  assert(model.toggle('window', key(entries[2]), true).ok);
  assert.equal(target(model).total, 49_999);
  assert.equal(model.isSelected(key(entries[1])), true);
  const other = item('not-a-member'); registerWindow(model, [other], {id: 'unbound', sectionId: 'other', queryId: undefined});
  assert.deepEqual(model.toggle('unbound', key(other)), {ok: false, reason: 'not-query-member'});
  assert.equal(target(model).total, 49_999);
  model.dispose();
});

test('selecting loaded content after a query is an explicit bounded target, and deselecting all ends the mode', () => {
  const model = new SelectionModel(context);
  registerQuery(model, query([fact('movie', 1)])); const a = item('a'); registerWindow(model, [a]);
  model.selectWholeQuery('query'); model.toggle('window', key(a));
  assert.equal(model.getSnapshot().active, false); assert.equal(model.getSnapshot().target, null);
  model.selectWholeQuery('query'); model.selectLoaded();
  assert.equal(model.getSnapshot().target?.mode, 'explicit'); assert.equal(model.getSnapshot().summary.count, 1);
  model.dispose();
});

test('query replacement clears its windows and selection; stale cleanups cannot remove the new descriptor', () => {
  const model = new SelectionModel(context);
  const first = registerQuery(model, query([fact('movie', 50_000)]));
  registerWindow(model, [item('old')]); model.selectWholeQuery('query');
  const operation = model.captureOperation(), read = model.captureContext(); assert(operation);
  const replacement = registerQuery(model, query([fact('movie', 10)]));
  assert.equal(model.getSnapshot().target, null); assert.equal(model.getSnapshot().registeredWindowCount, 0);
  assert.equal(model.isContextCurrent(read), false); assert.equal(model.isOperationCurrent(operation), false);
  first.unregister(); assert.equal(model.getQuery('query')?.total, 10);
  registerWindow(model, [item('new')]); model.selectWholeQuery('query'); replacement.unregister();
  assert.equal(model.getQuery('query'), null); assert.equal(model.getSnapshot().target, null);
  assert.equal(model.getSnapshot().registeredWindowCount, 0);
  model.dispose();
});

test('same query descriptor replacement keeps selection but old unregister remains harmless', () => {
  const model = new SelectionModel(context);
  const descriptor = query([fact('movie', 50_000)]);
  const old = registerQuery(model, descriptor); registerWindow(model, [item('a')]); model.selectWholeQuery('query');
  const current = registerQuery(model, descriptor); old.unregister();
  assert.equal(target(model).total, 50_000); assert.equal(model.getSnapshot().registeredWindowCount, 1);
  current.unregister(); assert.equal(model.getSnapshot().target, null);
  model.dispose();
});

test('query aggregate and revision facts are validated before registration', () => {
  const model = new SelectionModel(context);
  const good = query([fact('movie', 50_000)]);
  for (const bad of [
    {...good, catalogRevision: 'stale'}, {...good, total: 49_999},
    {...good, summary: {...good.summary, every: {...good.summary.every, play: false}}},
    {...good, summary: {...good.summary, types: [{...good.summary.types[0], libraries: [{libraryId: 'library', count: 1}]}]}},
    {...good, summary: {...good.summary, types: [{...good.summary.types[0], capabilityCounts: {...good.summary.types[0].capabilityCounts, play: 50_001}}]}},
    {...good, selector: {items: {ids: ['a']}}},
    {...good, selector: {query: {libraryId: '*', pivot: 'all', excludedIds: ['a']}}},
    {...good, selector: {guide: {sourceId: 's'}}},
  ]) assert.throws(() => registerQuery(model, bad as SelectionQuery), error => error instanceof SelectionInputError && error.code === 'invalid-query');
  assert.equal(model.getQuery('query'), null);
  model.dispose();
});

test('channel/person whole-query aggregates and channel membership in a catalogue selector are rejected', () => {
  const model = new SelectionModel(context);
  const good = query([fact('movie', 1)]);
  for (const kind of ['channel', 'person']) {
    const bad = {...good, summary: {...good.summary, types: [{...good.summary.types[0], kind}]}};
    assert.throws(() => registerQuery(model, bad as SelectionQuery), SelectionInputError);
  }
  registerQuery(model, good);
  const channel: SelectionItem = {kind: 'channel', sourceId: 'source', channelId: 'channel', capabilities: selectionCapabilities('channel', {play: true})};
  assert.throws(() => registerWindow(model, [channel]), SelectionInputError);
  assert.equal(model.getSnapshot().registeredWindowCount, 0);
  model.dispose();
});

test('loaded query facts must fit each kind/library and true/false capability count, including evicted exclusions', () => {
  const model = new SelectionModel(context);
  const row = fact('movie', 2);
  registerQuery(model, query([{...row, capabilityCounts: {...row.capabilityCounts, queue: 1}}]));
  const denied = item('denied', 'movie', 'library', {...offers, queue: false});
  const registration = registerWindow(model, [denied]); model.selectWholeQuery('query'); model.toggle('window', key(denied)); registration.unregister();
  const before = model.getSnapshot();
  assert.throws(() => registerWindow(model, [item('second-denied', 'movie', 'library', {...offers, queue: false})]), SelectionInputError);
  assert.throws(() => registerWindow(model, [item('other-library', 'movie', 'other')]), SelectionInputError);
  assert.throws(() => registerWindow(model, [item('wrong-kind', 'song')]), SelectionInputError);
  assert.equal(model.getSnapshot(), before);
  registerWindow(model, [item('permitted')]); assert.equal(target(model).total, 1);
  model.dispose();
});

test('selector filters are bounded immutable copies and cycles/depth bombs are rejected', () => {
  const model = new SelectionModel(context);
  const descriptor = query([fact('movie', 50_000)]);
  const filter = {op: 'and', children: [{op: 'eq', field: 'favorite', value: true}]};
  const selector = {query: {libraryId: '*', pivot: 'all', filter}};
  registerQuery(model, {...descriptor, selector});
  filter.children[0].value = false;
  const saved = model.getQuery('query'); assert(saved && 'query' in saved.selector);
  assert.equal((saved.selector.query.filter as typeof filter).children[0].value, true);
  const cyclic: {self?: unknown} = {}; cyclic.self = cyclic;
  assert.throws(() => registerQuery(model, {...descriptor, selector: {query: {...selector.query, filter: cyclic}}}), SelectionInputError);
  let deep: unknown = null; for (let i = 0; i < 30; i++) deep = {child: deep};
  assert.throws(() => registerQuery(model, {...descriptor, selector: {query: {...selector.query, filter: deep}}}), SelectionInputError);
  model.dispose();
});

test('query work reads bounded facts equally at 50,000 and 50,000,000 results', () => {
  function run(total: number) {
    let reads = 0;
    const row = fact('movie', total);
    const observed = {...row, get count() { reads++; return total; }};
    const descriptor = query([observed]);
    const model = new SelectionModel(context);
    registerQuery(model, descriptor);
    registerWindow(model, [item('one'), item('two')]);
    model.selectWholeQuery('query'); model.toggle('window', key(item('one'))); model.keepOnly('movie');
    assert.equal(target(model).total, total - 1);
    assert.equal(model.getSnapshot().registeredItemCount, 2);
    assert.equal(target(model).refinement.excludedItems.length, 1);
    assert(JSON.stringify(target(model)).length < 6000);
    model.dispose(); return reads;
  }
  const smaller = run(50_000), larger = run(50_000_000);
  assert.equal(larger, smaller); assert(smaller < 40);
});
