import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';
import {NativeWatchPresenters} from '../src-next/screens/watch/presenters.ts';
import {NativeScreenServices} from '../src-next/screens/screen-services.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {FileInfoSession} from '../../../packages/client-core/src/file-info-session.ts';
import {groupRow, groupsPage, memberRow, membersPage, opening, ownerSurface} from './helpers/file-info-windows.ts';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const entry = {id: 'movie', kind: 'movie' as const, title: 'Real title', displayTitle: 'Real title: Edition', libraryId: 'library'};
const offered = parseOptions({itemId: 'movie', versions: [{id: 'asset', parts: [{id: 'asset', index: 0, durationMs: 60000}], durationMs: 60000, sizeBytes: 1024, video: [], audio: [], subtitles: []}]});
const occurrence = 'occ-asset';
/** One version, one file, the owner's view of an opening; `held` keeps the groups lease open (a next window exists). */
const fileInfoServer = (path: string, state: {held: boolean; manage: typeof ownerSurface['manage']; serial: number}) => {
  if (path.includes('/groups/members')) return membersPage('q-members', 'g1', [memberRow(occurrence, 'asset', {path: '/actual/asset.mkv'})]);
  return groupsPage(`q-groups-${++state.serial}`, {...ownerSurface, groupCount: state.held ? 2 : 1, manage: state.manage}, [groupRow(1, {headOccurrence: occurrence})], state.held ? {nextCursor: 'n1'} : {});
};
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
const drain = () => new Promise(resolve => setImmediate(resolve));
async function fixture(over: {manage?: typeof ownerSurface['manage']; held?: boolean} = {}) {
  let active = true, changed = 0;
  const state = {held: over.held ?? false, manage: over.manage ?? ownerSurface.manage, serial: 0};
  let respond = async (path: string, method: string): Promise<unknown> => method === 'DELETE' ? null : fileInfoServer(path, state);
  const calls: {path: string; method: string; body: unknown; signal?: AbortSignal | null}[] = [];
  const listeners = new Set<() => void>();
  const api = new HttpLocalApi('https://portico.test', '', async (input, init) => {
    const url = new URL(String(input)), method = init?.method ?? 'GET';
    calls.push({path: url.pathname + url.search, method, body: init?.body && JSON.parse(String(init.body)), signal: init?.signal});
    const body = await respond(url.pathname, method);
    return body instanceof Response ? body : new Response(JSON.stringify(body), {status: 200});
  });
  const navigation = new NativeShellNavigation(authority, () => authority);
  navigation.open({kind: 'title', entry}, authority);
  const owner = {api, navigation, current: () => active, authority: () => authority, subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const common = new NativeScreenServices(owner, async () => '11111111-1111-4111-8111-111111111111');
  const context = {...owner, session: () => ({viewer: {...authority, authority: 'local'}}), playback: {getSnapshot: () => ({fence: undefined}), subscribe: () => () => {}}} as unknown as NativeShellBootstrapContext;
  const presenters = new NativeWatchPresenters(context, common);
  const capture = {entry, authority, routeRevision: navigation.getSnapshot().revision, expectedFence: undefined, current: () => active};
  // The factory's own work: one session over the accepted opening, its first window and the owner's files read.
  const open = async () => {
    const session = new FileInfoSession({api, opening, isCurrent: () => active});
    await session.groups();
    if (session.getSnapshot().surface?.manage) await session.members('g1');
    return session;
  };
  const session = await open();
  const fileInfo = async () => {presenters.openFileInfo(capture, 'real content', {session, kind: 'movie', changed: async () => {changed++;}});};
  presenters.openMenu(capture, {fileInfo});
  await presenters.fileInfo(presenters.getSnapshot()!);
  return {presenters, common, capture, session, open, calls, changed: () => changed, state,
    leases: (lane = '') => calls.filter(call => call.method === 'DELETE' && call.path.startsWith('/v1/file-info-queries/' + lane)).map(call => call.path),
    respond: (next: typeof respond) => {respond = next;}, retire: () => {active = false; for (const listener of listeners) listener();}, navigation,
    dispose: () => {presenters.dispose(); common.dispose();}};
}

test('committed menu close preserves its real File info destination; dismissal and navigation retire it', async () => {
  const d = await fixture();
  try {
    assert.equal(d.presenters.getSnapshot()?.phase, 'file-info');
    const owner = d.presenters.getSnapshot()?.owner;
    assert.equal(owner?.session, d.session); assert.equal(owner?.kind, 'movie');
    assert.equal(owner?.session.getSnapshot().groups.rows?.rows[0]?.id, 'g1');
    d.presenters.close();
    d.state.held = true;
    const late = await d.open(); d.state.held = false;
    d.presenters.openFileInfo(d.capture, 'late', {session: late, kind: 'movie', changed: async () => {}});
    assert.equal(d.presenters.getSnapshot(), undefined);
    await drain(); assert.ok(d.leases().includes('/v1/file-info-queries/q-groups-2'), 'a session the presenter does not accept is retired, not leaked');
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}});
    d.navigation.back(authority);
    assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});

