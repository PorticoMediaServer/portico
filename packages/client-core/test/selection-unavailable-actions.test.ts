import test from 'node:test';
import assert from 'node:assert/strict';
import {parseWholeSelection} from '../src/browse.ts';
import {browseWholeSelectionQuery} from '../src/browse-whole-selection.ts';
import {BrowseSelectionModel, SelectionInputError, selectionActions, browseSelectionKey, type BrowseSelectionContext} from '../src/selection-model/index.ts';
import {enqueueCatalogueQuerySelection, playCatalogueQuerySelection} from '../src/catalogue-query-selection.ts';

const revision = `browse_${'a'.repeat(64)}`, basis = 'b'.repeat(64);
const context: BrowseSelectionContext = {scope: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', viewId: 'v'}, revision: {pageRevision: revision}};
const unavailableActions = {play: 'unsupported-sort', queue: 'unsupported-sort', metadataPreview: 'preview-limit'} as const;
function wire(total = 201, restrictions: unknown = unavailableActions) {
  return {selector: {query: {libraryId: 'lib', pivot: 'movies', selectionBasis: basis, filter: {variant:'predicate',field: 'search', operator: 'contains', value: 'a'}, sort: [{field: 'title', direction: 'asc'}]}},
    catalogRevision: revision, total, unavailableActions: restrictions,
    types: [{kind: 'movie', count: total, libraries: [{libraryId: 'lib', count: total}], capabilityCounts: {play: total, queue: total, playlist: total, myList: total, watched: total, collection: total, favorite: total, download: total, edit: total, refresh: total, delete: total}}]};
}
function query(total = 201, restrictions: unknown = unavailableActions) {
  const parsed = parseWholeSelection(wire(total, restrictions));assert(parsed);
  const query = browseWholeSelectionQuery(parsed);assert(query);return query;
}
function selected(total = 201) {
  const model = new BrowseSelectionModel(context), descriptor = query(total);
  assert(model.registerQuery(model.captureContext(), descriptor).ok);
  assert(model.selectWholeQuery(descriptor.id).ok);
  return model;
}
test('strict optional exclusions preserve exact counts and proof through parser, model and capture', () => {
  const descriptor = query(), model = selected();
  const target = model.captureOperation()!.target;assert(target.mode === 'query');
  assert.deepEqual(target.summary.unavailableActions, unavailableActions);
  assert.equal(target.total, 201);assert.equal(target.summary.every.play, true);assert.equal(target.summary.every.edit, true);
  assert.equal(descriptor.summary.types[0].capabilityCounts.play, 201);
  assert.equal('query' in target.selector && target.selector.query.selectionBasis, basis);
  assert(Object.isFrozen(target.summary.unavailableActions));
  assert(model.keepOnly('movie').ok);assert.deepEqual(model.getSnapshot().summary.unavailableActions, unavailableActions);
  model.dispose();
});
test('unknown action keys, cross-wired reasons and malformed restrictions refuse the optional descriptor', () => {
  for (const value of [null, [], 'unsupported-sort', {shuffle: 'unsupported-sort'}, {play: 'preview-limit'}, {queue: null}, {metadataPreview: 'unsupported-sort'}, {metadataPreview: 'future-code'}, {play: undefined}]) {
    assert.equal(parseWholeSelection(wire(201, value)), undefined, JSON.stringify(value));
  }
  for (const value of [{}, {queue: 'unsupported-sort'}, {metadataPreview: 'preview-limit'}]) assert(parseWholeSelection(wire(201, value)));
  const original = wire();delete (original as {unavailableActions?: unknown}).unavailableActions;
  assert.equal(parseWholeSelection(original)?.unavailableActions, undefined);
});
test('direct query registration validates and copies restrictions, rather than dropping unsupported restrictions', () => {
  const model = new BrowseSelectionModel(context), descriptor = query();
  for (const unavailableActions of [{play: 'other'}, {bogus: 'unsupported-sort'}, null]) {
    assert.throws(() => model.registerQuery(model.captureContext(), {...descriptor, summary: {...descriptor.summary, unavailableActions}} as never), SelectionInputError);
  }
  const input = {...unavailableActions};
  assert(model.registerQuery(model.captureContext(), {...descriptor, summary: {...descriptor.summary, unavailableActions: input}}).ok);
  delete (input as {play?: string}).play;
  assert(model.selectWholeQuery(descriptor.id).ok);assert.equal(model.getSnapshot().summary.unavailableActions?.play, 'unsupported-sort');
  model.dispose();
});
test('restrictions disable otherwise ready offers on every platform without changing authority or hidden behavior', () => {
  const model = selected(), summary = model.getSnapshot().summary;
  for (const platform of ['desktop-web', 'mobile-web', 'phone-app', 'tv'] as const) {
    const rules = selectionActions(summary, {platform, isOwner: true, canEditMetadata: true});
    assert.deepEqual(rules.play, {state: 'disabled', reason: 'unsupported-sort'});
    assert.deepEqual(rules['play-next'], {state: 'disabled', reason: 'unsupported-sort'});
    assert.deepEqual(rules['play-later'], {state: 'disabled', reason: 'unsupported-sort'});
    assert.deepEqual(rules.edit, {state: 'disabled', reason: 'preview-limit'});
    assert.equal(rules.playlist.state, 'ready');assert.equal(rules.refresh.state, 'ready');
    assert.deepEqual(rules.delete, {state: 'explain', reason: 'delete-limit'});
    const viewer = selectionActions(summary, {platform, isOwner: false, canEditMetadata: false});
    assert.equal(viewer.edit.state, 'hidden');
    const denied = selectionActions({...summary, every: {...summary.every, play: false, queue: false, edit: false}}, {platform, isOwner: true, canEditMetadata: true});
    assert.equal(denied.play.state, 'hidden');assert.equal(denied['play-next'].state, 'hidden');
    assert.deepEqual(denied.edit, {state: 'disabled', reason: 'not-permitted'});
  }
  const grouped = {...summary, unavailableActions: undefined};
  assert.equal(selectionActions(grouped, {platform: 'tv', isOwner: true, canEditMetadata: true}).play.state, 'ready');
  model.dispose();
});
test('approved unavailable Play and Queue refuse before transport; supported selectors retain their proof', async () => {
  const model = selected();const target = model.captureOperation()!.target;assert(target.mode === 'query');
  let calls = 0;const player = {playSegments: async () => {calls++;}, enqueueSegments: async () => {calls++;}};
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  await assert.rejects(playCatalogueQuerySelection(player as never, target, lifetime), /catalogue-query-selection-unavailable/);
  await assert.rejects(enqueueCatalogueQuerySelection(player as never, target, 'next', lifetime), /catalogue-query-selection-unavailable/);
  assert.equal(calls, 0);model.dispose();
});
test('query exclusions retain server restrictions after narrowing; switching to explicit loaded picks removes them', () => {
  const model = selected(), descriptor = query();
  const item = {kind: 'movie' as const, id: 'one', libraryId: 'lib', capabilities: Object.fromEntries(Object.keys(descriptor.summary.every).map(name => [name, true])) as typeof descriptor.summary.every};
  assert(model.registerWindow(model.captureContext(), {id: 'window', sectionId: 'row', sectionOrder: 0, start: 0, queryId: descriptor.id, items: [item]}).ok);
  const key = browseSelectionKey(context.scope, item);
  assert(model.toggle('window', key).ok);
  assert.equal(model.getSnapshot().summary.count, 200);
  assert.deepEqual(model.getSnapshot().summary.unavailableActions, unavailableActions, 'client counts never invent a new server replay qualification');
  assert.equal(model.getSnapshot().summary.every.play, true);
  assert(model.selectLoaded().ok);
  assert.equal(model.getSnapshot().target?.mode, 'explicit');assert.equal(model.getSnapshot().summary.unavailableActions, undefined);
  assert.equal(selectionActions(model.getSnapshot().summary, {platform: 'tv', isOwner: true, canEditMetadata: true}).play.state, 'ready');
  model.dispose();
});
test('Playlist sort exclusion survives wire/model/rules without reducing grants or affecting other forms', async () => {
  const descriptor = query(201, {playlist: 'unsupported-sort'}), model = new BrowseSelectionModel(context);
  assert(model.registerQuery(model.captureContext(), descriptor).ok);assert(model.selectWholeQuery(descriptor.id).ok);
  const target = model.captureOperation()!.target;assert(target.mode === 'query');
  assert.deepEqual(target.summary.unavailableActions, {playlist: 'unsupported-sort'});
  assert.equal(target.total, 201);assert.equal(descriptor.summary.types[0].capabilityCounts.playlist, 201);
  assert.equal(target.summary.every.playlist, true);
  for (const platform of ['desktop-web', 'mobile-web', 'phone-app', 'tv'] as const) {
    const environment = {platform, isOwner: true, canEditMetadata: true};
    assert.deepEqual(selectionActions(target.summary, environment).playlist, {state: 'disabled', reason: 'unsupported-sort'});
    assert.equal(selectionActions({...target.summary, every: {...target.summary.every, playlist: false}}, environment).playlist.state, 'hidden');
    assert.equal(selectionActions({...target.summary, unavailableActions: undefined}, environment).playlist.state, 'ready');
    assert.equal(selectionActions(target.summary, environment).collection.state, 'ready');
  }
  assert.equal(parseWholeSelection(wire(201, {playlist: 'preview-limit'})), undefined);
  const {selectionJobTasks} = await import('../src/home-selection/execution.ts');
  const {HomeSelectionDestinations} = await import('../src/home-selection/destinations.ts');
  let requests = 0, ids = 0;
  const picker = new HomeSelectionDestinations({api: {request: async () => {requests++;}}, requestId: async () => {ids++;return 'operation';}} as never, target as never, 'playlist', new AbortController().signal, () => true, () => {});
  try {
    for (const dispatch of [() => picker.load(), () => picker.choose('playlist'), () => picker.create('New'), () => picker.retry()]) await assert.rejects(dispatch, {code: 'query-destination-unavailable'});
    await assert.rejects(selectionJobTasks(target as never, 'playlist-add', {playlistId: 'playlist', expectedRevision: 1, placement: 'end'}, async () => {ids++;return 'operation';}), {code: 'query-destination-unavailable'});
    assert.equal(requests, 0);assert.equal(ids, 0);
    const legacy = {...target, summary: {...target.summary, unavailableActions: undefined}};
    const tasks = await selectionJobTasks(legacy as never, 'playlist-add', {playlistId: 'playlist', expectedRevision: 1, placement: 'end'}, async () => 'operation');
    assert.equal(tasks.length, 1);assert.deepEqual(tasks[0].request.selector, target.selector);
    const collection = await selectionJobTasks(target as never, 'collection-add', {collectionId: 'collection', expectedRevision: 1}, async () => 'operation');assert.equal(collection.length, 1);
  } finally {picker.dispose();model.dispose();}
});
test('Delete sort exclusion preserves exact facts, owner precedence and the existing 200-title limit', () => {
  const input = wire(200, {delete: 'unsupported-sort'});input.selector.query.sort = [{field: 'groupOrder', direction: 'asc'}];
  const parsed = parseWholeSelection(input);assert(parsed);
  const descriptor = browseWholeSelectionQuery(parsed);assert(descriptor);
  const model = new BrowseSelectionModel(context);assert(model.registerQuery(model.captureContext(), descriptor).ok);assert(model.selectWholeQuery(descriptor.id).ok);
  const summary = model.getSnapshot().summary;
  assert.deepEqual(summary.unavailableActions, {delete: 'unsupported-sort'});assert.equal(summary.count, 200);
  assert.equal(summary.every.delete, true);assert.equal(descriptor.summary.types[0].capabilityCounts.delete, 200);
  for (const platform of ['desktop-web', 'mobile-web', 'phone-app', 'tv'] as const) {
    const owner = {platform, isOwner: true, canEditMetadata: true};
    assert.deepEqual(selectionActions(summary, owner).delete, {state: 'disabled', reason: 'unsupported-sort'});
    assert.equal(selectionActions(summary, {...owner, isOwner: false}).delete.state, 'hidden');
    assert.deepEqual(selectionActions({...summary, every: {...summary.every, delete: false}}, owner).delete, {state: 'disabled', reason: 'not-permitted'});
    assert.deepEqual(selectionActions({...summary, count: 201}, owner).delete, {state: 'explain', reason: 'delete-limit'});
    assert.equal(selectionActions({...summary, unavailableActions: undefined}, owner).delete.state, 'ready');
    for (const action of ['edit', 'collection', 'my-list'] as const) assert.equal(selectionActions(summary, owner)[action].state, 'ready');
  }
  assert.equal(parseWholeSelection(wire(200, {delete: 'preview-limit'})), undefined);model.dispose();
});
