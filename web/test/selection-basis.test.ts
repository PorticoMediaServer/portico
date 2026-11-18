import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSelectionModel, type BrowseSelectionContext} from '../../packages/client-core/src/selection-model/index.ts';
import {createDownloadRequest} from '../../packages/client-core/src/download-requests.ts';
import {wholeViewQuery} from '../src/screens/browse/selection-query.ts';
import {createSelectionActions} from '../src/screens/browse/selection-actions.ts';
import {createBrowserQueryActions} from '../src/screens/browse/selection-action-host.tsx';
import {createQuerySelectionEditor} from '../src/screens/browse/selection-query-editor.ts';
import {queryDeleteTarget, resolveQueryDelete} from '../src/screens/browse/selection-delete-query.ts';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';
import {defaultI18n} from '@i18n';
import * as selectionUnavailableActions from '../src/screens/browse/selection-unavailable-actions.ts';
import {captureQueueSource} from '../src/platform/playback/queue-source.ts';

const proof = '0123456789abcdef'.repeat(4), otherProof = 'f'.repeat(64), revision = 'browse_' + 'c'.repeat(64);
const sort = [{field: 'title', direction: 'asc' as const}];
const counts = {play: 2, queue: 2, playlist: 2, myList: 2, watched: 2, collection: 2, favorite: 2, download: 2, edit: 2, refresh: 2, delete: 2};
const whole = {libraryId: 'lib', pivot: 'movies', filter: null, sort, catalogRevision: revision, selectionBasis: proof, total: 2,
  types: [{kind: 'movie', count: 2, capabilities: counts, libraries: [{libraryId: 'lib', count: 2}]}]};
