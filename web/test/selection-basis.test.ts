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

test('personal dispatch retains the exact query witness without resolving a single member', async () => {
  const held = selected(), bodies: any[] = [];
  const actions = createSelectionActions({api: {request: async <T>(path: string, method?: string, body?: unknown) => {
    assert.equal(path, '/v1/jobs'); assert.equal(method, 'POST'); bodies.push(body);
    return {jobId: 'job', command: 'personal-state', state: 'complete', total: 2, totalKnown: true, done: 2, failed: 0} as T;
  }}, events: {on: () => () => {}}, requestId: async () => 'operation', isCurrent: () => true, reportError() {}});
  try {
    for (const action of [{watched: true}, {favorite: true}, {watchlist: true}]) await actions.personal(held.operation, action);
    assert.equal(bodies.length, 3);
    for (const body of bodies) {assert.equal(body.selector.query.selectionBasis, proof); assert.equal(body.expected.catalogRevision, revision); assert.equal('items' in body.selector, false);}
  } finally {held.model.dispose();}
});

test('query metadata preview and save carry the same full witness into the final job', async () => {
  const held = selected(), bodies: any[] = [], fence = 'd'.repeat(64);
  const spec = {field: 'contentRating', label: 'Content rating', type: 'text', bulk: true, group: 'general', maxLength: 300};
  const preview = {variant: 'ready', serverId: 'server', viewerFence: fence, contentKind: 'movie', kind: 'item', total: 2, selectionRevision: revision,
    schema: [spec], singleItemFields: [spec], excludedFields: [], targets: [],
    fields: {contentRating: {state: 'shared', distinctCount: 1, value: 'G', values: [{value: 'G', count: 2}], entries: [], locked: 0, truncated: false}}};
  const editor = createQuerySelectionEditor({api: {request: async <T>(path: string, _method?: string, body?: unknown) => {
    bodies.push(body); assert.ok(path === '/v1/metadata/bulk/preview' || path === '/v1/jobs');
    return (path.includes('preview') ? preview : {jobId: 'job', command: 'metadata-edit', state: 'complete', total: 2, totalKnown: true, done: 2, failed: 0}) as T;
  }}, serverId: 'server', viewerId: 'viewer', viewerFence: () => fence, events: {on: () => () => {}}, requestId: async () => 'operation', isCurrent: () => true});
  try {
    const grant = await editor.preview(held.operation);
    const result = await editor.save(held.operation, grant, {fields: {contentRating: 'PG'}, lists: {}, lockEdited: true}, () => {});
    assert.equal(result.updated, 2); assert.equal(bodies.length, 2);
    for (const body of bodies) {assert.equal(body.selector.query.selectionBasis, proof); assert.equal(body.expected.catalogRevision, revision); assert.equal(body.targets, undefined);}
    assert.equal(bodies[1].command, 'metadata-edit');
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
    const legacy = {...held.target, total: 2, sourceTotal: 2, selector: {query: {libraryId: 'lib', pivot: 'movies', sort}}}; assert.equal(queryDeleteTarget(legacy), true);
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

async function deleteResolver() {
  const details: string[] = [];
  const module = await componentModule(new URL('../src/screens/browse/selection-delete-query.ts', import.meta.url), {
    '@core/catalogue-selection.ts': {readCatalogueSelectionItem: async (_options: unknown, entry: {id: string}) => {details.push(entry.id); return {capabilities: {delete: true}};}},
  });
  return {resolve: module.resolveQueryDelete as typeof resolveQueryDelete, details};
}
const exactPage = (total: number, over: Record<string, unknown> = {}) => ({pivot: 'movies', applied: {query: null, sort, seek: null},
  entries: Array.from({length: total}, (_, n) => ({id: `movie-${n}`, kind: 'movie', title: `Movie ${n}`, libraryId: 'lib', navigation: {view: 'item'}})),
  pageInfo: {start: 0, total, revision, totalIsLowerBound: false, hasMore: false, nextCursor: ''}, ...over});

test('bounded whole-view Delete posts the exact proof once and converts every eligible member at the 200 boundary', async () => {
  for (const size of [1, 199, 200]) {
    const held = selected(proof, size), resolver = await deleteResolver(), bodies: any[] = [];
    try {
      const targets = await resolver.resolve(held.target, {api: {request: async <T>(path: string, method?: string, body?: unknown) => {
        assert.equal(path, '/v1/libraries/lib/browse'); assert.equal(method, 'POST'); bodies.push(body); return exactPage(size) as T;
      }}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'}, new AbortController().signal);
      assert.equal(bodies.length, 1); assert.equal(bodies[0].selectionBasis, proof); assert.deepEqual(bodies[0].range, {start: 0, revision}); assert.equal(bodies[0].limit, 200);
      assert.equal(targets.length, size); assert.equal(resolver.details.length, size); assert.equal(new Set(targets.map(entry => entry.itemId)).size, size);
      assert.deepEqual(targets.map(entry => entry.itemId), resolver.details); assert.ok(Object.isFrozen(targets));
    } finally {held.model.dispose();}
  }
});

test('witness refusal under the same page revision and incomplete 200-member answers never reach item checks', async () => {
  const held = selected(proof, 200);
  try {
    for (const response of [
      {pageInfo: {...exactPage(200).pageInfo, hasMore: true}}, {pageInfo: {...exactPage(200).pageInfo, nextCursor: 'more'}},
      {pageInfo: {...exactPage(200).pageInfo, totalIsLowerBound: true}}, {pageInfo: {...exactPage(200).pageInfo, revision: 'other'}},
      {entries: exactPage(199).entries}, {pageInfo: {...exactPage(200).pageInfo, total: 201}},
    ]) {
      const resolver = await deleteResolver();
      await assert.rejects(resolver.resolve(held.target, {api: {request: async <T>() => exactPage(200, response) as T}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'}, new AbortController().signal));
      assert.deepEqual(resolver.details, []);
    }
    const resolver = await deleteResolver(); let calls = 0;
    await assert.rejects(resolver.resolve(held.target, {api: {request: async (_path: string, _method?: string, body?: unknown) => {
      calls++; assert.equal((body as any).selectionBasis, proof); throw Object.assign(new Error('witness changed'), {status: 409, code: 'stale_continuation'});
    }}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'}, new AbortController().signal), {code: 'stale_continuation'});
    assert.equal(calls, 1); assert.deepEqual(resolver.details, []);
  } finally {held.model.dispose();}
});

test('the existing explicit-delete conversion remains compatible with legacy descriptors without a proof', async () => {
  const held = selected(), resolver = await deleteResolver();
  try {
    const legacy = {...held.target, selector: {query: {libraryId: 'lib', pivot: 'movies', sort}}};
    const targets = await resolver.resolve(legacy, {api: {request: async <T>(_path: string, _method?: string, body?: unknown) => {
      assert.equal('selectionBasis' in (body as object), false); return exactPage(2) as T;
    }}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'}, new AbortController().signal);
    assert.equal(targets.length, 2);
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
      if (size > 200) {assert.equal(fallback.props.actions.deleteDisabledReason, defaultI18n.t('next.selection.tooMany')); assert.equal(fallback.props.actions.onDelete, undefined); assert.equal(queryDeleteTarget(held.target), false);}
      else {assert.equal(fallback.props.actions.deleteDisabledReason, undefined); assert.equal(typeof fallback.props.actions.onDelete, 'function');}
      assert.equal(queryActions.offered(held.target, 'download'), true); assert.equal(queryActions.offered(held.target, 'delete'), true);
      const composed = render(withHost);
      assert.equal(composed.props.selection.count, size);
      assert.equal(typeof composed.props.actions.onDownload, 'function'); assert.equal(typeof composed.props.actions.onDelete, 'function');
      assert.equal(composed.props.actions.downloadDisabledReason, undefined); assert.equal(composed.props.actions.deleteDisabledReason, undefined);
      assert.equal(held.target.summary.every.delete, true); assert.equal(held.target.summary.every.download, true);
    } finally {withoutHost.dispose?.(); withHost.dispose?.(); queryActions.dispose(); held.model.dispose();}
  }
});
