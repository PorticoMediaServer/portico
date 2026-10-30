import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSelectionModel, SelectionModel, browseSelectionKey, selectionCapabilities, type BrowseSelectionContext, type SelectionItem, type SelectionQuery} from '../src/selection-model/index.ts';

const context = (): BrowseSelectionContext => ({scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  viewId: JSON.stringify({libraryId: 'movies', pivot: 'movies', query: {field: 'genre', operator: 'equals', value: 'Action'}, sort: [{field: 'title', direction: 'asc'}]})},
  revision: {pageRevision: 'opaque:do-not-split:catalog/viewer'}});
const movie = (id: string): SelectionItem => ({kind: 'movie', libraryId: 'movies', id, capabilities: selectionCapabilities('movie', {play: true})});
function register(model: BrowseSelectionModel, id: string, start: number, items: readonly SelectionItem[]) {
  const result = model.registerWindow(model.captureContext(), {id, sectionId: 'movies', sectionOrder: 0, start, items});
  assert(result.ok);return result;
}
const key = (model: BrowseSelectionModel, item: SelectionItem) => browseSelectionKey(model.getSnapshot().context.scope, item);

test('Browse context preserves only its genuine opaque page revision and applied source identity', () => {
  const input = {scope: {...context().scope}, revision: {...context().revision}}, model = new BrowseSelectionModel(input);
  const snapshot = model.getSnapshot();
  assert.deepEqual(snapshot.context, input);
  assert(Object.isFrozen(snapshot.context.revision));
  input.revision = {pageRevision: 'mutated'};
  input.scope = {...input.scope, viewId: 'mutated'};
  assert.equal(snapshot.context.revision.pageRevision, 'opaque:do-not-split:catalog/viewer');
  assert(!('privacyFence' in snapshot.context.scope));
  assert(!('catalogRevision' in snapshot.context.revision));
  assert(!('viewerRevision' in snapshot.context.revision));
  assert.throws(() => new BrowseSelectionModel({...context(), revision: {pageRevision: ''}}), {code: 'invalid-context'});
  assert.throws(() => new BrowseSelectionModel({...context(), revision: {pageRevision: 'actual', catalogRevision: 'fabricated'} as never}), {code: 'invalid-context'});
  assert.throws(() => new BrowseSelectionModel({...context(), scope: {...context().scope, privacyFence: 'borrowed-home'} as never}), {code: 'invalid-context'});
  model.dispose();
});

test('applied source, principal and opaque revision changes retire windows and exact operation/read tickets', () => {
  const initial = context();
  const changes: BrowseSelectionContext[] = [
    ...(['serverId', 'accountId', 'profileId', 'viewId'] as const).map(field => ({...initial, scope: {...initial.scope, [field]: 'changed'}})),
    {...initial, scope: {...initial.scope, authority: 'hosted'}},
    {...initial, revision: {pageRevision: 'new-opaque-token'}},
  ];
  for (const next of changes) {
    const model = new BrowseSelectionModel(initial), item = movie('one');
    register(model, 'page', 0, [item]);model.selectLoaded();
    const read = model.captureContext(), operation = model.captureOperation();assert(operation);
    assert.equal(model.setContext(next), true);
    assert.equal(model.isContextCurrent(read), false);
    assert.equal(model.isOperationCurrent(operation), false);
    assert.equal(model.getSnapshot().summary.count, 0);
    assert.equal(model.getSnapshot().registeredWindowCount, 0);
    assert.equal(model.setContext(next), false);
    assert.deepEqual(model.registerWindow(read, {id: 'late', sectionId: 'movies', sectionOrder: 0, start: 0, items: [item]}), {ok: false, reason: 'stale-context'});
    model.setContext(initial);
    assert.equal(model.isContextCurrent(read), false, 'return to prior route must not revive old reads');
    model.dispose();
  }
});

test('explicit Browse picks survive eviction but ranges require genuine contiguous resident offsets', () => {
  const model = new BrowseSelectionModel(context()), first = movie('first'), middle = movie('middle'), last = movie('last');
  const a = register(model, 'a', 0, [first]);register(model, 'c', 2, [last]);
  model.toggle('a', key(model, first));
  assert.deepEqual(model.previewRange('c', key(model, last)), {ok: false, reason: 'range-not-loaded'});
  register(model, 'b', 1, [middle]);
  assert(model.toggle('c', key(model, last), true).ok);
  const target = model.getSnapshot().target;assert(target?.mode === 'explicit');
  assert.deepEqual(target.displayItems.map(pick => 'id' in pick.item ? pick.item.id : null), ['first', 'middle', 'last']);
  a.unregister();
  assert.equal(model.getSnapshot().summary.count, 3);
  assert.equal(model.getSnapshot().registeredItemCount, 2);
  assert.deepEqual(model.toggle('a', key(model, first)), {ok: false, reason: 'unregistered'});
  assert.deepEqual(model.previewRange('c', key(model, last)), {ok: false, reason: 'no-anchor'});
  const operation = model.captureOperation();assert(operation);
  assert.equal(model.isOperationCurrent({...operation}), false);
  model.cancelPending();assert.equal(model.isOperationCurrent(operation), false);
  model.dispose();
});

test('Browse never synthesizes whole-query membership or accepts another model domain ticket', () => {
  const input = {scope: {...context().scope}, revision: {...context().revision}}, model = new BrowseSelectionModel(input);
  const legacy = new SelectionModel({scope: {...input.scope, privacyFence: 'actual-legacy-fence'}, revision: {catalogRevision: 'catalog', viewerRevision: 'viewer'}});
  assert.equal(model.isContextCurrent(legacy.captureContext() as never), false);
  assert.throws(() => model.registerQuery(model.captureContext(), {id: 'forged', total: 999} as SelectionQuery), {code: 'invalid-query'});
  assert.equal(model.getSnapshot().summary.count, 0);
  assert.equal(model.getQuery('forged'), null);
  const a = register(model, 'actual', 0, [movie('one')]);model.selectLoaded();
  const read = model.captureContext();model.dispose();
  assert.equal(model.isContextCurrent(read), false);
  assert.equal(model.getSnapshot().registeredItemCount, 0);
  a.unregister();legacy.dispose();
});