test('closing File info retires its session and gives back the lease it still holds', async () => {
  const d = await fixture({held: true});
  try {
    assert.deepEqual(d.leases('q-groups'), [], 'a version list with more to read keeps its window');
    d.presenters.close();
    await drain();
    assert.deepEqual(d.leases('q-groups'), ['/v1/file-info-queries/q-groups-1']);
    await d.session.groups('next');
    assert.equal(d.calls.some(call => call.path.includes('cursor=')), false, 'a retired session reads nothing more');
  } finally {d.dispose();}
});

test('composed entry actions require their exact current menu and retain disabled semantics', async () => {
  const d = await fixture(); let calls = 0;
  try {
    d.presenters.close();
    const actions = {fileInfo: async () => {}, extraActions: [
      {id: 'not-interested', label: 'Not interested', onSelect: () => calls++},
      {id: 'disabled', label: 'Unavailable', disabled: true, onSelect: () => calls++},
    ]};
    d.presenters.openMenu(d.capture, actions);
    const first = d.presenters.getSnapshot()!;
    d.presenters.extraEntry(first, 'disabled'); assert.equal(calls, 0); assert.equal(d.presenters.getSnapshot(), first);
    d.presenters.openMenu(d.capture, actions);
    d.presenters.extraEntry(first, 'not-interested'); assert.equal(calls, 0, 'a replaced menu cannot execute');
    const second = d.presenters.getSnapshot()!;
    d.presenters.extraEntry(second, 'not-interested'); assert.equal(calls, 1); assert.equal(d.presenters.getSnapshot(), undefined);
    d.presenters.extraEntry(second, 'not-interested'); assert.equal(calls, 1);
    d.presenters.openMenu(d.capture, actions); const third = d.presenters.getSnapshot()!;
    d.navigation.back(authority); d.presenters.extraEntry(third, 'not-interested'); assert.equal(calls, 1);
  } finally {d.dispose();}
});

test('a wrong occurrence, or withdrawn manage authority in the resident surface, cannot dispatch owner commands', async () => {
  const d = await fixture();
  try {
    d.presenters.manageFile(d.capture, 'delete-version', 'wrong');
    assert.equal(d.presenters.getSnapshot()?.phase, 'file-info');
    d.presenters.manageFile(d.capture, 'delete-version');
    assert.equal(d.presenters.getSnapshot()?.phase, 'file-info', 'a delete names the occurrence it was chosen for');
    d.presenters.chooseSplit(occurrence);
    assert.equal(d.presenters.getSnapshot()?.phase, 'file-info', 'split is chosen from the split list only');
    await drain();
    assert.equal(d.calls.some(call => call.method !== 'GET' && !call.path.startsWith('/v1/file-info-queries/')), false);
  } finally {d.dispose();}
  const withheld = await fixture({manage: {delete: false, split: false, merge: false}});
  try {
    for (const action of ['delete-version', 'split-versions', 'merge-title'] as const) withheld.presenters.manageFile(withheld.capture, action, occurrence);
    await drain();
    assert.equal(withheld.presenters.getSnapshot()?.phase, 'file-info');
    assert.equal(withheld.calls.some(call => call.path.includes('/delete/preview')), false);
  } finally {withheld.dispose();}
});

test('owner commands do not read File info again: the proof is the opening, and the server refuses a stale one', async () => {
  const d = await fixture();
  try {
    const reads = d.calls.length;
    d.respond(async path => path.endsWith('/delete/preview') ? new Response(JSON.stringify({code: 'stale_file_info', message: 'stale'}), {status: 409}) : fileInfoServer(path, d.state));
    d.presenters.manageFile(d.capture, 'delete-version', occurrence);
    await drain();
    assert.deepEqual(d.calls.slice(reads).map(call => [call.method, call.path, call.body]),
      [['POST', '/v1/admin/items/movie/files/asset/delete/preview', {fileInfo: {opening: 'o1', occurrence}}]]);
    assert.equal(d.presenters.getSnapshot()?.phase, 'delete');
    assert.ok(d.presenters.getSnapshot()?.error, 'the 409 is shown, nothing was deleted');
    assert.equal(d.presenters.getSnapshot()?.preview, undefined);
    assert.equal(d.calls.some(call => call.method === 'DELETE' && call.path.includes('/v1/admin/')), false);
  } finally {d.dispose();}
});

