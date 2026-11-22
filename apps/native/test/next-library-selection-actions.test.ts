import test from 'node:test';
import assert from 'node:assert/strict';
import * as actions from '../../../packages/client-core/src/selection-actions.ts';
import {NativeBulkMedia} from '../src-next/screens/destinations/bulk-media.ts';
import * as nativeUnavailable from '../src-next/screens/library/selection-unavailable-actions.ts';
import {closeNativeDownload, getNativeDownloadSnapshot} from '../src-next/screens/downloads/request.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const basis = 'a'.repeat(64), total = 257;
function target() {
  return {mode: 'query', context: {}, queryId: 'exact-query', selector: {query: {libraryId: 'library', pivot: 'movies', selectionBasis: basis,
    filter: {variant: 'predicate', field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]}},
  expected: {catalogRevision: 'browse_' + 'b'.repeat(64)}, total, sourceTotal: total,
  refinement: {kind: null, excludedItems: []}, summary: {count: total, types: [{kind: 'movie', count: total}],
    library: {kind: 'single', libraryId: 'library'}, every: {delete: true, download: true}}};
}
const preview = () => ({kind: 'selection-action-preview', action: 'delete', preparationId: 'preparation', basis,
  total, files: total, bytes: 1234, bytesKnown: true, confirmation: String(total), confirmationKind: 'count',
  expiresAt: '2030-01-01T00:00:00Z', sample: [{itemId: 'item', title: 'Real title', kind: 'movie'}]});
const partialJob = {operationId: 'stable-operation', action: 'delete', state: 'partial', total, admitted: total,
  done: 0, failed: 1, filesDeleted: 2, bytesDeleted: 1024, errorCode: 'cancelled', retryAfterMs: 250};

