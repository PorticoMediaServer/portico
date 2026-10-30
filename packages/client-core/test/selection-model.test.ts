import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
  SelectionInputError, SelectionModel, selectionCapabilities, selectionKey,
  type SelectionContext, type SelectionItem, type SelectionWindow,
} from '../src/selection-model/index.ts';

const context = (): SelectionContext => ({
  scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', viewId: 'home', privacyFence: 'private-1'},
  revision: {catalogRevision: '10', viewerRevision: '20'},
});
const movie = (id: string, libraryId = 'movies'): SelectionItem => ({
  kind: 'movie', id, libraryId,
  capabilities: selectionCapabilities('movie', {play: true, queue: true, playlist: true, watched: true, myList: true}),
});
function window(model: SelectionModel, id: string, items: readonly SelectionItem[], options: Partial<SelectionWindow> = {}) {
  const value = {id, sectionId: id, sectionOrder: 0, start: 0, items, ...options};
  const result = model.registerWindow(model.captureContext(), value);
  assert(result.ok);
  return result;
}
const key = (model: SelectionModel, item: SelectionItem) => selectionKey(model.getSnapshot().context.scope, item);
function explicit(model: SelectionModel) {
  const target = model.getSnapshot().target;
  assert(target?.mode === 'explicit');
  return target;
}

test('selection fences duplicate catalogue, source-local channel and person ids honestly', () => {
  const model = new SelectionModel(context());
  const entries: SelectionItem[] = [
    movie('same', 'a'), movie('same', 'b'),
    {kind: 'channel', sourceId: 'source-a', channelId: 'same', capabilities: selectionCapabilities('channel', {play: true, favorite: true})},
    {kind: 'channel', sourceId: 'source-b', channelId: 'same', capabilities: selectionCapabilities('channel', {play: true})},
    {kind: 'person', id: 'same', capabilities: selectionCapabilities('person', {})},
  ];
  window(model, 'all', entries);
  assert(model.selectLoaded().ok);
  assert.equal(new Set(model.getSnapshot().selectedKeys).size, 5);
  assert.deepEqual(explicit(model).items.map(entry => entry.item), entries);
  assert.equal(model.getSnapshot().summary.count, 5);
  assert.equal(model.getSnapshot().summary.every.play, false);
  assert.deepEqual(model.getSnapshot().summary.library, {kind: 'unknown'});
  assert.deepEqual(model.getSnapshot().summary.types, [{kind: 'movie', count: 2}, {kind: 'channel', count: 2}, {kind: 'person', count: 1}]);
  const original = key(model, entries[0]);
  for (const change of [
    {serverId: 'other'}, {authority: 'hosted' as const}, {accountId: 'other'},
    {profileId: 'other'}, {viewId: 'other'}, {privacyFence: 'other'},
  ]) assert.notEqual(selectionKey({...context().scope, ...change}, entries[0]), original);
  model.dispose();
});

test('every identity, privacy and revision change clears selection and fences late work', () => {
  const initial = context();
  const changes: SelectionContext[] = [
    ...(['serverId', 'accountId', 'profileId', 'viewId', 'privacyFence'] as const).map(name => ({...initial, scope: {...initial.scope, [name]: 'changed'}})),
    {...initial, scope: {...initial.scope, authority: 'hosted'}},
    {...initial, revision: {...initial.revision, catalogRevision: '11'}},
    {...initial, revision: {...initial.revision, viewerRevision: '21'}},
  ];
  for (const next of changes) {
    const model = new SelectionModel(initial);
    const item = movie('one');
    window(model, 'row', [item]); model.selectLoaded();
    const read = model.captureContext(), operation = model.captureOperation();
    assert(operation);
    assert.equal(model.setContext(next), true);
    assert.equal(model.getSnapshot().active, false);
    assert.equal(model.getSnapshot().registeredWindowCount, 0);
    assert.equal(model.isContextCurrent(read), false);
    assert.equal(model.isOperationCurrent(operation), false);
    assert.deepEqual(model.registerWindow(read, {id: 'late', sectionId: 'row', sectionOrder: 0, start: 0, items: [item]}), {ok: false, reason: 'stale-context'});
    assert.equal(model.setContext(next), false);
    model.setContext(initial);
    assert.equal(model.isContextCurrent(read), false, 'returning to an old identity must not resurrect a ticket');
    model.dispose();
  }
});

test('read and operation tickets cannot be cloned or shared between model instances', () => {
  const a = new SelectionModel(context()), b = new SelectionModel(context());
  window(a, 'row', [movie('one')]); a.selectLoaded();
  const read = a.captureContext(), operation = a.captureOperation();
  assert(operation);
  assert.equal(a.isContextCurrent({...read}), false);
  assert.equal(b.isContextCurrent(read), false);
  assert.equal(a.isOperationCurrent({...operation}), false);
  assert.equal(b.isOperationCurrent(operation), false);
  a.cancelPending();
  assert.equal(a.isContextCurrent(read), false);
  assert.equal(a.isOperationCurrent(operation), false);
  assert.equal(a.getSnapshot().summary.count, 1, 'cancellation does not pretend to reverse a server write');
  assert.equal(a.isContextCurrent(a.captureContext()), true);
  a.dispose(); b.dispose();
});

