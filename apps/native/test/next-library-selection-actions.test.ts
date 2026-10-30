import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import * as actions from '../../../packages/client-core/src/selection-actions.ts';
import {SelectionActionController} from '../../../packages/client-core/src/selection-actions-controller.ts';
import {musicQualityLadder} from '../../../packages/client-core/src/playback-v1/quality.ts';
import {musicQualityPresentation} from '../../../packages/client-core/src/playback-v1/quality-presentation.ts';
import {authorityKey} from '../src-next/shell/model.ts';
import * as downloadRequests from '../src-next/screens/downloads/request.ts';
import * as nativeUnavailable from '../src-next/screens/library/selection-unavailable-actions.ts';
import * as wholeUnavailable from '../src-next/screens/library/whole-query-unavailable.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const basis = 'a'.repeat(64), total = 257;
function emitter() {
  const listeners = new Set<() => void>();
  return {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    emit: () => {for (const listener of [...listeners]) listener();}, size: () => listeners.size};
}
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

async function fixture() {
  const sources = emitter(), navigation = emitter(), activation = emitter(), authority = emitter(), model = emitter(), home = emitter();
  const calls: {path: string; method?: string; body: any; signal?: AbortSignal}[] = [], events: string[] = [];
  let supported = true, visible = true, live = true, operationCurrent = true, ids = 0;
  let viewer: any = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'};
  let session: any = {viewer, deviceId: 'device', sessionFamilyId: 'session', tokenGeneration: 1, authorizationHorizon: 1, installationId: 'installation'};
  let source: any = {phase: 'ready', library: {id: 'library'}, tab: {id: 'movies'}, queryRevision: 1, revision: 'page', collection: {}};
  let route: any = {route: {kind: 'library'}, revision: 1};
  let document: any = {viewerFence: 'fence', rows: []};
  const held = target(), operation = {target: held};
  let responder = async (path: string, method?: string, body?: any, _signal?: AbortSignal): Promise<unknown> => {
    if (method === 'POST' && path.endsWith('/preparations')) return preview(body.action);
    if (method === 'POST') return job();
    return undefined;
  };
  const context: any = {api: {request: async (path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    calls.push({path, method, body, signal}); return responder(path, method, body, signal);
  }}, session: () => session, authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}),
  current: () => live, subscribeAuthority: authority.subscribe, navigation: {...navigation, getSnapshot: () => route}, home: {...home, getSnapshot: () => ({document})}};
  const queue = {follow: async (operationId: string, deviceId: string) => {events.push(`follow:${operationId}:${deviceId}`);}};
  const lease = {device: {open: async () => {events.push('device-open');}}, service: {qualities: async () => {
    events.push('qualities'); return [{quality: 'original', label: 'Original', kind: 'source', targetDisplayHeight: 0},
      {quality: 'automatic', label: 'Automatic', kind: 'optimized', targetDisplayHeight: 0}];
  }}, dispose: () => {events.push('device-dispose');}};
  const module = await screenModule(new URL('../src-next/screens/library/selection-actions.ts', import.meta.url), {
    react: {default: React}, '../../../../../packages/client-core/src/selection-actions': actions,
    '../../../../../packages/client-core/src/selection-actions-controller': {SelectionActionController},
    '../../../../../packages/client-core/src/playback-v1/quality': {musicQualityLadder},
    '../../../../../packages/client-core/src/playback-v1/quality-presentation': {musicQualityPresentation},
    '../../shell/model': {authorityKey}, '../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported})}},
    '../downloads/device': {createNativeDownloadsDevice: () => lease}, '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => {ids++; return 'stable-operation';}})},
  });
  const sourcePort = {...sources, getSnapshot: () => source}, activationPort = {...activation, getSnapshot: () => ({visible})};
  const modelPort = {...model, captureOperation: () => operation, isOperationCurrent: (value: unknown) => operationCurrent && value === operation};
  const create = (signal?: AbortSignal) => module.createNativeLibrarySelectionAction(context, sourcePort, activationPort, modelPort, signal, queue);
  return {module, context, held, create, calls, events, sourcePort, activationPort, modelPort, ids: () => ids, responders: (next: typeof responder) => {responder = next;},
    createHome: () => module.createNativeHomeSelectionAction(context, {selection: modelPort}, undefined, queue),
    changeHome: () => {document = {...document}; home.emit();},
    changeSource: (patch: any) => {source = {...source, ...patch}; sources.emit();},
    changeRoute: () => {route = {route: {kind: 'library'}, revision: route.revision + 1}; navigation.emit();},
    changeViewer: () => {viewer = {...viewer, profileId: 'successor'}; session = {...session, viewer}; authority.emit();},
    changeSession: () => {session = {...session, tokenGeneration: 2}; authority.emit();},
    hide: () => {visible = false; activation.emit();}, staleOperation: () => {operationCurrent = false; model.emit();},
    unsupported: () => {supported = false;}, nonOwner: () => {viewer = {...viewer, role: 'user'}; session = {...session, viewer};},
    listenerCount: () => [sources, navigation, activation, authority, model, home].reduce((sum, value) => sum + value.size(), 0),
    retire: () => {live = false; authority.emit();}};
}

