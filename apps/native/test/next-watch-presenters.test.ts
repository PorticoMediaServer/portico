import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';
import {NativeWatchPresenters} from '../src-next/screens/watch/presenters.ts';
import {NativeScreenServices} from '../src-next/screens/screen-services.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {FileInfoSession} from '../../../packages/client-core/src/file-info-session.ts';
import {item as fileItem, locations} from './helpers/file-info-windows.ts';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const entry = {id: 'movie', kind: 'movie' as const, title: 'Real title', displayTitle: 'Real title: Edition', libraryId: 'library'};
const offered = parseOptions({itemId: 'movie', versions: [{id: 'asset', parts: [{id: 'asset', index: 0, durationMs: 60000}], durationMs: 60000, sizeBytes: 1024, video: [], audio: [], subtitles: []}]});
const occurrence = 'g1:0';
const ownerSurface = {manage: {delete: false, split: true, merge: true}};
const fileInfoServer = (path: string, _state: unknown) => path.endsWith('/files') ? locations : fileItem;
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
const drain = () => new Promise(resolve => setImmediate(resolve));
async function fixture(over: {manage?: typeof ownerSurface['manage']; held?: boolean; role?: string} = {}) {
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
  // The factory's own work: one item read and the authorized version locations.
  const open = async () => {
    const session = new FileInfoSession({api, itemId: 'movie', role: over.role ?? (state.manage.merge ? 'owner' : 'member'), isCurrent: () => active});
    await session.groups();
    if (session.getSnapshot().surface?.manage) await session.members('g1');
    return session;
  };
  const session = await open();
  const fileInfo = async () => {presenters.openFileInfo(capture, 'real content', {session, kind: 'movie', changed: async () => {changed++;}});};
  presenters.openMenu(capture, {fileInfo});
  await presenters.fileInfo(presenters.getSnapshot()!);
  return {presenters, common, capture, session, open, calls, changed: () => changed, state,
    // Arrange the future screen's opening; manageFile's production gate stays off until it draws all the impacts.
    beginDelete: () => {Reflect.get(presenters, 'patch').call(presenters, presenters.getSnapshot(), {phase: 'delete', fileId: occurrence, confirmation: '', preview: undefined}); return presenters.previewDelete();},
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
    const reads = d.calls.length; await late.groups(); assert.equal(d.calls.length, reads, 'a rejected session is retired');
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}});
    d.navigation.back(authority);
    assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});

