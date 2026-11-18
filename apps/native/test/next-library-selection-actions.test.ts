import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
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
const preview = (action = 'delete', preparationId = 'preparation') => ({kind: 'selection-action-preview', action, preparationId, basis,
  total, files: total, bytes: 1234, bytesKnown: action === 'delete', confirmation: String(total), confirmationKind: 'count',
  expiresAt: '2030-01-01T00:00:00Z', sample: [{itemId: 'item', title: 'Real title', kind: 'movie'}]});
const job = (action = 'delete', state = 'complete') => ({operationId: 'stable-operation', action, state, total, admitted: total,
  done: state === 'complete' ? total : 0, failed: 0, bytesDeleted: 0, filesDeleted: 0, retryAfterMs: 250});

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
    navigation: {getSnapshot: () => ({revision: 1})}};
  const source = {getSnapshot: () => ({phase: 'ready', library: {id: 'library'}, tab: {id: 'movies'}, revision: 'reading-1', queryRevision: 0, collection: {}}),
    subscribe: () => () => {}, hold: () => () => {}};
  const activation = {getSnapshot: () => ({visible: true}), subscribe: () => () => {}};
  const {createNativeLibrarySelection} = await screenModule(new URL('../src-next/screens/library/selection.tsx', import.meta.url), {
    '../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported})}},
    react: host.react, 'react-native': {View: 'View', Text: 'Text'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/errors': {}, '../../../../../packages/client-core/src/catalogue-query-selection': {},
    '../../../../../packages/client-core/src/selection-model': {selectionFourthAction: () => 'my-list'},
    '../../../../../packages/client-core/src/presentation/selection-notice': {}, '../../../../../packages/client-core/src/media-selection': {},
    '../../shell/selection-display': {selectedItemsLabel: () => '2 movies'},
    '../../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'PhoneBar', DeleteMediaDialog: 'DeleteMediaDialog'},
    '../../kit/theme': {useTheme: () => ({isTV, layers: {navigation: 1}, type: {}, colors: {}})}, '../../kit/overlays': {}, '../../kit/controls': {},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true, scope: {serverId: 'server', viewerId: 'owner'}})},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => {throw error;}})},
    '../destinations/DestinationPicker': {}, '../search/loaded-destinations': {}, '../destinations/bulk-media': {NativeBulkMedia},
    './model': {}, './play-selection': {createNativeLibraryPlaySelection: () => producer}, './selection-unavailable-actions': nativeUnavailable,
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
    assert.equal(request?.queryAction, undefined);
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
    openDelete: async () => {edits++;}, downloadQualities: async () => []};
  const module = await screenModule(new URL('../src-next/screens/library/SelectionActionSurface.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, bytes: (n: number) => String(n)}},
    '../../../../../packages/client-core/src/selection-actions': actions, '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'error'})},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/controls/Switch': {Switch: 'Switch'}, '../../kit/controls/TextField': {TextField: 'TextField'},
    '../../kit/theme': {useTheme: () => ({isTV: false, colors: {danger: 'red'}})},
    '../../kit/selection/surface': {SelectionSurface: 'SelectionSurface', SelectionText: 'SelectionText', selectionFooterProps: () => ({})},
  });
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.footer), ...nodes(value.props.children)] : [];
  const render = () => nodes(host.render(() => module.NativeQuerySelectionActionSurface({action, kind: 'delete', onClose: () => {closes++;}})));
  const first = render(), confirm = first.find(node => node.props.testID === 'query-action-confirm');
  assert.equal(confirm.props.disabled, false); await confirm.props.onPress(); assert.equal(confirms, 1);
  assert.ok(first.some(node => node.type === 'TextField')); assert.ok(first.some(node => node.type === 'Switch'));
  assert.equal(first.find(node => node.type === 'TextField').props.keyboardType, 'number-pad');
  state = {...ready, confirmation: ''}; confirm.props.onPress(); first.find(node => node.props.testID === 'query-delete-files').props.onCheckedChange(false);
  first.find(node => node.props.testID === 'query-action-close').props.onPress();
  assert.equal(confirms, 1); assert.equal(edits, 0); assert.equal(closes, 0);
  assert.equal(render().find(node => node.props.testID === 'query-action-confirm').props.disabled, true);
});

test('native terminal cancellation is explicit, partial Delete shows actual file effects, and Download says processed', async () => {
  const host = hooks();
  let state: any = {phase: 'result', input: {action: 'delete', options: {deleteFiles: true}}, preparation: null, confirmation: '', errorCode: null,
    job: {...job('delete', 'partial'), done: 0, failed: 1, filesDeleted: 2, bytesDeleted: 1024, errorCode: 'cancelled'}};
  const action: any = {target: target(), current: () => true, getSnapshot: () => state, subscribe: () => () => {},
    controller: {getSnapshot: () => state}, downloadQualities: async () => []};
  const module = await screenModule(new URL('../src-next/screens/library/SelectionActionSurface.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {
      t: (key: string, values?: unknown) => `${key}:${JSON.stringify(values ?? {})}`, bytes: (n: number) => `bytes(${n})`}},
    '../../../../../packages/client-core/src/selection-actions': actions,
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({silent: true, body: undefined})},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/controls/Switch': {Switch: 'Switch'}, '../../kit/controls/TextField': {TextField: 'TextField'},
    '../../kit/theme': {useTheme: () => ({isTV: false, colors: {danger: 'red'}})},
    '../../kit/selection/surface': {SelectionSurface: 'SelectionSurface', SelectionText: 'SelectionText', selectionFooterProps: () => ({})},
  });
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.footer), ...nodes(value.props.children)] : [];
  const render = (kind: string) => nodes(host.render(() => module.NativeQuerySelectionActionSurface({action, kind, onClose: () => {}})));
  const deletion = render('delete'), text = deletion.filter(node => node.type === 'SelectionText').flatMap(node => node.props.children);
  assert.ok(text.includes('download.state.canceled:{}'), 'cancellation does not rely on the silent error presenter');
  assert.ok(text.includes('next.selection.native.deleteResult:{"removed":0,"failed":1}'));
  assert.ok(text.includes('next.selection.native.fileFacts:{"count":2,"size":"bytes(1024)"}'));
  assert.ok(!deletion.some(node => node.props.testID === 'query-action-cancel'));
  state = {...state, input: {action: 'download', options: {quality: 'original', deviceId: 'device'}}, job: job('download')};
  const download = render('download').filter(node => node.type === 'SelectionText').flatMap(node => node.props.children);
  assert.ok(download.includes('next.selection.jobProgress:{"done":257,"total":257}'));
  assert.ok(!download.some((value: unknown) => typeof value === 'string' && value.startsWith('downloads.requestProgress')));
});