test('native exact offers preserve the 257-item selector and refuse refinement, non-owner and Vega sink', async () => {
  const f = await fixture();
  const input = f.module.nativeSelectionActionInput(f.held, 'delete', f.context);
  assert.deepEqual(input.selector, f.held.selector); assert.deepEqual(input.expected, f.held.expected);
  assert.deepEqual(input.options, {deleteFiles: false}); assert.equal(f.calls.length, 0);
  assert.throws(() => f.module.nativeSelectionActionInput({...f.held, refinement: {kind: 'movie', excludedItems: []}}, 'delete', f.context));
  assert.throws(() => f.module.nativeSelectionActionInput({...f.held, refinement: {kind: null, excludedItems: [{}]}}, 'delete', f.context));
  f.nonOwner(); assert.throws(() => f.module.nativeSelectionActionInput(f.held, 'delete', f.context));
  f.unsupported(); assert.throws(() => f.module.nativeSelectionActionInput(f.held, 'download', f.context));
  assert.equal(f.events.length, 0, 'no device or network attempt invents a TV sink');
});

test('native source retires idle choice, route, session, viewer, query, visibility and exact model operation; cleanup detaches', async () => {
  for (const change of ['changeRoute', 'changeViewer', 'changeSession', 'hide', 'staleOperation', 'retire'] as const) {
    const f = await fixture(), action = f.create(); let notified = 0;
    const off = action.subscribe(() => {notified++;});
    assert.ok(action.current()); assert.equal(f.listenerCount(), 5);
    f[change](); assert.equal(action.current(), false); assert.equal(action.getSnapshot(), undefined); assert.ok(notified > 0);
    off(); action.dispose(); assert.equal(f.listenerCount(), 0); assert.equal(f.calls.length, 0);
  }
  for (const patch of [{queryRevision: 2}, {revision: 'successor-page'}, {collection: {}}, {tab: {id: 'movies'}}]) {
    const f = await fixture(), action = f.create(); f.changeSource(patch); assert.equal(action.current(), false); action.dispose();
  }
});

test('native download reads real quality choices once and refuses unoffered quality without preparing a query', async () => {
  const f = await fixture(), action = f.create();
  try {
    const choices = await action.downloadQualities(); assert.equal(choices.length, 2);
    assert.equal(await action.downloadQualities(), choices); assert.deepEqual(f.events, ['device-open', 'qualities']);
    await assert.rejects(action.openDownload('made-up'), {code: 'unavailable'}); assert.equal(f.calls.length, 0);
  } finally {action.dispose();}
  assert.equal(f.events.at(-1), 'device-dispose'); assert.equal(f.listenerCount(), 0);
});

test('native Home interception retains its actual document and model operation without an explicit-identity conversion', async () => {
  const f = await fixture(), action = f.createHome();
  try {
    assert.equal(action.target, f.held); assert.ok(action.current()); assert.equal(f.calls.length, 0);
    assert.equal(action.source.expectedTotal, total);
    f.changeHome(); assert.equal(action.current(), false); assert.equal(action.getSnapshot(), undefined);
  } finally {action.dispose();}
  assert.equal(f.listenerCount(), 0);
});