test('closing during the delete preview aborts the actual request and refuses its late answer', async () => {
  const d = await fixture(), held = deferred<unknown>();
  try {
    d.respond(async () => held.promise);
    d.presenters.manageFile(d.capture, 'delete-version', occurrence);
    const request = d.calls.at(-1)!;
    assert.ok(request.path.endsWith('/delete/preview'));
    d.presenters.close(); assert.equal(request.signal?.aborted, true);
    held.resolve(preview); await drain();
    assert.equal(d.presenters.getSnapshot(), undefined);
    assert.equal(d.calls.some(call => call.method === 'DELETE' && call.path.includes('/v1/admin/')), false);
  } finally {d.dispose();}
});

const preview = {revision: 7, confirmation: 'Real title: Edition', confirmationKind: 'title', totalBytes: 1024, totalFiles: 1, totalBytesText: '1 KB', blocked: [], assetId: 'asset', previewKey: 'a'.repeat(64),
  targets: [{itemId: 'movie', title: 'Real title: Edition', kind: 'movie', libraryId: 'library', libraryName: 'Movies', allowMediaDeletion: true, titles: 1, bytes: 1024,
    files: [{assetId: 'asset', path: '/actual/asset.mkv', bytes: 1024, present: true, shared: false}], dependents: []}]};
const deleted = {revision: 8, removed: 1, failed: 0, bytesDeleted: 1024, filesRetained: false, receipts: [{itemId: 'movie', title: 'Real title: Edition', removed: true, filesDeleted: 1, filesKept: 0, bytes: 1024, code: ''}]};