test('explicit picks survive eviction and keep display order distinct from queue pick order', () => {
  const model = new SelectionModel(context());
  const a = movie('a'), b = movie('b'), c = movie('c');
  const later = window(model, 'later', [c], {sectionOrder: 1});
  const earlier = window(model, 'earlier', [a, b]);
  model.toggle('later', key(model, c)); model.toggle('earlier', key(model, b)); model.toggle('earlier', key(model, a));
  assert.deepEqual(explicit(model).items.map(entry => 'id' in entry.item ? entry.item.id : ''), ['c', 'b', 'a']);
  assert.deepEqual(explicit(model).displayItems.map(entry => 'id' in entry.item ? entry.item.id : ''), ['a', 'b', 'c']);
  earlier.unregister(); later.unregister();
  assert.equal(model.getSnapshot().registeredItemCount, 0);
  assert.equal(model.getSnapshot().summary.count, 3);
  assert.equal(model.isSelected(key(model, a)), true);
  model.clear(); assert.equal(model.getSnapshot().target, null);
  model.dispose();
});

test('Shift ranges require contiguous bounded windows and stay within one section', () => {
  const model = new SelectionModel(context());
  const items = Array.from({length: 5}, (_, i) => movie(String(i)));
  window(model, 'first', items.slice(0, 2), {sectionId: 'row'});
  const last = window(model, 'last', items.slice(3), {sectionId: 'row', start: 3});
  window(model, 'other', [movie('else')], {sectionOrder: 1});
  model.toggle('first', key(model, items[0]));
  assert.deepEqual(model.previewRange('last', key(model, items[4])), {ok: false, reason: 'range-not-loaded'});
  assert.deepEqual(model.toggle('last', key(model, items[4]), true), {ok: false, reason: 'range-not-loaded'});
  assert.equal(model.getSnapshot().summary.count, 1);
  assert.deepEqual(model.previewRange('other', key(model, movie('else'))), {ok: false, reason: 'different-section'});
  window(model, 'gap', items.slice(2, 3), {sectionId: 'row', start: 2});
  const preview = model.previewRange('last', key(model, items[4]));
  assert(preview.ok); assert.equal(preview.keys.length, 5);
  assert.equal(model.getSnapshot().summary.count, 1, 'preview is pure');
  assert(model.toggle('last', key(model, items[4]), true).ok);
  assert.equal(model.getSnapshot().summary.count, 5);
  last.unregister();
  assert.equal(model.getSnapshot().summary.count, 5);
  model.dispose();
});

test('evicted or moved range anchors never select a different item at an old index', () => {
  const model = new SelectionModel(context());
  const a = movie('a'), b = movie('b');
  const original = window(model, 'row', [a, b]);
  model.toggle('row', key(model, a));
  window(model, 'row', [b], {start: 1});
  original.unregister();
  assert.equal(model.getSnapshot().registeredItemCount, 1);
  assert.deepEqual(model.previewRange('row', key(model, b)), {ok: false, reason: 'no-anchor'});
  model.dispose();
});

test('Select row uses the last pick and selections can cross Home rows', () => {
  const model = new SelectionModel(context());
  const a = movie('a'), b = movie('b'), c = movie('c'), d = movie('d');
  window(model, 'top', [a, b]); window(model, 'bottom', [c, d], {sectionOrder: 1});
  model.toggle('top', key(model, a)); model.toggle('bottom', key(model, c));
  assert(model.selectRow().ok);
  assert.deepEqual(explicit(model).items.map(entry => 'id' in entry.item ? entry.item.id : ''), ['a', 'c', 'd']);
  assert.equal(model.isSelected(key(model, b)), false);
  model.keepOnly('person');
  assert.equal(model.getSnapshot().active, false);
  model.dispose();
});

test('overlapping windows deduplicate identities and reject conflicting content transactionally', () => {
  const model = new SelectionModel(context());
  const a = movie('a'), b = movie('b');
  window(model, 'one', [a, b], {sectionId: 'row'});
  window(model, 'two', [b], {sectionId: 'row', start: 1});
  model.selectLoaded(); assert.equal(model.getSnapshot().summary.count, 2);
  const previous = model.getSnapshot();
  assert.throws(() => window(model, 'bad', [movie('c')], {sectionId: 'row', start: 1}), error => error instanceof SelectionInputError && error.code === 'conflicting-windows');
  assert.equal(model.getSnapshot(), previous);
  assert.throws(() => window(model, 'bad', [{...a, capabilities: selectionCapabilities('movie', {})}], {sectionId: 'different'}), SelectionInputError);
  assert.equal(model.getSnapshot(), previous);
  model.dispose();
});

