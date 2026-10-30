import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from 'react-native';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src/index.ts';
import {emptyUserMutationState, type UserMutationState} from '../../../packages/client-core/src/user-mutations';
import {UserMutationRecoveryModel} from '../../../packages/client-core/src/user-mutation-recovery';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';
import {NativeRemoteSourcesController} from '../src-next/screens/settings/remote-sources/controller';
import {NativeOwnerLibrariesController} from '../src-next/screens/settings-live/owner-libraries/controller';
import {createStorageForms} from '../src-next/screens/settings-live/server/storage/forms';
import {legacyChannelRow} from '../../../packages/client-core/src/guide/legacy-source';
import type {GuideChannel} from '../../../packages/client-core/src/channel-guide';
import type {LibraryAdminDocument, LibrarySettings} from '../../../packages/client-core/src/administration';

const settings: LibrarySettings = {scanDepth: 'basic', groupCollections: true, localTags: 'prefer', lyricsSource: 'files', titleSource: 'tags', episodeNumbering: 'absolute', findExtras: true, sortIgnoreArticles: true, refreshMode: 'replace_unlocked', acoustidEnabled: false,
  continueWatching: {weeks: 16, maximumItems: 40, includeSeasonPremieres: true, videoPlayedThreshold: 95, videoCompletion: 'earliest'}, allowMediaDeletion: false, monitorChanges: true, providers: [], analysis: ['probe', 'local_metadata', 'subtitle_discovery'],
  navigation: {trickplayIntervalMs: 10000, trickplayTileWidth: 320, trickplayMaxTiles: 1080, chapterThumbnailMode: 'embedded', videoPreviewEnabled: false, videoPreviewMs: 30000}, segmentDetection: 'server'};
const document = (revision = 7): LibraryAdminDocument => ({libraryId: 'library', libraryName: 'Library', libraryKind: 'anime', revision, digest: 'document', settings, defaults: settings, eligibleControls: [], audience: {everyone: true, accounts: 1}, actions: [], analysisMatrix: {costClasses: [], operations: []}, availableProviders: [], enumerations: {}, sources: []});
function owner(api: unknown) {
  let current = true;
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'one', viewerFence: 'local'};
  const context = {api, authority: () => authority, current: () => current, subscribeAuthority: () => () => {}, account: {serverId: 'server', canManage: true, capabilities: {manageServer: true}, account: {id: 'account', role: 'owner', primaryProfileId: 'profile', disabled: false}}};
  return {context: context as any, retire: () => {current = false;}};
}

test('native library retries retain original body and ID after parse, conflict and ack failures', async () => {
  for (const failure of ['parse', 'conflict', 'ack']) {
    let fail = true, ids = 0, acknowledgements = 0;const writes: any[] = [];
    const controller = new NativeOwnerLibrariesController({
      request: async <T,>(_path: string, _method?: string, body?: unknown): Promise<T> => {
        writes.push(structuredClone(body));if (fail && failure === 'conflict') throw Object.assign(new Error('conflict'), {status: 409});
        return {...document(8), libraryId: fail && failure === 'parse' ? 'foreign' : 'library', settings: (body as any).settings} as T;
      },
      acknowledgeUserMutation: async () => {acknowledgements++;if (fail && failure === 'ack') throw new Error('ack failed');},
    }, {serverId: 'server', viewerId: 'profile'}, () => `original-${++ids}`);
    const state = controller as unknown as {state: any};state.state = {...state.state, selectedId: 'library', document: document()};
    try {
      assert.equal(await controller.save({monitorChanges: false}), false);assert.equal(acknowledgements, failure === 'ack' ? 1 : 0);
      state.state = {...state.state, document: {...document(12), settings: {...settings, allowMediaDeletion: true}}};
      assert.equal(await controller.save({monitorChanges: true}), false);assert.equal(writes.length, 1);
      fail = false;assert.equal(await controller.save({monitorChanges: false}), true);
      assert.deepEqual(writes[1], writes[0]);assert.equal(writes[1].expectedRevision, 7);assert.equal(ids, 1);
    } finally {controller.dispose();}
  }
});

test('native library selection retirement prevents acknowledgement of a late valid document', async () => {
  let finish!: (value: any) => void, acknowledgements = 0;
  const controller = new NativeOwnerLibrariesController({request: <T,>() => new Promise<T>(resolve => {finish = resolve;}), acknowledgeUserMutation: async () => {acknowledgements++;}}, {serverId: 'server', viewerId: 'profile'}, () => 'original');
  const state = controller as unknown as {state: any};state.state = {...state.state, selectedId: 'library', document: document()};
  try {const saving = controller.save({monitorChanges: false});state.state = {...state.state, selectedId: 'successor'};finish(document(8));assert.equal(await saving, false);assert.equal(acknowledgements, 0);}
  finally {controller.dispose();}
});