test('native music query choices use canonical audio qualities instead of the video settings menu', async () => {
  const f = await fixture();
  f.held.selector.query.pivot = 'songs'; f.held.summary.types = [{kind: 'song', count: total}];
  const action = f.create();
  try {
    assert.equal(action.source.expectedTotal, total);
    const choices = await action.downloadQualities();
    assert.deepEqual(choices.map((choice: any) => choice.quality), musicQualityLadder.map(quality => quality.id));
    assert.ok(choices.every((choice: any) => !!choice.label && choice.targetDisplayHeight === 0));
    assert.deepEqual(f.events, ['device-open']); assert.equal(f.calls.length, 0);
  } finally {action.dispose();}
});

test('native real controller prepares exact options and replays an ambiguous confirmation with the original ID/body', async () => {
  const f = await fixture(), action = f.create(); let sends = 0;
  f.responders(async (path, method, _body) => {
    if (method === 'POST' && path.endsWith('/preparations')) return preview();
    if (method === 'POST') {if (++sends === 1) throw new Error('lost accepted response'); return job();}
    return undefined;
  });
  try {
    await action.openDelete(true); assert.equal(action.controller.getSnapshot().phase, 'ready');
    assert.deepEqual(f.calls[0].body, actions.selectionActionTargetInput(f.held as never, {action: 'delete', options: {deleteFiles: true}}));
    action.controller.setConfirmation('257'); await action.controller.confirm(); assert.equal(action.controller.getSnapshot().phase, 'uncertain');
    // Same query's new page may be the confirmed deletion's own publication.
    f.changeSource({revision: 'own-command-page', collection: {}, tab: {id: 'movies'}}); assert.ok(action.current());
    await action.controller.retry(); assert.equal(action.controller.getSnapshot().phase, 'result'); assert.equal(f.ids(), 1);
    const sent = f.calls.filter(call => call.method === 'POST' && !call.path.endsWith('/preparations'));
    assert.equal(sent.length, 2); assert.deepEqual(sent[0].body, sent[1].body); assert.equal(sent[0].body.operationId, 'stable-operation');
    assert.equal(f.calls.length, 3, 'one query preparation, two exact confirmation sends; no Browse/Detail enumeration');
  } finally {action.dispose();}
  assert.equal(f.calls.filter(call => call.method === 'DELETE').length, 0, 'closing accepted work never cancels it');
});

test('native changing deleteFiles starts a new real preview and clears typed confirmation', async () => {
  const f = await fixture(), action = f.create(); let preparations = 0;
  f.responders(async (path, method) => method === 'POST' && path.endsWith('/preparations') ? preview('delete', `prep-${++preparations}`) : undefined);
  try {
    await action.openDelete(false); action.controller.setConfirmation('257'); await action.openDelete(true);
    const state = action.controller.getSnapshot(); assert.equal(state.phase, 'ready'); assert.equal(state.confirmation, '');
    const sent = f.calls.filter(call => call.method === 'POST'); assert.deepEqual(sent.map(call => call.body.options), [{deleteFiles: false}, {deleteFiles: true}]);
    assert.equal(f.calls.filter(call => call.method === 'DELETE').length, 1, 'old demand is released');
  } finally {action.dispose();}
});

test('native Home keeps its confirmed command across own document/model publication before the POST response', async () => {
  const f = await fixture(), action = f.createHome();
  f.responders(async (path, method) => {
    if (method === 'POST' && path.endsWith('/preparations')) return preview();
    if (method === 'POST') {f.changeHome(); f.staleOperation(); assert.ok(action.current()); return job();}
    return undefined;
  });
  try {
    await action.openDelete(); action.controller.setConfirmation('257'); await action.controller.confirm();
    assert.equal(action.controller.getSnapshot().phase, 'result'); assert.ok(action.current());
    assert.deepEqual(f.calls[0].body.options, {deleteFiles: true}, 'same default as existing explicit Delete');
    f.changeRoute(); assert.equal(action.current(), false);
  } finally {action.dispose();}
});