test('closing File info retires its read without an obsolete lease request', async () => {
  const d = await fixture();
  try {d.presenters.close(); const count = d.calls.length; await d.session.groups('next'); assert.equal(d.calls.length, count); assert.deepEqual(d.leases(), []);}
  finally {d.dispose();}
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

test('native Delete stays gated until the confirm draws every preview impact', async () => {
  const d = await fixture();
  try {
    const count = d.calls.length; d.presenters.manageFile(d.capture, 'delete-version', occurrence); await drain();
    assert.equal(d.presenters.getSnapshot()?.phase, 'file-info'); assert.equal(d.calls.length, count);
  } finally {d.dispose();}
});

test('an owner reads one real version preview per opening, then confirms its title to delete that whole version', async () => {
  const d = await fixture(), preview = {title: 'Real title', titles: 2, files: 2, bytes: 2048, listEntries: 3, otherPeople: 4, alsoRemoves: [{id: 'other', title: 'Other title'}]};
  const answer = deferred<typeof preview>();
  const adminCalls = () => d.calls.filter(call => call.path.startsWith('/v1/admin/versions/'));
  d.respond(async (path, method) => path.endsWith('/deletion') ? answer.promise : method === 'DELETE' ? {filesDeleted: 2, bytes: 2048, kept: []} : fileInfoServer(path, d.state));
  try {
    const reading = d.beginDelete(); await drain(); await d.presenters.previewDelete();
    assert.equal(adminCalls().length, 1, 'an in-flight opening does not ask twice');
    answer.resolve(preview); await reading;
    assert.deepEqual(d.presenters.getSnapshot()?.preview, {versionId: 'g1', ...preview});
    await d.presenters.previewDelete(); d.presenters.confirmation('Wrong title'); await d.presenters.deleteVersion();
    assert.deepEqual(adminCalls().map(call => [call.method, call.path]), [['GET', '/v1/admin/versions/g1/deletion']]);
    const ready = d.presenters.getSnapshot()!.preview;
    d.presenters.confirmation('Real title');
    Reflect.get(d.presenters, 'patch').call(d.presenters, d.presenters.getSnapshot(), {preview: {...ready, versionId: 'g2'}});
    await d.presenters.deleteVersion(); assert.equal(adminCalls().length, 1, 'another version cannot use this confirmation');
    Reflect.get(d.presenters, 'patch').call(d.presenters, d.presenters.getSnapshot(), {preview: ready});
    d.presenters.confirmation(' Real title '); await d.presenters.deleteVersion();
    assert.deepEqual(adminCalls().map(call => [call.method, call.path]), [['GET', '/v1/admin/versions/g1/deletion'], ['DELETE', '/v1/admin/versions/g1']]);
    assert.equal(d.changed(), 1); assert.equal(d.presenters.getSnapshot(), undefined);
  } finally {d.dispose();}
});

test('a preview refusal keeps its sentence and cannot delete even with the right title', async () => {
  const d = await fixture(), deleteRefusal = 'Deleting files is turned off for this library.';
  d.respond(async path => path.endsWith('/deletion') ? {title: 'Real title', titles: 1, files: 2, bytes: 2048, listEntries: 0, otherPeople: 0, alsoRemoves: [], deleteRefusal} : fileInfoServer(path, d.state));
  try {
    await d.beginDelete(); d.presenters.confirmation('Real title'); await d.presenters.deleteVersion();
    assert.equal(d.presenters.getSnapshot()?.preview?.deleteRefusal, deleteRefusal);
    assert.equal(d.calls.some(call => call.method === 'DELETE'), false);
    await d.presenters.previewDelete();
    assert.equal(d.calls.filter(call => call.path.endsWith('/deletion')).length, 1);
    d.presenters.backToInfo();
    d.respond(async (path, method) => method === 'DELETE' ? new Response(JSON.stringify({error: {code: 'deleting_not_allowed', message: deleteRefusal, retryable: false}}), {status: 403})
      : path.endsWith('/deletion') ? {title: 'Real title', titles: 1, files: 2, bytes: 2048, listEntries: 0, otherPeople: 0, alsoRemoves: []} : fileInfoServer(path, d.state));
    await d.beginDelete(); d.presenters.confirmation('Real title'); await d.presenters.deleteVersion();
    assert.equal(d.presenters.getSnapshot()?.refusal, deleteRefusal, 'the adapter reads the HTTP refusal before the screen receives it');
  } finally {d.dispose();}
});

test('an administrator cannot ask for a version deletion preview or send its delete', async () => {
  const d = await fixture({role: 'admin'});
  try {
    await d.beginDelete();
    Reflect.get(d.presenters, 'patch').call(d.presenters, d.presenters.getSnapshot(), {confirmation: 'Real title',
      preview: {versionId: 'g1', title: 'Real title', titles: 1, files: 2, bytes: 2048, listEntries: 0, otherPeople: 0, alsoRemoves: []}});
    await d.presenters.deleteVersion();
    assert.equal(d.calls.some(call => call.path.startsWith('/v1/admin/versions/')), false);
  } finally {d.dispose();}
});

test('split names a whole version, including all its parts, and refreshes the title', async () => {
  const d = await fixture();
  try {
    d.presenters.manageFile(d.capture, 'split-versions');
    d.presenters.chooseSplit('not-a-candidate'); assert.equal(d.presenters.getSnapshot()?.phase, 'split-pick');
    d.presenters.chooseSplit('g1'); assert.equal(d.presenters.getSnapshot()?.phase, 'split');
    d.respond(async path => path.endsWith('/split') ? {itemId: 'new-title'} : fileInfoServer(path, d.state));
    const count = d.calls.length; await d.presenters.split();
    assert.deepEqual(d.calls.slice(count).map(call => [call.method, call.path, call.body]), [['POST', '/v1/admin/versions/g1/split', {}]]);
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

test('a shared title action closes the menu and runs only for its exact current opening; a stale or busy menu runs nothing', async () => {
  const d = await fixture(), calls: string[] = [];
  const entryMenu = (over: Record<string, unknown> = {}) => ({current: () => undefined, subscribe: () => () => {}, mutate: async () => {}, dispose: () => {calls.push('dispose');},
    rate: () => {calls.push('rate');}, addToCollection: () => {calls.push('collection');}, deleteTitle: () => {calls.push('delete');},
    refreshMetadata: async () => {calls.push('refresh');}, playFromStart: async () => {calls.push('start-over');}, goToSeason: () => {calls.push('season');}, ...over});
  try {
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}, entry: entryMenu()});
    const first = d.presenters.getSnapshot()!;
    d.presenters.titleAction(first, 'rate');
    assert.deepEqual(calls, ['dispose', 'rate']); assert.equal(d.presenters.getSnapshot(), undefined);
    calls.length = 0;
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}, entry: entryMenu()});
    const second = d.presenters.getSnapshot()!;
    d.presenters.titleAction(first, 'deleteTitle');
    assert.deepEqual(calls, [], 'a retained row from an earlier opening acts on nothing');
    d.presenters.startOver(second);
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}, entry: entryMenu()});
    d.presenters.goToSeason(d.presenters.getSnapshot()!);
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}, entry: entryMenu()});
    await d.presenters.titleAction(d.presenters.getSnapshot()!, 'refreshMetadata');
    assert.deepEqual((calls as string[]).filter(call => !call.startsWith('dispose')), ['start-over', 'season', 'refresh']);
    calls.length = 0;
    d.presenters.openMenu(d.capture, {fileInfo: async () => {}, entry: entryMenu({addToCollection: undefined})});
    d.presenters.titleAction(d.presenters.getSnapshot()!, 'addToCollection');
    assert.equal(d.presenters.getSnapshot()?.phase, 'menu', 'no executor, nothing happens and the menu stays');
    d.retire();
    d.presenters.titleAction(d.presenters.getSnapshot() ?? first, 'rate');
    assert.deepEqual(calls.filter(call => call === 'rate'), [], 'a retired route never runs the action');
  } finally {d.dispose();}
});