async function librarySelection({supported = false, isTV = false} = {}) {
  const host = hooks(), held = target();
  const selected = {...held, total: 2, sourceTotal: 2, expected: {catalogRevision: 'reading-1'},
    selector: {query: {libraryId: held.selector.query.libraryId, pivot: held.selector.query.pivot,
      filter: held.selector.query.filter, sort: held.selector.query.sort}},
    summary: {...held.summary, count: 2, types: [{kind: 'movie', count: 2}], every: {delete: false, download: false}}};
  let view = {active: true, summary: selected.summary, selectedKeys: [], target: selected};
  const model = {getSnapshot: () => view, subscribe: () => () => {}, clear() {},
    captureOperation: () => ({target: view.target}), isOperationCurrent: (operation: {target: unknown}) => operation.target === view.target};
  const producer = {model, whole: () => undefined, dispose() {}};
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const api = {request: async (path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'GET' && path.startsWith('/v1/items?')) {
      const second = new URL(path, 'https://fixture.invalid').searchParams.has('cursor');
      const item = {id: second ? 'movie-b' : 'movie-a', kind: 'movie', libraryId: 'library', title: second ? 'Second' : 'First'};
      return second ? {items: [item]} : {items: [item], nextCursor: 'next'};
    }
    if (method === 'POST' && path === '/v1/admin/items/deletion') {
      return {count: 2, titles: 2, files: 2, bytes: 4096, alsoRemoves: []};
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  }};
  const context = {api, session: () => ({viewer: {role: 'owner'}, deviceId: 'device'}), subscribeAuthority: () => () => {},
    playback: {subscribe: () => () => {}, getSnapshot: () => ({dock: {}})},
    navigation: {getSnapshot: () => ({revision: 1})}};
  const source = {getSnapshot: () => ({phase: 'ready', library: {id: 'library'}, tab: {id: 'movies'}, revision: 'reading-1', queryRevision: 0, collection: {}}),
    subscribe: () => () => {}, hold: () => () => {}};
  const activation = {getSnapshot: () => ({visible: true}), subscribe: () => () => {}};
  const {createNativeLibrarySelection} = await screenModule(new URL('../src-next/shell/library-selection.tsx', import.meta.url), {
    '../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported})}},
    react: host.react, 'react-native': {View: 'View', Text: 'Text'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../packages/client-core/src/presentation/errors': {}, '../../../../packages/client-core/src/catalogue-query-selection': {},
    '../../../../packages/client-core/src/selection-model': {selectionFourthAction: () => 'my-list'},
    '../../../../packages/client-core/src/presentation/selection-notice': {}, '../../../../packages/client-core/src/media-selection': {},
    './selection-display': {selectedItemsLabel: () => '2 movies'},
    '../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'PhoneBar', DeleteMediaDialog: 'DeleteMediaDialog'},
    '../kit/theme': {useTheme: () => ({isTV, layers: {navigation: 1}, type: {}, colors: {}})}, '../kit/overlays/OverlayHost': {OverlayPortal: ({children}: any) => children}, '../kit/overlays': {}, '../kit/controls': {},
    '../screens/destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true, scope: {serverId: 'server', viewerId: 'owner'}})},
    '../screens/native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => {throw error;}})},
    '../kit/selection/LoadedDestinationPicker': {}, '../screens/search/loaded-destinations': {}, '../screens/destinations/bulk-media': {NativeBulkMedia},
    '../screens/library/model': {}, '../screens/library/play-selection': {createNativeLibraryPlaySelection: () => producer}, '../screens/library/selection-unavailable-actions': nativeUnavailable,
  });
  const port = createNativeLibrarySelection(context, source, activation);
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
  const render = () => {const element = port.render(); return nodes(host.render(() => element.type(element.props)));};
  const bar = () => render().find(node => node.type === (isTV ? 'SelectionBar' : 'PhoneBar'));
  const exceedLimit = () => {
    const large = {...selected, total: 201, sourceTotal: 201, summary: {...selected.summary, count: 201}};
    view = {...view, target: large, summary: large.summary};
  };
  port.start();
  return {port, render, bar, calls, exceedLimit};
}

const list = '/v1/items?library=library&kind=movie&year=2000..9999&sort=title&order=asc';
const listReads = [
  {path: list + '&limit=2', method: 'GET', body: undefined},
  {path: list + '&cursor=next&limit=1', method: 'GET', body: undefined},
];

test('native Library whole-view Delete resolves the filtered pages into one explicit deletion preview', async () => {
  const {port, render, bar, calls, exceedLimit} = await librarySelection();
  try {
    assert.deepEqual(bar().props.actions.find((offer: any) => offer.id === 'delete'), {id: 'delete', disabled: false});
    assert.ok(!bar().props.actions.some((offer: any) => offer.id === 'download'));
    bar().props.onAction('delete');
    await tick();
    assert.deepEqual(calls, [
      ...listReads,
      {path: '/v1/admin/items/deletion', method: 'POST', body: {ids: ['movie-a', 'movie-b']}},
    ]);
    const dialog = render().find(node => node.type === 'DeleteMediaDialog');
    assert.equal(dialog.props.testID, 'library-delete-media');
    assert.deepEqual(dialog.props.preview.targets.map((item: any) => item.itemId), ['movie-a', 'movie-b']);
    dialog.props.onOpenChange(false);
    exceedLimit();
    assert.deepEqual(bar().props.actions.find((offer: any) => offer.id === 'delete'),
      {id: 'delete', disabled: true, wholeQueryUnavailable: true, reason: 'next.selection.tooMany'});
    assert.ok(!bar().props.actions.some((offer: any) => offer.id === 'download'));
    bar().props.onAction('delete');
    assert.equal(calls.length, 3);
  } finally {port.dispose();}
});

test('native Library whole-view Download hands exact Browse IDs to the native download request', async () => {
  const {port, bar, calls, exceedLimit} = await librarySelection({supported: true});
  try {
    assert.deepEqual(bar().props.actions.find((offer: any) => offer.id === 'download'), {id: 'download', disabled: false});
    bar().props.onAction('download');
    await tick();
    assert.deepEqual(calls, listReads);
    const request = getNativeDownloadSnapshot();
    assert.deepEqual(request?.itemIds, ['movie-a', 'movie-b']);
    assert.equal(request?.title, '2 movies');
    closeNativeDownload(request);

    exceedLimit();
    assert.deepEqual(bar().props.actions.find((offer: any) => offer.id === 'download'),
      {id: 'download', disabled: true, wholeQueryUnavailable: true, reason: 'next.selection.native.downloadLimit'});
    bar().props.onAction('download');
    await tick();
    assert.deepEqual(calls, listReads);
    assert.equal(getNativeDownloadSnapshot(), undefined);
  } finally {closeNativeDownload(); port.dispose();}

  const tv = await librarySelection({supported: true, isTV: true});
  try {assert.ok(!tv.bar().props.actions.some((offer: any) => offer.id === 'download'));}
  finally {tv.port.dispose();}
});

test('native aggregate Delete dialog accepts 257 real preview totals and rejects retained callbacks from an older render', async () => {
  const host = hooks(), ready: any = {phase: 'ready', input: {action: 'delete', options: {deleteFiles: true}}, preparation: preview(), confirmation: '257', job: null, errorCode: null};
  let state = ready, confirms = 0, closes = 0, edits = 0;
  const action: any = {target: target(), current: () => true, getSnapshot: () => state, subscribe: () => () => {},
    controller: {getSnapshot: () => state, confirm: async () => {confirms++;}, cancel: async () => {}, setConfirmation: () => {}, retry: async () => {}},
    openDelete: async () => {edits++;}};
  const module = await screenModule(new URL('../src-next/kit/selection/QueryDeleteMediaDialog.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, bytes: (n: number) => String(n)}},
    '../../../../../packages/client-core/src/selection-actions': actions, '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'error'})},
    '../controls/Button': {Button: 'Button'}, '../controls/Switch': {Switch: 'Switch'}, '../controls/TextField': {TextField: 'TextField'},
    '../theme': {useTheme: () => ({isTV: false, colors: {danger: 'red'}})},
    './surface': {SelectionSurface: 'SelectionSurface', SelectionText: 'SelectionText', selectionFooterProps: () => ({})},
  });
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.footer), ...nodes(value.props.children)] : [];
  const render = () => nodes(host.render(() => module.NativeQueryDeleteMediaDialog({action, onClose: () => {closes++;}})));
  const first = render(), confirm = first.find(node => node.props.testID === 'query-action-confirm');
  assert.equal(confirm.props.disabled, false); await confirm.props.onPress(); assert.equal(confirms, 1);
  assert.ok(first.some(node => node.type === 'TextField')); assert.ok(first.some(node => node.type === 'Switch'));
  assert.equal(first.find(node => node.type === 'TextField').props.keyboardType, 'number-pad');
  state = {...ready, confirmation: ''}; confirm.props.onPress(); first.find(node => node.props.testID === 'query-delete-files').props.onCheckedChange(false);
  first.find(node => node.props.testID === 'query-action-close').props.onPress();
  assert.equal(confirms, 1); assert.equal(edits, 0); assert.equal(closes, 0);
  assert.equal(render().find(node => node.props.testID === 'query-action-confirm').props.disabled, true);
});