test('native exact inventory waits for terminal admission, honors real queue backpressure and reads one bounded filtered page', async () => {
  let closed = false, reads = 0, admissions = 0;
  const calls: string[] = [];
  const module = await screenModule(new URL('../src-next/screens/library/selection-action-downloads.ts', import.meta.url), {
    react: {default: React}, '../../../../../packages/client-core/src/selection-actions': {...actions,
      getSelectionAction: async () => {reads++; throw new Error('short-lived status must not gate transfer inventory');}},
  });
  const preparation = (i: number) => ({id: `prep-${i}`, itemId: `item-${i}`, libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'manual', state: 'ready',
    artifact: {kind: 'source', sha256: 'a'.repeat(64), bytes: 1024, estimated: false}, title: {kind: 'movie', title: `Film ${i}`, onServer: true},
    actions: ['remove'], revision: 1, createdAt: '2026-10-28T00:00:00Z', updatedAt: '2026-10-28T00:00:00Z', readyAt: '2026-10-28T00:00:00Z', expiresAt: '2030-01-01T00:00:00Z'});
  const api = {request: async (path: string) => {
    calls.push(path); const query = new URL(path, 'https://fixture.invalid').searchParams;
    assert.equal(query.get('operationId'), 'stable-operation'); assert.equal(query.get('deviceId'), 'device');
    assert.equal(query.get('limit'), '20'); assert.equal(query.get('cursor'), null);
    return {items: Array.from({length: 20}, (_, i) => preparation(i)), nextCursor: 'next-exact-page', closed};
  }};
  const intent = Object.freeze({operationId: 'stable-operation', deviceId: 'device', cursor: ''}), signal = new AbortController().signal;
  const admitted: any[] = [];
  const options = {api, intent, signal, isCurrent: () => true, room: 0, admit: async (page: any) => {admissions++; admitted.push(page);}};
  assert.equal(await module.stepNativeSelectionActionDownloads(options), 'blocked'); assert.equal(reads, 0);
  assert.equal(await module.stepNativeSelectionActionDownloads({...options, room: 200}), 'waiting'); assert.equal(calls.length, 1);
  closed = true;
  assert.equal(await module.stepNativeSelectionActionDownloads({...options, room: 200}), 'more');
  assert.equal(calls.length, 2); assert.equal(admissions, 1); assert.equal(reads, 0); assert.equal(admitted[0].items.length, 20);
  assert.equal(admitted[0].nextCursor, 'next-exact-page'); assert.equal(admitted[0].complete, false); assert.equal(intent.cursor, '');
  await assert.rejects(module.stepNativeSelectionActionDownloads({...options, room: 200, admit: async () => {throw new Error('native write failed');}}), /native write failed/);
  assert.equal(intent.cursor, '', 'failed native persistence cannot advance the durable cursor');
});

test('native exact inventory refuses changed scope and repeated cursor, and enrolls partial cancellation inventory truthfully', async () => {
  const module = await screenModule(new URL('../src-next/screens/library/selection-action-downloads.ts', import.meta.url), {
    react: {default: React}, '../../../../../packages/client-core/src/selection-actions': {...actions,
      getSelectionAction: async () => actions.parseSelectionActionJob({...job('download', 'partial'), done: 1, errorCode: 'cancelled'})},
  });
  const intent = {operationId: 'stable-operation', deviceId: 'device', cursor: 'last'}, abort = new AbortController();
  let live = true, admissions = 0;
  const options = {intent, signal: abort.signal, isCurrent: () => live, room: 20, admit: async () => {admissions++;},
    api: {request: async () => ({items: [], nextCursor: 'last', closed: true})}};
  await assert.rejects(module.stepNativeSelectionActionDownloads(options), {code: 'invalid_response'});
  assert.equal(admissions, 0);
  assert.equal(await module.stepNativeSelectionActionDownloads({...options, api: {request: async () => ({items: [], nextCursor: '', closed: true})}}), 'complete');
  assert.equal(admissions, 1);
  await assert.rejects(module.stepNativeSelectionActionDownloads({...options, api: {request: async () => {live = false; return {items: [], nextCursor: '', closed: true};}}}), {name: 'AbortError'});
  assert.equal(admissions, 1, 'late inventory never enrolls into a successor profile');
});