test('replacement registration refreshes actual offers and invalidates captured work safely', () => {
  const model = new SelectionModel(context());
  const item = movie('a');
  const old = window(model, 'row', [item]); model.selectLoaded();
  const operation = model.captureOperation(); assert(operation);
  const replacement = window(model, 'row', [{...item, capabilities: selectionCapabilities('movie', {})}]);
  old.unregister();
  assert.equal(model.getSnapshot().registeredWindowCount, 1);
  assert.equal(model.getSnapshot().summary.every.play, false);
  assert.equal(model.isOperationCurrent(operation), false);
  replacement.unregister();
  assert.equal(model.getSnapshot().registeredWindowCount, 0);
  model.dispose();
});

test('registration limits are enforced before publication and sparse huge ranges do bounded work', () => {
  const model = new SelectionModel(context(), {maxWindows: 2, maxWindowItems: 2, maxRegisteredItems: 2});
  const a = movie('a'), b = movie('b');
  window(model, 'first', [a], {sectionId: 'row'});
  window(model, 'last', [b], {sectionId: 'row', start: 50_000_000});
  model.toggle('first', key(model, a));
  assert.deepEqual(model.previewRange('last', key(model, b)), {ok: false, reason: 'range-not-loaded'});
  const snapshot = model.getSnapshot();
  assert.throws(() => window(model, 'third', []), error => error instanceof SelectionInputError && error.code === 'registration-limit');
  assert.throws(() => window(model, 'first', [a, b, movie('c')]), SelectionInputError);
  assert.throws(() => window(model, 'first', [a, movie('c')], {sectionId: 'row'}), SelectionInputError);
  assert.equal(model.getSnapshot(), snapshot);
  model.dispose();
});

test('input copies and published snapshots cannot be mutated to change an operation', () => {
  const supplied = context();
  const model = new SelectionModel(supplied);
  const item = movie('a');
  window(model, 'row', [item]); model.selectLoaded();
  const target = explicit(model);
  (supplied.scope as {profileId: string}).profileId = 'forged';
  (item as {id: string}).id = 'forged';
  assert.equal(model.getSnapshot().context.scope.profileId, 'profile');
  assert.equal('id' in target.items[0].item && target.items[0].item.id, 'a');
  assert(Object.isFrozen(target.items[0].item.capabilities));
  assert.throws(() => { (target.items as unknown[]).push({}); }, TypeError);
  model.dispose();
});

test('aggregate selections retain the displayed show rather than its playable child', () => {
  const model = new SelectionModel(context());
  const show: SelectionItem = {kind: 'show', id: 'displayed-show', libraryId: 'tv', capabilities: selectionCapabilities('show', {play: true})};
  window(model, 'row', [show]); model.selectLoaded();
  assert.deepEqual(explicit(model).items[0].item, show);
  model.dispose();
});

test('a person remains selectable without any permitted bulk command; malformed channel identities fail', () => {
  const model = new SelectionModel(context());
  const person: SelectionItem = {kind: 'person', id: 'person', capabilities: selectionCapabilities('person', {})};
  window(model, 'people', [person]); model.selectLoaded();
  assert.equal(model.getSnapshot().active, true);
  assert.equal(model.getSnapshot().summary.count, 1);
  assert.deepEqual(model.getSnapshot().summary.library, {kind: 'none'});
  assert.throws(() => window(model, 'fake', [{kind: 'channel', id: 'fake', libraryId: 'fake', sourceId: 's', channelId: 'c', capabilities: selectionCapabilities('channel', {})} as unknown as SelectionItem]), SelectionInputError);
  assert.throws(() => window(model, 'missing-source', [{kind: 'channel', channelId: 'c', capabilities: selectionCapabilities('channel', {})} as SelectionItem]), SelectionInputError);
  model.dispose();
});

test('dispose publishes an empty final snapshot, fences work, and makes cleanup harmless', () => {
  const model = new SelectionModel(context());
  let publications = 0;
  const unsubscribe = model.subscribe(() => { publications++; });
  const registration = window(model, 'row', [movie('a')]); model.selectLoaded();
  const read = model.captureContext(), operation = model.captureOperation(); assert(operation);
  model.dispose();
  assert.equal(model.getSnapshot().target, null);
  assert.equal(model.isContextCurrent(read), false);
  assert.equal(model.isOperationCurrent(operation), false);
  assert.deepEqual(model.toggle('row', key(model, movie('a'))), {ok: false, reason: 'disposed'});
  assert.deepEqual(model.registerWindow(read, {id: 'late', sectionId: 'row', sectionOrder: 0, start: 0, items: []}), {ok: false, reason: 'disposed'});
  const count = publications;
  registration.unregister(); unsubscribe(); model.clear(); model.dispose();
  assert.equal(publications, count);
});