test('native terminal cancellation is explicit and partial Delete shows actual file effects', async () => {
  const host = hooks();
  const state = {phase: 'result', input: {action: 'delete', options: {deleteFiles: true}}, preparation: null, confirmation: '', errorCode: null,
    job: partialJob};
  const action: any = {target: target(), current: () => true, getSnapshot: () => state, subscribe: () => () => {},
    controller: {getSnapshot: () => state}};
  const module = await screenModule(new URL('../src-next/kit/selection/QueryDeleteMediaDialog.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {
      t: (key: string, values?: unknown) => `${key}:${JSON.stringify(values ?? {})}`, bytes: (n: number) => `bytes(${n})`}},
    '../../../../../packages/client-core/src/selection-actions': actions,
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({silent: true, body: undefined})},
    '../controls/Button': {Button: 'Button'}, '../controls/Switch': {Switch: 'Switch'}, '../controls/TextField': {TextField: 'TextField'},
    '../theme': {useTheme: () => ({isTV: false, colors: {danger: 'red'}})},
    './surface': {SelectionSurface: 'SelectionSurface', SelectionText: 'SelectionText', selectionFooterProps: () => ({})},
  });
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.footer), ...nodes(value.props.children)] : [];
  const deletion = nodes(host.render(() => module.NativeQueryDeleteMediaDialog({action, onClose: () => {}}))), text = deletion.filter(node => node.type === 'SelectionText').flatMap(node => node.props.children);
  assert.ok(text.includes('download.state.canceled:{}'), 'cancellation does not rely on the silent error presenter');
  assert.ok(text.includes('next.selection.native.deleteResult:{"removed":0,"failed":1}'));
  assert.ok(text.includes('next.selection.native.fileFacts:{"count":2,"size":"bytes(1024)"}'));
  assert.ok(!deletion.some(node => node.props.testID === 'query-action-cancel'));
});
