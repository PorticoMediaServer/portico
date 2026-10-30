import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedOperations} from '../src/screens/saved/operations.ts';
import {sameSavedDownloadsScope, type SavedDownloadsScope, type SavedLocalDownload} from '../src/screens/saved/downloads-port.ts';
import {diskFractions, localDownloadGroups, localGroupPercent, measuredPercent} from '../src/screens/saved/downloads-model.ts';
import {createSavedRuntime} from '../src/screens/saved/runtime.ts';
import {sameSavedRoute, savedViewKey} from '../src/screens/saved/route.ts';
import {playlistRemovalUndo} from '../src/screens/saved/removal.ts';
import type {SavedReceipt} from '@core/saved.ts';
import {PersonalSavedService} from '@core/personal-saved.ts';
import {personalEntriesWindow} from '../src/screens/saved/window.ts';

const scope: SavedDownloadsScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
test('Undo accepts only the committed current-viewer removal receipt for this exact occurrence', () => {
  const expected = {serverId: 'server', viewerFence: 'viewer', playlistId: 'playlist', entryId: 'occurrence'};
  const now = Date.parse('2026-10-23T12:00:00Z');
  const receipt: SavedReceipt = {...expected, operationId: 'operation', revision: 4, deleted: false, undoToken: 'opaque-token', undoUntil: '2026-10-23T12:01:00Z'};
  assert.deepEqual(playlistRemovalUndo(receipt, expected, 'Exact title', now), {token: 'opaque-token', until: now + 60_000, title: 'Exact title'});
  for (const field of ['serverId', 'viewerFence', 'playlistId', 'entryId'] as const) assert.equal(playlistRemovalUndo({...receipt, [field]: 'different'}, expected, 'Exact title', now), undefined, field);
  for (const changed of [{deleted: true}, {undoToken: undefined}, {undoUntil: 'invalid'}, {undoUntil: '2026-10-23T12:00:00Z'}]) assert.equal(playlistRemovalUndo({...receipt, ...changed}, expected, 'Exact title', now), undefined);
  assert.equal(playlistRemovalUndo(null, expected, 'Exact title', now), undefined);
});
test('personal resource grid follows the real opaque cursor without turning a library count into resident entities', async () => {
  const resource = {serverId: 'server', viewerFence: 'fence', id: 'collection', kind: 'collection', name: 'Collection', summary: '', visibility: 'private', revision: 1, role: 'owner', entryCount: 2000, actions: ['pin', 'update', 'delete', 'entries'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []};
  const entry = (id: string) => ({id, hidden: false, media: {id, libraryId: 'library', kind: 'movie', title: id}});
  const fields = {query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [], totalCount: 2000, start: 0};
  let next!: (value: unknown) => void, calls = 0;
  const service = new PersonalSavedService({async request<T>(path: string): Promise<T> {
    calls++;
    if (path.includes('cursor=opaque-next')) return await new Promise<unknown>(resolve => { next = resolve; }) as T;
    return {resource, entries: [entry('one'), entry('two'), entry('three')], nextCursor: 'opaque-next', ...fields} as T;
  }}, {serverId: 'server', viewerId: 'viewer'}, async () => '00000000-0000-4000-8000-000000000001');
  const window = personalEntriesWindow(service);
  try {
    await service.select({view: 'resource', resourceId: 'collection'});
    assert.equal(window.getSnapshot().total, 3);
    assert.equal(window.getSnapshot().totalIsLowerBound, true);
    assert.deepEqual(window.slotAt(3), {kind: 'placeholder', index: 3, key: 'placeholder:3'});
    window.ensureRange(0, 2); window.ensureRange(0, 3);
    assert.equal(calls, 1, 'scrolling the current page cannot silently replace it');
    const advancing = service.next();
    const published = new Promise<void>(resolve => { const off = service.subscribe(() => { if (!service.getSnapshot().loading) { off(); resolve(); } }); });
    next({resource, entries: [entry('four')], nextCursor: 'opaque-later', ...fields, start: 3}); await published; await advancing;
    assert.deepEqual(window.residentItems().map(value => value.id), ['four']);
    assert.equal(window.getSnapshot().total, 1); assert.equal(window.getSnapshot().totalIsLowerBound, true);
    assert.equal(window.keyAt(0), 'four'); window.dispose(); window.ensureRange(0, 4); assert.equal(calls, 2);
  } finally { window.dispose(); service.dispose(); }
});
test('every captured identity boundary distinguishes the binding', () => {
  assert.equal(sameSavedDownloadsScope(scope, {...scope}), true);
  for (const field of ['serverId', 'authority', 'accountId', 'profileId', 'sessionFamilyId'] as const) {
    assert.equal(sameSavedDownloadsScope(scope, {...scope, [field]: field === 'authority' ? 'hosted' : 'other'}), false, field);
  }
});
test('authority closing while an operation completes suppresses all UI effects', async () => {
  let current = true, complete!: (value: number) => void, callbacks = 0;
  const operations = new SavedOperations(() => current);
  const pending = operations.run(() => new Promise<number>(resolve => { complete = resolve; }), () => callbacks++);
  current = false; complete(1); await pending;
  assert.equal(callbacks, 0);
});
test('parent disposal aborts pending native/API work and cannot reopen for another family', async () => {
  let signal: AbortSignal | undefined, reject!: (error: unknown) => void, callbacks = 0;
  const operations = new SavedOperations(() => true);
  const pending = operations.run(value => { signal = value; return new Promise((_, fail) => { reject = fail; }); }, () => callbacks++);
  operations.dispose(); assert.equal(signal?.aborted, true);
  reject(new Error('late native result')); await pending;
  await operations.run(async () => { callbacks++; }); assert.equal(callbacks, 0);
});
test('current native failures stay actionable; late failures are silent', async () => {
  const operations = new SavedOperations(() => true), failure = new Error('native write refused');
  await assert.rejects(operations.run(async () => { throw failure; }), error => error === failure);
  operations.dispose(); await operations.run(async () => { throw failure; });
});
test('route effect cleanup aborts the old callback even when the current view opens again', async () => {
  let active = true, complete!: () => void, callbacks = 0, signal: AbortSignal | undefined;
  const operations = new SavedOperations(() => active);
  const pending = operations.run(value => { signal = value; return new Promise<void>(resolve => { complete = resolve; }); }, () => callbacks++);
  active = false; operations.abortPending(); active = true; complete(); await pending;
  assert.equal(signal?.aborted, true); assert.equal(callbacks, 0);
  await operations.run(async () => {}, () => callbacks++);
  assert.equal(callbacks, 1); operations.dispose();
});
test('Saved route guards compare the complete query independently of property order', () => {
  const route = {view: 'watchlist', filter: 'unwatched', sort: 'title', direction: 'asc', shape: 'posters'} as const;
  assert.equal(sameSavedRoute({view: 'watchlist', shape: 'posters', sort: 'title', direction: 'asc', filter: 'unwatched'}, route), true);
  for (const other of [{...route, filter: 'all'} as const, {...route, direction: 'desc'} as const, {...route, shape: 'squares'} as const, {...route, sort: 'added'} as const]) assert.equal(sameSavedRoute(other, route), false);
  assert.equal(sameSavedRoute({view: 'playlist', playlistId: 'old'}, {view: 'playlist', playlistId: 'new'}), false);
  assert.equal(sameSavedRoute({view: 'history', period: '30d'}, {view: 'history', period: 'all'}), false);
  assert.equal(sameSavedRoute({view: 'resource', resourceId: 'a'}, {view: 'resource', resourceId: 'b'}), false);
  assert.equal(sameSavedRoute(null, route), false);
});
test('dialogs and native actions cannot retain UI state in a replacement captured viewer', () => {
  const key = savedViewKey(scope, {view: 'downloads'});
  assert.equal(savedViewKey({...scope}, {view: 'downloads'}), key);
  for (const field of ['serverId', 'authority', 'accountId', 'profileId', 'sessionFamilyId'] as const) assert.notEqual(savedViewKey({...scope, [field]: field === 'authority' ? 'hosted' : 'other'}, {view: 'downloads'}), key, field);
  assert.notEqual(savedViewKey(scope, {view: 'playlists'}), key);
});
test('storage bar uses actual OS total/free and all native Portico bytes', () => {
  assert.deepEqual(diskFractions({totalBytes: 1000, freeBytes: 400, usedBytes: 70}), {portico: 0.07, other: 0.53});
  for (const storage of [{totalBytes: 0, freeBytes: 0, usedBytes: 0}, {totalBytes: 100, freeBytes: 80, usedBytes: 30}, {totalBytes: 100, freeBytes: -1, usedBytes: 0}]) assert.equal(diskFractions(storage), undefined);
  assert.equal(measuredPercent(null), undefined); assert.equal(measuredPercent(NaN), undefined); assert.equal(measuredPercent(101), undefined); assert.equal(measuredPercent(0), 0);
});
test('native group progress requires exact consistent byte observations', () => {
  assert.equal(localGroupPercent({bytes: 400, bytesDone: 100}), 25);
  assert.equal(localGroupPercent({bytes: 400, bytesDone: 0}), 0);
  for (const value of [{bytes: 0, bytesDone: 0}, {bytes: 400, bytesDone: 401}, {bytes: 400, bytesDone: -1}, {bytes: 400, bytesDone: 1.5}, {bytes: Number.MAX_SAFE_INTEGER + 1, bytesDone: 10}]) assert.equal(localGroupPercent(value), undefined);
});
test('local group identity survives duplicate show names; missing-server bytes remain visible', () => {
  const item = (id: string, parentId: string, onServer = true): SavedLocalDownload => ({id, revision: 1, preparationId: id, itemId: id, quality: 'original', state: 'ready', held: false, bytes: 100, bytesDone: 100, percent: 100, offlineAccess: 'valid', onServer, title: {kind: 'episode', title: id, year: 2026, durationSeconds: 1, parentId, parentTitle: 'Same show name', groupId: '', groupTitle: '', groupNumber: 1, number: 1, artist: '', onServer}});
  const groups = localDownloadGroups([item('a', 'show-a'), item('b', 'show-b'), item('c', 'show-a'), item('d', 'show-a', false)]);
  assert.deepEqual(groups.map(group => [group.id, group.items.map(row => row.id)]), [['show:show-a', ['a', 'c']], ['show:show-b', ['b']], ['server', ['d']]]);
  assert.equal('count' in groups[0], false);
});
test('the actual Saved runtime refuses incomplete authority captures before requesting data', () => {
  for (const key of ['serverId', 'accountId', 'profileId', 'sessionFamilyId'] as const) {
    assert.throws(() => createSavedRuntime({scope: {...scope, [key]: ''}, api: {request: async () => { throw new Error('unexpected API call'); }}, isCurrent: () => true}), /complete captured/);
  }
});
test('no server service begins a read after the parent authority closes', async () => {
  let calls = 0;
  const runtime = createSavedRuntime({scope, api: {request: async () => { calls++; throw new Error('unexpected API call'); }}, isCurrent: () => false});
  await runtime.select({view: 'watchlist'});
  assert.equal(calls, 0); runtime.dispose();
});
test('parent disposal aborts both real shape-service transports and drops delayed responses', async () => {
  const signals: AbortSignal[] = [];
  const runtime = createSavedRuntime({scope, isCurrent: () => true, api: {request: (_path, _method, _body, signal) => {
    assert.ok(signal); signals.push(signal);
    return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Cancelled', 'AbortError')), {once: true}));
  }}});
  const pending = runtime.select({view: 'watchlist'});
  assert.equal(signals.length, 2); runtime.dispose(); await pending;
  assert.ok(signals.every(signal => signal.aborted));
  assert.equal(runtime.posters.getSnapshot().projection, null); assert.equal(runtime.squares.getSnapshot().projection, null);
});

// FX216: exercise the actual Web consumer against the real service's failure
// snapshot. Reuse the existing component hook fixture; no browser/runner job.
import {DownloadsService} from '@core/downloads-service.ts';
import {defaultI18n} from '@i18n';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {savedText} from '../src/screens/saved/text.ts';

test('actual ServerDownloads keeps Retry for transient failures and shows feature-off/embedded-null only for genuine absence', async () => {
  const failures = [
    {error: Object.assign(new Error('busy'), {status: 503, code: 'server_busy'}), absent: false},
    {error: Object.assign(new Error('timeout'), {status: 503, code: 'timeout'}), absent: false},
    {error: new TypeError('offline'), absent: false},
    {error: Object.assign(new Error('unsupported route'), {status: 404}), absent: true},
    {error: Object.assign(new Error('disabled'), {status: 503, code: 'downloads_unavailable'}), absent: true},
  ];
  for (const {error, absent} of failures) {
    let failure: unknown = error, listReads = 0;
    const service = new DownloadsService({request: async <T>(path: string): Promise<T> => {
      if (!path.startsWith('/v1/downloads/preparations?')) throw new Error(`Unexpected fixture request: ${path}`);
      listReads++; if (failure) throw failure;
      return {items: [], nextCursor: ''} as T;
    }});
    const operations = new SavedOperations(() => true), host = hooks();
    const screen = await componentModule(new URL('../src/screens/saved/Downloads.tsx', import.meta.url), {
      react: host.react, '@i18n': {defaultI18n}, './text.ts': {savedText}, './saved.module.css': {default: {}},
      '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch'},
      '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'}, '../../kit/Icon': {Icon: 'Icon'},
      '../../kit/states': {StateView: 'StateView'}, './SavedDialog': {SavedDialog: 'SavedDialog'},
    });
    const render = (embedded = false) => host.render(() => screen.ServerDownloads({service, operations, embedded, managed: true, online: true,
      artwork: () => undefined, effects: {openItem() {}, saveFile: async () => {}, reportError(error: unknown) {throw error;}}}));
    type Node = {type: unknown; props: Record<string, any>};
    const nodes = (value: unknown): Node[] => {
      if (Array.isArray(value)) return value.flatMap(nodes);
      if (!value || typeof value !== 'object' || !('props' in value)) return [];
      const node = value as Node; return [node, ...nodes(node.props.children), ...nodes(node.props.footer)];
    };
    try {
      await service.refresh(); assert.equal(service.getSnapshot().unavailable, absent);
      const visible = render(), entries = nodes(visible), off = entries.filter(node => node.type === 'StateView' && node.props.title === defaultI18n.t('web.downloads.offTitle'));
      if (absent) {
        assert.equal(off.length, 1); assert.equal(render(true), null, 'genuine absence hides an embedded server panel');
        assert.equal(entries.some(node => node.type === 'SavedDialog'), false); assert.equal(listReads, 1);
      } else {
        assert.equal(off.length, 0); assert.notEqual(render(true), null, 'transient failure must not hide the embedded panel as unsupported');
        assert.equal(service.getSnapshot().failure, error); assert.equal(service.getSnapshot().phase, 'error');
        const dialog = entries.find(node => node.type === 'SavedDialog' && node.props.title === savedText('next.saved.loadFailed'));
        assert(dialog); assert.equal(dialog.props.open, true);
        const retry = nodes(dialog.props.footer).find(node => node.type === 'Button' && node.props.children.includes(defaultI18n.t('action.tryAgain')));
        assert(retry); assert.equal(retry.props.disabled, false);
        failure = undefined;
        const ready = new Promise<void>(resolve => {const off = service.subscribe(() => {if (service.getSnapshot().phase === 'ready') {off(); resolve();}});});
        retry.props.onClick(); await ready;
        assert.equal(listReads, 2, 'the actual Retry callback invokes the real service once'); assert.equal(service.getSnapshot().unavailable, false);
        assert.equal(service.getSnapshot().failure, undefined); assert.equal(nodes(render()).find(node => node.type === 'SavedDialog')?.props.open, false);
      }
    } finally {operations.dispose(); service.dispose();}
  }
});