const channel: GuideChannel = {id: 'channel', sourceId: 'source', provenance: 'live-source', generation: 'generation', name: 'Channel', number: '1', group: '', programmes: [], tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 7};
test('both native channel wrappers await the original ack and refuse it after opening retirement', async () => {
  for (const retire of [false, true]) {
    let acknowledgements = 0, allocations = 0, failAck = true, finish!: (value: any) => void;
    const writes: any[] = [], f = owner({}), lifetime = new NativeScreenLifetime(f.context);
    const controller = new NativeLiveController(lifetime, {request: async <T,>(_path: string, _method?: string, body?: unknown): Promise<T> => {writes.push(structuredClone(body));return new Promise<T>(resolve => {finish = resolve;});}, acknowledgeUserMutation: async () => {acknowledgements++;if (failAck) throw new Error('ack failed');}}, 'UTC');
    const abort = new AbortController(), opening = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
    try {
      const first = controller.channelPreference(legacyChannelRow(channel), {favorite: true, hidden: false}, async () => {allocations++;return 'a'.repeat(48);}, opening);
      await new Promise(resolve => setImmediate(resolve));if (retire) abort.abort();finish({protocolVersion: '1.0', serverId: 'server', revision: 8});await assert.rejects(first);
      assert.equal(acknowledgements, retire ? 0 : 1);
      if (!retire) {
        failAck = false;const retry = controller.channelPreference(legacyChannelRow(channel), {favorite: true, hidden: false}, async () => {allocations++;return 'b'.repeat(48);}, opening);
        await new Promise(resolve => setImmediate(resolve));finish({protocolVersion: '1.0', serverId: 'server', revision: 8});
        assert.equal((await retry)?.favorite, true);assert.deepEqual(writes[1], writes[0]);assert.equal(allocations, 1);assert.equal(acknowledgements, 2);
      }
    } finally {controller.dispose();lifetime.dispose();}
  }
});

const profile = {sourceId: 'source', generation: '1', revision: 7, saved: true, values: {scanDepth: 'basic' as const, wholeFileReads: false, connections: 2, requestsPerSecond: 2, bytesPerSecond: 0}};
test('remote-source wrapper preserves parsed custody on ack failure and checks owner after ack', async () => {
  let writes = 0, acknowledgements = 0, failAck = true, finishAck!: () => void;
  const api = {request: async () => {writes++;return {scope: {serverId: 'server', viewerFence: 'wire'}, settings: {...profile, revision: 8}};}, acknowledgeUserMutation: async () => {acknowledgements++;if (failAck) throw new Error('ack failed');await new Promise<void>(resolve => {finishAck = resolve;});}};
  const f = owner(api), controller = new NativeRemoteSourcesController({context: f.context, requestId: () => '00000000-0000-4000-8000-000000000000'} as any);
  try {
    const values = {...profile.values, connections: 4};await assert.rejects(controller.saveProfile(profile, values, () => true));assert.equal(writes, 1);assert.equal(acknowledgements, 1);
    await assert.rejects(controller.saveProfile(profile, {...values, connections: 8}, () => true));assert.equal(writes, 1);
    failAck = false;const retry = controller.saveProfile(profile, values, () => true);await new Promise(resolve => setImmediate(resolve));assert.ok(finishAck);
    f.retire();finishAck();await assert.rejects(retry);assert.equal(writes, 1);assert.equal(acknowledgements, 2);assert.equal(controller.getSnapshot().allowed, false);
  } finally {controller.dispose();}
});

class Store {
  state = emptyUserMutationState();
  async read() {return structuredClone(this.state);}
  async compareAndSwap(revision: number, next: UserMutationState) {if (this.state.revision !== revision) return false;this.state = structuredClone(next);return true;}
}
const maintenance = (revision = 7, backgroundSpeed = 'normal') => ({revision, digest: 'a'.repeat(64), pausedUntilUnixMillis: 0, settings: {windows: [], retention: {}, backupKeepCount: 0, backgroundSpeed}, cadences: [], tasks: [], days: [], retentionMaxima: {}, storageCategories: []});
test('storage factory uses real CAS custody and releases frozen save only after root review and Keep as is', async () => {
  const store = new Store(), writes: any[] = [];let conflict = true, serial = 0;
  const originalRandom = NativeModules.PorticoRuntime.playbackRandomBytes;NativeModules.PorticoRuntime.playbackRandomBytes = (n: number) => Array.from({length: n}, () => ++serial % 256);
  const session = {accessToken: 'test-token', sessionFamilyId: 'family', deviceId: 'device', viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}} as LocalSession;
  const api = new HttpLocalApi('http://settings.test', session.accessToken, async (url, init) => {
    const path = new URL(String(url)).pathname;
    if (path === '/v1/user-mutations/policy') return Response.json({serverTimeMs: Date.now()});
    if (path.startsWith('/v1/user-mutations/')) {const entry = store.state.entries[0];return conflict || !entry ? Response.json({error: {code: 'receipt_unknown'}}, {status: 404}) : Response.json({key: entry.key, operation: entry.operation, status: 'applied', revision: 8, committedMs: Date.now(), expiresMs: Date.now() + 2592000000});}
    assert.equal(path, '/v1/admin/maintenance/settings');
    if (init?.method === 'PUT') {const body = JSON.parse(String(init.body));writes.push(body);if (conflict) return Response.json({error: {code: 'revision_mismatch'}}, {status: 409});return Response.json(maintenance(8, body.settings.backgroundSpeed));}
    return Response.json(maintenance());
  });
  api.setUserMutationSession(session);const core = api.configureUserMutations(store), f = owner(api), group = createStorageForms(f.context, ['maintenance'], () => {}), form = group.forms.maintenance;
  const model = new UserMutationRecoveryModel({api, coordinator: core, scope: api.getUserMutationScope});
  try {
    await form.load();form.set({backgroundSpeed: 'fast'});await assert.rejects(form.save());assert.equal(writes.length, 1);assert.equal(store.state.entries[0].state, 'review');const original = store.state.entries[0];
    form.set({backgroundSpeed: 'gentle'});await assert.rejects(form.save());assert.equal(writes.length, 1);
    await form.load();await assert.rejects(form.save());assert.equal(writes.length, 1);
    await assert.rejects(model.keepAsIs(original.key));await model.review(original.key);await model.keepAsIs(original.key);
    assert.equal(store.state.entries.length, 0);conflict = false;form.set({backgroundSpeed: 'gentle'});await form.save();
    assert.equal(writes.length, 2);assert.notEqual(writes[1].operationId, original.key);assert.equal(writes[1].expectedRevision, 7);assert.equal(store.state.entries.length, 0);
  } finally {model.dispose();group.dispose();core.dispose();NativeModules.PorticoRuntime.playbackRandomBytes = originalRandom;}
});