const context = {scope: {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', viewId: 'view'}, revision: {pageRevision: revision}};
function selected(selectionBasis = proof, total = 2) {
  const query = wholeViewQuery({...whole, selectionBasis, total, types: [{...whole.types[0]!, count: total, capabilities: Object.fromEntries(Object.keys(counts).map(key => [key, total])) as typeof counts, libraries: [{libraryId: 'lib', count: total}]}]}); const model = new BrowseSelectionModel(context);
  assert.equal(model.registerQuery(model.captureContext(), query).ok, true); assert.equal(model.selectWholeQuery(query.id).ok, true);
  const operation = model.captureOperation(); assert.ok(operation); assert.ok(operation.target.mode === 'query');
  return {model, query, operation, target: operation.target};
}

test('web held-selection identity changes with the witness while the ordinary page revision stays fixed', () => {
  const first = selected(), second = selected(otherProof);
  try {
    assert.notEqual(first.query.id, second.query.id); assert.equal(first.query.catalogRevision, second.query.catalogRevision);
    assert.ok('query' in first.target.selector); assert.equal(first.target.selector.query.selectionBasis, proof);
    const invalid = {...whole, selectionBasis: 'A'.repeat(64)}; assert.throws(() => wholeViewQuery(invalid));
    const invalidQuery = {...first.query, selector: {query: {libraryId: 'lib', pivot: 'movies', selectionBasis: 'bad'}}};
    assert.throws(() => first.model.registerQuery(first.model.captureContext(), invalidQuery));
  } finally {first.model.dispose(); second.model.dispose();}
});

test('a whole view\'s Watched, Favourite and My List are one set_state job over the view, never its members', async () => {
  const held = selected(), bodies: any[] = [];
  const actions = createSelectionActions({api: {request: async <T>(path: string, method?: string, body?: unknown) => {
    assert.equal(path, '/v1/jobs'); assert.equal(method, 'POST'); bodies.push(body);
    return {id: 'job', kind: 'set_state', state: 'done', progress: {done: 2, total: 2}} as T;
  }}, events: {on: () => () => {}}, requestId: async () => 'operation', isCurrent: () => true, reportError() {}});
  try {
    for (const action of [{watched: true}, {favorite: true}, {watchlist: true}]) await actions.personal(held.operation, action);
    assert.deepEqual(bodies, [{watched: true}, {favorite: true}, {inList: true}].map(params => ({kind: 'set_state', target: {filter: 'library=lib&kind=movie'}, params})));
  } finally {held.model.dispose();}
});

test('queue-source capture validates and preserves the supplied proof losslessly', () => {
  const input = {query: {libraryId: 'lib', pivot: 'movies', selectionBasis: proof, selectionRevision: revision, sort}};
  const captured = captureQueueSource(input); input.query.selectionBasis = otherProof;
  assert.ok('query' in captured); assert.equal(captured.query.selectionBasis, proof); assert.equal(captured.query.selectionRevision, revision); assert.ok(Object.isFrozen(captured.query));
  for (const invalid of [null, '', 'A'.repeat(64), 'a'.repeat(63), 1]) assert.throws(() => captureQueueSource({query: {...input.query, selectionBasis: invalid}} as never));
  assert.deepEqual(captureQueueSource({query: {libraryId: 'lib'}}), {query: {libraryId: 'lib'}});
});

test('larger query Delete fails before I/O while its capability facts remain truthful', async () => {
  const held = selected(proof, 201); let calls = 0;
  const ports = {api: {request: async () => {calls++; throw new Error('must not enumerate');}}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'};
  try {
    assert.equal(queryDeleteTarget(held.target), false);
    await assert.rejects(resolveQueryDelete(held.target, ports, new AbortController().signal), /query-delete-unavailable/); assert.equal(calls, 0);
    assert.equal(held.target.summary.every.delete, true);
    const legacy = {...held.target, total: 2, sourceTotal: 2, selector: {query: {libraryId: 'lib', pivot: 'movies', sort}}}; assert.equal(queryDeleteTarget(legacy), false);
  } finally {held.model.dispose();}
});

test('the container-only download dispatcher refuses a proof-bearing query instead of approximating item IDs', async () => {
  const held = selected(); let calls = 0;
  try {
    await assert.rejects(createDownloadRequest({request: async () => {calls++; throw new Error('must not enumerate');}}, {
      operationId: 'operation', deviceId: 'device', quality: 'original', policy: {episodes: 'all'}, target: held.target.selector,
    } as never)); assert.equal(calls, 0);
  } finally {held.model.dispose();}
});

test('the existing selection menu renders unavailable Download and Delete rows with their supplied reasons', async () => {
  const host = createRenderer();
  const {PhoneActionBar} = await componentModule(new URL('../src/kit/selection/SelectionBar.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n}, '../controls': {Button: 'Button'}, '../Icon': {Icon: 'Icon'},
    '../overlays': {Menu: 'Menu', MenuGroup: 'MenuGroup', MenuItem: 'MenuItem'}, '../overlays/shared': {usePhone: () => true},
    './copy': {selectionText: (key: string) => key}, './presentation': {selectionCountText: () => '201 movies'}, './selection.module.css': {default: {}},
  });
  const download = defaultI18n.t('error.unavailable'), deletion = defaultI18n.t('next.selection.tooMany');
  const output = host.render(() => PhoneActionBar({selection: {count: 201}, actions: {downloadDisabledReason: download, deleteDisabledReason: deletion}}));
  const rows = tree(output).flatMap((node: any) => node.type === 'Menu' && Array.isArray(node.props.items) ? node.props.items : []);
  for (const [id, reason] of [['download', download], ['delete', deletion]]) {const row = rows.find((item: any) => item.id === id); assert.ok(row); assert.equal(row.disabled, true); assert.equal(row.description, reason);}
  host.dispose();
});

test('the real web toolbar tells the truth about bounded query actions instead of guessing an unreal bound', async () => {
  for (const size of [200, 201]) {
    const held = selected(proof, size), host = hooks();
    const {createLibrarySelection} = await componentModule(new URL('../src/screens/browse/selection.tsx', import.meta.url), {
      react: host.react, '@i18n': {defaultI18n}, '../../kit/selection': {SelectionBars: 'SelectionBars'},
      './selection-actions': {createSelectionActions, personalTargetIsLeaf: () => true, personalTargetSupportsJob: () => true},
      './selection-delete-query': {queryDeleteTarget}, './selection-query': {wholeViewQuery},
      './selection-unavailable-actions': selectionUnavailableActions,
    });
    const signal = new AbortController().signal, ports = {api: {request: async () => {throw new Error('no I/O expected');}}, events: {on: () => () => {}}, serverId: 'server', viewerId: 'viewer', requestId: async () => 'operation', isCurrent: () => true, signal};
    const playSelection = {getSnapshot: () => ({phase: 'ready', selection: held.model.getSnapshot(), rows: [], canPlay: true, canQueue: true}), subscribe: () => () => {},
      captureOperation: () => held.operation, isOperationCurrent: () => true, dispose() {}};
    // The Library composer passes the actual query action host; that host's own exact policy
    // (selectionActionTargetInput) decides eligibility, at every bounded size.
    const queryActions = createBrowserQueryActions<BrowseSelectionContext>({subscribe: () => () => {}, captureOperation: () => held.operation, isOperationCurrent: () => true}, {
      api: {request: async <T>() => {throw new Error('no I/O expected');}, mediaUrl: (path: string) => path},
      requestId: async () => 'operation', deviceId: 'device', owner: true, signal, scopeKey: () => 'scope-key', isCurrent: () => true, subscribe: () => () => {}});
    // Without that host (the document-selection composer) the bar must not invent a Download it
    // cannot run, and the 200-member enumeration bound still bounds Delete on that path.
    const withoutHost = createLibrarySelection({playSelection, owner: true, actions: {...ports, reportError() {}}, deletion: ports, reportError() {}});
    const withHost = createLibrarySelection({playSelection, queryActions, owner: true, actions: {...ports, reportError() {}}, deletion: ports, reportError() {}});
    const render = (adapter: typeof withoutHost) => {const toolbar = adapter.toolbar.props.children[0]; return host.render(() => toolbar.type(toolbar.props));};
    try {
      const fallback = render(withoutHost);
      assert.equal(fallback.type, 'SelectionBars'); assert.equal(fallback.props.selection.count, size);
      assert.equal(fallback.props.actions.downloadDisabledReason, undefined); assert.equal(fallback.props.actions.onDownload, undefined);
      assert.equal(fallback.props.actions.deleteDisabledReason, undefined); assert.equal(fallback.props.actions.onDelete, undefined);
      assert.equal(queryActions.offered(held.target, 'download'), false); assert.equal(queryActions.offered(held.target, 'delete'), false);
      const composed = render(withHost);
      assert.equal(composed.props.selection.count, size);
      assert.equal(composed.props.actions.onDownload, undefined); assert.equal(composed.props.actions.onDelete, undefined);
      assert.equal(composed.props.actions.downloadDisabledReason, undefined); assert.equal(composed.props.actions.deleteDisabledReason, undefined);
      assert.equal(held.target.summary.every.delete, true); assert.equal(held.target.summary.every.download, true);
    } finally {withoutHost.dispose?.(); withHost.dispose?.(); queryActions.dispose(); held.model.dispose();}
  }
});

test('the held-selection picker retains the keyed transport and retires it with the selection', async () => {
  const held = selected(), host = hooks(); let current = true, calls = 0;
  const {createLibrarySelection} = await componentModule(new URL('../src/screens/browse/selection.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n}, '../../kit/selection': {SelectionBars: 'SelectionBars'},
    './selection-actions': {createSelectionActions, personalTargetIsLeaf: () => true, personalTargetSupportsJob: () => true},
    './selection-picker': {queryDestinationTarget: () => true},
    './selection-query': {wholeViewQuery}, './selection-unavailable-actions': selectionUnavailableActions,
  });
  const request = {headers: {'Idempotency-Key': 'same-operation'}, body: {kind: 'add_view_to_list'}};
  const signal = new AbortController().signal, ports = {api: {
    request: async () => {throw new Error('not used');},
    requestRaw: async (path: string, method: string, options: unknown) => {
      calls++; assert.equal(path, '/v1/jobs'); assert.equal(method, 'POST'); assert.equal(options, request);
      return {status: 202, headers: {}, body: {id: 'job'}};
    },
  }, serverId: 'server', viewerId: 'viewer', requestId: async () => 'operation', isCurrent: () => current, signal,
  canCreateCollection: true, pickerIdentity: {authority: 'local', accountId: 'account', profileId: 'profile'}};
  const playSelection = {getSnapshot: () => ({phase: 'ready', selection: held.model.getSnapshot(), rows: [], canPlay: true, canQueue: true}), subscribe: () => () => {},
    captureOperation: () => held.operation, isOperationCurrent: () => current};
  const adapter = createLibrarySelection({playSelection, pickers: ports, actions: {...ports, reportError(error: unknown) {throw error;}}, reportError(error: unknown) {throw error;}});
  try {
    const toolbar = adapter.toolbar.props.children[0], bar = host.render(() => toolbar.type(toolbar.props));
    bar.props.actions.onPlaylist({});
    const pickerHost = adapter.toolbar.props.children.find((node: any) => node?.type?.name === 'LibraryPickerHost');
    const surface = host.render(() => pickerHost.type(pickerHost.props)), api = surface.props.ports.api;
    assert.equal((await api.requestRaw('/v1/jobs', 'POST', request)).body.id, 'job');
    current = false;
    await assert.rejects(api.requestRaw('/v1/jobs', 'POST', request), {name: 'AbortError'});
    assert.equal(calls, 1);
  } finally {adapter.dispose?.(); held.model.dispose();}
});