test('actual native Library toolbar dispatches its whole query into the real DownloadSheet request without enumerating rows', async () => {
  const f = await fixture(), host = hooks();
  const view: any = {active: true, summary: f.held.summary, selectedKeys: [], target: f.held, version: 1};
  const model: any = {...f.modelPort, getSnapshot: () => view, clear() {}};
  const producer: any = {model, whole: () => undefined, dispose() {}};
  const module = await screenModule(new URL('../src-next/screens/library/selection.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View', Text: 'Text'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/errors': {}, '../../../../../packages/client-core/src/catalogue-query-selection': {},
    '../../../../../packages/client-core/src/selection-model': {selectionFourthAction: () => 'my-list'}, '../../../../../packages/client-core/src/media-selection': {},
    '../../shell/selection-display': {selectedItemsLabel: () => '257 selected'},
    '../../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'PhoneBar'},
    '../../kit/theme': {useTheme: () => ({isTV: false, layers: {navigation: 1}})}, '../../kit/overlays': {}, '../../kit/controls': {},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true})},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => {throw error;}})},
    '../destinations/DestinationPicker': {}, '../search/loaded-destinations': {}, '../destinations/bulk-media': {},
    './model': {nativeLibraryEntryKey: () => ''}, './play-selection': {createNativeLibraryPlaySelection: () => producer},
    './selection-unavailable-actions': nativeUnavailable, './whole-query-unavailable': wholeUnavailable,
    './selection-actions': f.module, '../downloads/request': downloadRequests, '../../platform/offline-downloads': {offlineDownloads: {}},
  });
  const port = module.createNativeLibrarySelection(f.context, f.sourcePort, f.activationPort);
  const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
  try {
    port.start(); const element = port.render();
    const bar = nodes(host.render(() => element.type(element.props))).find(node => node.type === 'SelectionBar');
    assert.equal(bar.props.actions.find((offer: any) => offer.id === 'download').disabled, false);
    bar.props.onAction('download');
    const request = downloadRequests.getNativeDownloadSnapshot()!;
    assert.equal(request.queryAction?.target, f.held); assert.equal(request.queryAction?.source.expectedTotal, 257);
    assert.equal(f.calls.length, 0, 'raising the genuine quality chooser does not enumerate Browse or Detail');
    bar.props.onAction('download'); assert.equal(downloadRequests.getNativeDownloadSnapshot(), request);
    downloadRequests.closeNativeDownload(request); assert.equal(request.queryAction?.current(), false);
  } finally {port.dispose(); downloadRequests.closeNativeDownload();}
});

test('native download persists the exact operation/device follow before confirm transport and leaves transfer completion distinct', async () => {
  const f = await fixture(), action = f.create();
  f.responders(async (path, method, _body) => {
    if (method === 'POST' && path.endsWith('/preparations')) return preview('download');
    if (method === 'POST') {assert.ok(f.events.includes('follow:stable-operation:device')); return job('download');}
    return undefined;
  });
  try {
    await action.openDownload('automatic'); assert.deepEqual(f.calls[0].body.options, {deviceId: 'device', quality: 'automatic'});
    action.controller.setConfirmation('257'); await action.controller.confirm();
    assert.equal(action.controller.getSnapshot().job?.done, 257); assert.equal(f.ids(), 1);
    assert.deepEqual(f.events.filter(value => value.startsWith('follow:')), ['follow:stable-operation:device']);
    assert.equal('transferred' in action.controller.getSnapshot(), false);
  } finally {action.dispose();}
});

test('native preparation retirement releases a late demand and cannot show it to a successor', async () => {
  const f = await fixture(), action = f.create(); let finish!: (value: unknown) => void;
  f.responders(async (_path, method) => method === 'POST' ? await new Promise(resolve => {finish = resolve;}) : undefined);
  try {
    const opening = action.openDelete(); await tick(); f.changeViewer();
    assert.equal(f.calls[0].signal?.aborted, true); finish(preview()); await opening; await tick();
    assert.equal(action.getSnapshot(), undefined); assert.equal(action.controller.getSnapshot().phase, 'retired');
    assert.equal(f.calls.filter(call => call.method === 'DELETE').length, 1);
  } finally {action.dispose();}
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