// Run each actual screen module's committed transport creation; only widgets and
// unrelated reads are shallow. The real owner controller receives the captured API.
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks} from './helpers/owner-server-settings.mjs';
for (const firstRun of [false, true]) test(`${firstRun ? 'first library' : 'owner libraries'} mount forwards custody to its original API and retires every method`, async () => {
  const host = lifecycleHooks();let captured!: any, finishReady!: () => void, hold = false;
  const calls: string[] = [], scope = {serverId: 'server', origin: 'http://settings.test', authority: 'local', accountId: 'account', profileId: 'profile', familyId: 'family'}, coordinator = {snapshot: () => []};
  const api = {
    request: async function() {assert.equal(this, api);calls.push('request');return {};},
    acknowledgeUserMutation: async function(id: string) {assert.equal(this, api);assert.equal(id, 'original');calls.push('ack');},
    awaitUserMutationsReady: async function() {assert.equal(this, api);calls.push('ready');if (hold) await new Promise<void>(resolve => {finishReady = resolve;});},
    getUserMutationScope: function() {assert.equal(this, api);calls.push('scope');return scope;},
    getUserMutationCoordinator: function() {assert.equal(this, api);calls.push('coordinator');return coordinator;},
  };
  const f = owner(api);
  class CapturedController extends NativeOwnerLibrariesController {constructor(api: any, scope: any, id: () => string) {super(api, scope, id);captured = api;}}
  const theme = {colors: {ink: 'black', text: 'white'}, type: {title: {}}};
  const fallback = new Proxy({}, {get: (_target, name) => name === 'useTheme' ? () => theme : name === 'defaultI18n' ? {t: (key: string) => key} : () => undefined});
  const modules = new Proxy({react: host.react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', BackHandler: {addEventListener: () => ({remove() {}})}}} as Record<PropertyKey, any>, {get: (target, key) => target[key] ?? fallback});
  modules[firstRun ? '../settings-live/lifetime' : '../lifetime'] = {NativeScreenLifetime, screenScopeEnded: () => Object.assign(new Error('ended'), {name: 'AbortError'})};
  modules[firstRun ? '../settings-live/owner-libraries/controller' : './controller'] = {NativeOwnerLibrariesController: CapturedController};
  const file = firstRun ? '../src-next/screens/accounts/PhoneFirstLibrary.tsx' : '../src-next/screens/settings-live/owner-libraries/OwnerLibrariesPanel.tsx';
  const screen = await screenModule(new URL(file, import.meta.url), modules);
  try {
    host.render(() => firstRun ? screen.PhoneFirstLibrary({context: f.context, onDone() {}}) : screen.NativeOwnerLibrariesPanel({ports: {context: f.context, requestId: () => 'id'}, runtime: {text: {t: (key: string) => key}}}));host.flush();
    assert.ok(captured);await captured.acknowledgeUserMutation('original');await captured.awaitUserMutationsReady();assert.equal(captured.getUserMutationScope(), scope);assert.equal(captured.getUserMutationCoordinator(), coordinator);
    // Replacing the context field cannot retarget the retained wrapper.
    f.context.api = {acknowledgeUserMutation: async () => assert.fail('successor API')};await captured.acknowledgeUserMutation('original');
    hold = true;const pending = captured.awaitUserMutationsReady();assert.ok(finishReady);host.dispose();finishReady();await assert.rejects(pending);
    const before = calls.length;await assert.rejects(captured.acknowledgeUserMutation('original'));await assert.rejects(captured.awaitUserMutationsReady());assert.throws(() => captured.getUserMutationScope());assert.throws(() => captured.getUserMutationCoordinator());assert.equal(calls.length, before);
  } finally {host.dispose();}
});