test('real version preview owns confirmation and exact physical DELETE receipt, with one operation across retry', async () => {
  const d = await fixture(); let attempt = 0;
  try {
    d.respond(async (path, method) => path.endsWith('/delete/preview') ? preview : method === 'DELETE' && ++attempt === 1
      ? {...deleted, removed: 0, failed: 1, receipts: [{...deleted.receipts[0], removed: false, filesDeleted: 0, filesKept: 1, code: 'file_not_removed'}]} : method === 'DELETE' ? deleted : fileInfoServer(path, d.state));
    d.presenters.manageFile(d.capture, 'delete-version', occurrence);
    await drain();
    assert.equal(d.presenters.getSnapshot()?.preview?.assetId, 'asset');
    const previews = d.calls.filter(call => call.path.endsWith('/delete/preview'));
    assert.deepEqual(previews.map(call => [call.method, call.path, call.body]), [['POST', '/v1/admin/items/movie/files/asset/delete/preview', {fileInfo: {opening: 'o1', occurrence}}]]);
    d.presenters.confirmation('wrong title'); await d.presenters.deleteVersion();
    assert.equal(d.calls.filter(call => call.method === 'DELETE' && call.path.includes('/v1/admin/')).length, 0);
    d.presenters.confirmation(preview.confirmation); await d.presenters.deleteVersion();
    assert.equal(d.changed(), 0); assert.ok(d.presenters.getSnapshot()?.error);
    await d.presenters.deleteVersion();
    const removes = d.calls.filter(call => call.method === 'DELETE' && call.path.includes('/v1/admin/'));
    assert.equal(d.calls.some(call => call.method === 'GET' && call.path.includes('/file-info/') && d.calls.indexOf(call) > d.calls.indexOf(previews[0]!)), false, 'no File info read between the preview and the delete');
    assert.equal(removes.length, 2);
    assert.deepEqual(removes[0]?.body, {confirmation: preview.confirmation, operationId: '11111111-1111-4111-8111-111111111111', previewKey: preview.previewKey, fileInfo: {opening: 'o1', occurrence}});
    assert.deepEqual(removes[1]?.body, removes[0]?.body);
    assert.equal(removes[1]?.path, '/v1/admin/items/movie/files/asset');
    assert.equal(d.changed(), 1); assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});

test('split names the exact occurrence of a one-file version and reads nothing before it', async () => {
  const d = await fixture();
  try {
    d.presenters.manageFile(d.capture, 'split-versions');
    assert.equal(d.presenters.getSnapshot()?.phase, 'split-pick');
    d.presenters.chooseSplit('not-a-candidate'); assert.equal(d.presenters.getSnapshot()?.phase, 'split-pick');
    d.presenters.chooseSplit(occurrence); assert.equal(d.presenters.getSnapshot()?.phase, 'split');
    d.respond(async path => path.endsWith('/file-info/separate') ? {itemId: 'new-title'} : fileInfoServer(path, d.state));
    const reads = d.calls.length;
    await d.presenters.split();
    assert.deepEqual(d.calls.slice(reads).map(call => [call.method, call.path, call.body]),
      [['POST', '/v1/admin/items/movie/file-info/separate', {fileInfo: {opening: 'o1', occurrence}}]]);
    assert.equal(d.changed(), 1); assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});

test('merge asks nothing of File info and still refreshes the title', async () => {
  const d = await fixture();
  try {
    d.presenters.manageFile(d.capture, 'merge-title');
    assert.equal(d.presenters.getSnapshot()?.phase, 'merge');
    assert.equal(d.calls.some(call => call.method === 'POST'), false);
  } finally {d.dispose();}
});

test('the real menu-to-chooser presenter keeps its sheet until start settles and aborts explicit close', async () => {
  for (const close of [false, true]) {
    const d = await fixture(), held = deferred<void>(); let selection: any;
    try {
      d.presenters.close();
      // The actual capture entry needs its genuine playback target for selection.
      const capture = {...d.capture, entry: {...d.capture.entry, playback: {itemId: 'movie'}}};
      const actual = new NativeVersionChooserController({capture, options: offered, rows: [{id: 'asset', heading: 'Actual edition', detail: ''}],
        hostCurrent: () => d.presenters.hostCurrent(capture), sourceCurrent: () => d.presenters.current(capture),
        prepare: async () => {throw new Error('disc title preparation is not configured in this chooser fixture');},
        reread: async () => {throw new Error('disc title reread is not configured in this chooser fixture');},
        preview: async () => {throw new Error('disc title preview is not configured in this chooser fixture');},
        start: async (_input, next) => {selection = next; await held.promise;}});
      d.presenters.openMenu(capture, {fileInfo: async () => {}, differentVersion: async () => {
        const versions = d.presenters.beginVersions(capture);
        if (versions) d.presenters.openVersions(capture, actual, versions);
      }});
      await d.presenters.differentVersion(d.presenters.getSnapshot()!);
      assert.equal(d.presenters.getSnapshot()?.phase, 'versions');
      const starting = d.presenters.chooseVersion(d.presenters.getSnapshot()!, 'asset');
      assert.equal(d.presenters.getSnapshot()?.phase, 'versions'); assert.equal(d.presenters.getSnapshot()?.busy, true);
      assert.equal(selection.lifetime.signal.aborted, false);
      if (close) {d.presenters.close(); assert.equal(selection.lifetime.signal.aborted, true);}
      held.resolve(); await starting; assert.equal(d.presenters.getSnapshot(), undefined);
    } finally {held.resolve(); d.dispose();}
  }
});

test('the TV entry menu owns the captured detail lifetime and its real play, personal and details commands', async () => {
  const d = await fixture(), calls: string[] = [];
  const detail = {actions: [{id: 'play', enabled: true, playback: {itemId: entry.id}}]} as any;
  const actions = {fileInfo: async () => {}, entry: {current: () => detail, subscribe: () => () => {},
    play: async () => {calls.push('play');}, mutate: async (intent: unknown) => {calls.push(JSON.stringify(intent));},
    details: () => {calls.push('details');}, dispose: () => {calls.push('dispose');}}};
  try {
    d.presenters.openMenu(d.capture, actions);
    const menu = d.presenters.getSnapshot()!;
    assert.equal(menu.phase, 'menu');
    await d.presenters.personal(menu, {action: 'watched', value: true});
    assert.deepEqual(calls, [JSON.stringify({action: 'watched', value: true})]);
    d.presenters.playEntry(d.presenters.getSnapshot()!);
    assert.equal(d.presenters.getSnapshot(), undefined);
    assert.deepEqual(calls.slice(1), ['dispose', 'play']);
    d.presenters.openMenu(d.capture, actions);
    d.presenters.showDetails(d.presenters.getSnapshot()!);
    assert.equal(d.presenters.getSnapshot(), undefined);
    assert.equal(calls.at(-1), 'details');
  } finally {d.dispose();}
});

test('the shared leaf menu hands the captured item to the playlist picker and closes', async () => {
  const d = await fixture(), calls: string[] = [];
  const actions = {fileInfo: async () => {}, entry: {current: () => undefined, subscribe: () => () => {},
    addToPlaylist: () => {calls.push('playlist');}, dispose: () => {calls.push('dispose');},
    mutate: async () => {}, play: async () => {}, details: () => {}}};
  try {
    d.presenters.openMenu(d.capture, actions);
    d.presenters.addToPlaylist(d.presenters.getSnapshot()!);
    assert.deepEqual(calls, ['dispose', 'playlist']);
    assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});
