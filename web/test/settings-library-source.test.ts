import test from 'node:test';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {emptyUserMutationState, type UserMutationState} from '@core/user-mutations.ts';
import {UserMutationRecoveryModel} from '@core/user-mutation-recovery.ts';
import assert from 'node:assert/strict';
import type {LibraryAdminDocument, LibrarySettings} from '@core/administration.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LibrarySonicDocument} from '@core/server-admin/library-sonic.ts';
import {SettingsLibrarySource} from '../src/screens/settings/library-source.ts';
import {SettingsController} from '../src/screens/settings/controller.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {settingsRowIndex, settingsSpec} from '@core/presentation/settings-spec.ts';

const settings: LibrarySettings = {scanDepth: 'basic', groupCollections: true, localTags: 'prefer', lyricsSource: 'files', titleSource: 'tags', episodeNumbering: 'absolute', findExtras: true, sortIgnoreArticles: true, refreshMode: 'replace_unlocked', acoustidEnabled: false,
  continueWatching: {weeks: 16, maximumItems: 40, includeSeasonPremieres: true, videoPlayedThreshold: 95, videoCompletion: 'earliest'}, allowMediaDeletion: false, monitorChanges: true, providers: [], analysis: ['probe', 'local_metadata', 'subtitle_discovery'],
  navigation: {trickplayIntervalMs: 10000, trickplayTileWidth: 320, trickplayMaxTiles: 1080, chapterThumbnailMode: 'embedded', videoPreviewEnabled: false, videoPreviewMs: 30000}, segmentDetection: 'server'};
function document(libraryId: string, revision = 7): LibraryAdminDocument {
  return {libraryId, libraryName: libraryId, libraryKind: 'anime', revision, digest: 'actual-document', eligibleControls: ['libraries.cwWeeks', 'libraries.episodeNumbering'], audience: {everyone: false, accounts: 3}, actions: ['scan', 'rename'],
    settings, defaults: {...settings, continueWatching: {...settings.continueWatching, weeks: 8}}, analysisMatrix: {costClasses: [], operations: []}, availableProviders: [], enumerations: {}, sources: []};
}
function inventory(libraryId: string, revision = 3) {
  return {scope: {serverId: 'server', viewerFence: 'fence-test'}, libraryId, revision,
    sources: [{id: 'folder', libraryId, kind: 'local', name: 'Folder', path: '/media', resolvedPath: '/media', classification: 'local', generation: 1, health: 'healthy', enabled: true, followSymlinks: false, identityConfirmed: true, intervalMs: 3600000, nextScanAt: '', lastCompleteAt: '', lastProgressAt: ''}],
    policy: {tier: 'basic', revision: 1, operations: []}};
}
function fixture(request: LibraryContentApi['request']) {
  let current = true;
  const errors: unknown[] = [];
  const source = new SettingsLibrarySource({request}, () => current, () => {}, error => {errors.push(error);});
  return {source, errors, retire: () => {current = false;}};
}

test('Library state requires explicit selection and uses only producer defaults and eligible controls', async () => {
  const calls: string[] = [];
  const f = fixture(async <T>(path: string) => {calls.push(path);return document('selected') as T;});
  try {
    assert.deepEqual(calls, []);assert.equal(f.source.document(), undefined);
    await f.source.select('selected');
    assert.deepEqual(calls, ['/v1/admin/libraries/selected/settings']);
    assert.equal(f.source.read('selected', 'libraries.cwWeeks')?.current, 16);
    assert.equal(f.source.read('selected', 'libraries.cwWeeks')?.default, 8);
    assert.equal(f.source.document('selected')?.audience.accounts, 3);
    assert.equal(f.source.read('selected', 'libraries.allowDelete'), undefined);
    assert.equal(f.source.read('unselected', 'libraries.cwWeeks'), undefined);
    await f.source.select('selected');assert.equal(calls.length, 1);
    await f.source.select();assert.equal(f.source.document('selected'), undefined);
  } finally {f.source.dispose();}
});

test('selection replacement aborts the old read and ignores a transport that returns it late', async () => {
  let release!: (value: LibraryAdminDocument) => void, oldSignal: AbortSignal | undefined;
  const f = fixture(async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    if (path.includes('/first/')) {oldSignal = signal;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
    return document('second') as T;
  });
  try {
    const first = f.source.select('first');await f.source.select('second');
    assert.equal(oldSignal?.aborted, true);release(document('first'));await first;
    assert.equal(f.source.document('second')?.libraryId, 'second');assert.equal(f.source.document('first'), undefined);
  } finally {f.source.dispose();}
});

test('Library save uses original CAS and complete canonical settings, adopts only confirmed results and resets to actual defaults', async () => {
  const writes: {expectedRevision: number; operationId: string; settings: LibrarySettings}[] = [];
  let release!: () => void, hold: Promise<void> | undefined;
  let server = document('selected');
  const f = fixture(async <T>(_path: string, method?: string, body?: unknown) => {
    if (method === 'PUT') {
      const write = body as typeof writes[number];writes.push(write);await hold;
      server = {...server, revision: server.revision + 1, settings: write.settings};
    }
    return server as T;
  });
  try {
    await f.source.select('selected');
    hold = new Promise(resolve => {release = resolve;});
    const saving = f.source.change('selected', 'libraries.cwWeeks', 20);
    assert.equal(f.source.getSnapshot().saving, true);
    assert.equal(f.source.read('selected', 'libraries.cwWeeks')?.current, 16);
    await f.source.change('selected', 'libraries.cwWeeks', 30);assert.equal(writes.length, 1);
    assert.equal(writes[0].expectedRevision, 7);assert.ok(writes[0].operationId);
    assert.equal(writes[0].settings.episodeNumbering, 'absolute');
    assert.deepEqual(writes[0].settings.navigation, settings.navigation);
    release();await saving;hold = undefined;
    assert.equal(f.source.read('selected', 'libraries.cwWeeks')?.current, 20);
    await f.source.reset('selected', 'libraries.cwWeeks');
    assert.equal(writes[1].expectedRevision, 8);assert.equal(writes[1].settings.continueWatching.weeks, 8);
    assert.equal(f.source.read('selected', 'libraries.cwWeeks')?.current, 8);
    await assert.rejects(f.source.change('selected', 'libraries.allowDelete', true));assert.equal(writes.length, 2);
  } finally {f.source.dispose();}
});

test('an ambiguous or conflicting save removes its old revision until a genuine reread; there is no automatic replay', async () => {
  let calls = 0, writes = 0;
  const failure = new Error('response lost');
  const f = fixture(async <T>(_path: string, method?: string) => {
    calls++;if (method === 'PUT') {writes++;throw failure;}return document('selected', calls > 2 ? 8 : 7) as T;
  });
  try {
    await f.source.select('selected');
    await assert.rejects(f.source.change('selected', 'libraries.cwWeeks', 20), failure);
    assert.equal(f.source.document('selected'), undefined);assert.equal(f.source.getSnapshot().error, failure);
    await assert.rejects(f.source.change('selected', 'libraries.cwWeeks', 24), {code: 'mutation_needs_review'});assert.equal(writes, 1);
    await f.source.select('selected');assert.equal(f.source.document('selected')?.revision, 8);assert.equal(writes, 1);
  } finally {f.source.dispose();}
});

test('foreign library answers and retired authority cannot publish facts or start writes', async () => {
  let calls = 0;
  const f = fixture(async <T>() => {calls++;return document('foreign') as T;});
  await f.source.select('selected');assert.equal(f.source.document('selected'), undefined);assert.equal(f.errors.length, 1);
  f.retire();await f.source.select('foreign');await f.source.change('selected', 'libraries.cwWeeks', 20);
  assert.equal(calls, 1);assert.equal(f.source.getSnapshot().document, undefined);f.source.dispose();
});

test('closing or disposing during a save aborts it without exposing a late new revision', async () => {
  for (const close of ['select', 'dispose']) {
    let release!: () => void, signal: AbortSignal | undefined;
    const f = fixture(async <T>(_path: string, method?: string, _body?: unknown, captured?: AbortSignal) => {
      if (method === 'PUT') {signal = captured;await new Promise<void>(resolve => {release = resolve;});}
      return document('selected', method === 'PUT' ? 8 : 7) as T;
    });
    try {
      await f.source.select('selected');const pending = f.source.change('selected', 'libraries.cwWeeks', 20);
      if (close === 'select') await f.source.select();else f.source.dispose();
      assert.equal(signal?.aborted, true);release();assert.equal(await pending, false);
      assert.equal(f.source.document('selected'), undefined);assert.deepEqual(f.errors, []);
    } finally {f.source.dispose();}
  }
});

test('the existing Settings panel loads only its actual record and dispatches changes through the Library facade', async () => {
  const calls: string[] = [], section = settingsSpec.find(section => section.scope === 'server' && section.id === 'libraries')!;
  let server = document('selected');
  const api: LibraryContentApi = {request: async <T>(path: string, method?: string, body?: unknown) => {
    calls.push(path);
    if (path.endsWith('/inventory-config')) return inventory('selected') as T;
    assert.equal(path, '/v1/admin/libraries/selected/settings');
    if (method === 'PUT') server = {...server, revision: server.revision + 1, settings: (body as {settings: LibrarySettings}).settings};
    return server as T;
  }};
  const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
  const reader = new PreferencesReader(api, {serverId: 'server', viewerId: 'viewer'});
  const controller = new SettingsController({api, serverId: 'server', viewerId: 'viewer', role: 'admin', mainProfile: true,
    capabilities: {manageServer: true, managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => true, reportError: error => {throw error;},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => false, library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  const selected = {section, panelId: 'library', recordId: 'selected'}, row = settingsRowIndex.get('libraries.cwWeeks')!.row;
  try {
    assert.deepEqual(calls, []);
    assert.equal(controller.rowState(row, selected).present, false);
    await controller.loadContext({section, panelId: 'library'});assert.deepEqual(calls, []);
    await controller.loadContext(selected);
    assert.equal(controller.rowState(row, selected).value, 16);assert.equal(controller.rowState(row, selected).defaultValue, 8);
    controller.change(row, 24, selected);await new Promise(resolve => setImmediate(resolve));
    assert.equal(controller.rowState(row, selected).value, 24);
    await controller.loadContext({section});assert.equal(controller.rowState(row, selected).present, false);
    controller.change(row, 32, selected);assert.equal(calls.length, 3);
  } finally {controller.dispose();reader.dispose();}
});

const sonic: LibrarySonicDocument = {libraryId: 'selected', available: true, defaultEnabled: true, enabled: false, revision: 3, policyAllows: false, state: 'disabled', done: 2, total: 4, policyRevision: 5, observedAtMs: 1000};
test('Sonic requires the producer hint and actual readiness, and reads the canonical factory default', async () => {
  for (const offered of [false, true]) for (const available of [false, true]) {
    const calls: string[] = [];
    const f = fixture(async <T>(path: string) => {
      calls.push(path);
      return (path.endsWith('/sonic-analysis') ? {...sonic, available} : {...document('selected'), libraryKind: 'music', eligibleControls: offered ? ['libraries.sonicAnalysis'] : []}) as T;
    });
    try {
      await f.source.select('selected');
      assert.equal(calls.filter(path => path.endsWith('/sonic-analysis')).length, offered ? 1 : 0);
      const value = f.source.read('selected', 'libraries.sonicAnalysis');
      assert.equal(!!value, offered && available);assert.equal(value?.default, offered && available ? sonic.defaultEnabled : undefined);
      if (!offered || !available) {
        assert.equal(await f.source.reset('selected', 'libraries.sonicAnalysis'), false);
        assert.equal(await f.source.change('selected', 'libraries.sonicAnalysis', true), false);
      }
      assert.equal(calls.length, offered ? 2 : 1);
    } finally {f.source.dispose();}
  }
});

test('Sonic Reset follows either actual factory choice at the Sonic revision and status excludes model text', async () => {
  for (const defaultEnabled of [true, false]) {
    let saved: unknown;
    const f = fixture(async <T>(path: string, method?: string, body?: unknown) => {
      if (path.endsWith('/settings')) return {...document('selected'), libraryKind: 'music', eligibleControls: ['libraries.sonicAnalysis', 'libraries.sonicStatus']} as T;
      if (method === 'PUT') saved = body;
      return {...sonic, defaultEnabled, enabled: method === 'PUT' ? defaultEnabled : !defaultEnabled, revision: method === 'PUT' ? 4 : 3, model: {id: 'runtime-model', version: 'v1', sha256: 'a'.repeat(64)}} as T;
    });
    try {
      await f.source.select('selected');assert.equal(await f.source.reset('selected', 'libraries.sonicAnalysis'), true);
      assert.deepEqual(saved, {enabled: defaultEnabled, expectedRevision: 3});assert.equal(f.source.document('selected')?.revision, 7);
      assert.deepEqual(f.source.sonicStatus('selected'), {state: 'disabled', done: 2, total: 4, observedAtMs: 1000});
      assert.equal(f.source.read('selected', 'libraries.sonicStatus'), undefined);
      await assert.rejects(f.source.change('selected', 'libraries.sonicStatus', true));
    } finally {f.source.dispose();}
  }
});

test('a missing required Sonic default is refused without a guessed policy or writable control', async () => {
  const f = fixture(async <T>(path: string) => (path.endsWith('/sonic-analysis') ? {...sonic, defaultEnabled: undefined} : {...document('selected'), libraryKind: 'music', eligibleControls: ['libraries.sonicAnalysis']}) as T);
  try {
    await f.source.select('selected');
    assert.equal(f.errors.length, 1);assert.equal(f.source.document('selected'), undefined);
    assert.equal(f.source.read('selected', 'libraries.sonicAnalysis'), undefined);
    assert.equal(await f.source.reset('selected', 'libraries.sonicAnalysis'), false);
  } finally {f.source.dispose();}
});

test('Sonic uses its own CAS and probe facade while leaving Library settings and mode untouched', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  let state = sonic;
  const f = fixture(async <T>(path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});
    if (path.endsWith('/settings')) return {...document('selected'), libraryKind: 'music', eligibleControls: ['libraries.sonicAnalysis']} as T;
    if (method === 'PUT') {assert.deepEqual(body, {enabled: true, expectedRevision: 3});state = {...sonic, enabled: true, revision: 4};}
    if (path.endsWith('/retry')) assert.deepEqual(body, {});
    return state as T;
  });
  try {
    await f.source.select('selected');assert.equal(await f.source.change('selected', 'libraries.sonicAnalysis', true), true);
    assert.equal(f.source.read('selected', 'libraries.sonicAnalysis')?.current, true);
    assert.equal(f.source.document('selected')?.revision, 7);assert.deepEqual(f.source.document('selected')?.settings, settings);
    assert.equal(await f.source.retrySonic('selected'), true);
    assert.deepEqual(calls.map(call => [call.path, call.method]), [
      ['/v1/admin/libraries/selected/settings', 'GET'], ['/v1/admin/libraries/selected/sonic-analysis', 'GET'],
      ['/v1/admin/libraries/selected/sonic-analysis', 'PUT'], ['/v1/admin/libraries/selected/sonic-analysis/retry', 'POST'],
    ]);
  } finally {f.source.dispose();}
});

test('Sonic selection retirement suppresses a late answer and removal of its producer hint clears cached facts', async () => {
  let release!: () => void, signal: AbortSignal | undefined, offered = true;
  const f = fixture(async <T>(path: string, method?: string, body?: unknown, captured?: AbortSignal) => {
    if (path.endsWith('/sonic-analysis')) {
      if (method === 'PUT') {signal = captured;await new Promise<void>(resolve => {release = resolve;});return {...sonic, enabled: true, revision: 4} as T;}
      return sonic as T;
    }
    if (method === 'PUT') offered = false;
    return {...document('selected', method === 'PUT' ? 8 : 7), libraryKind: 'music', eligibleControls: ['libraries.cwWeeks', ...(offered ? ['libraries.sonicAnalysis'] : [])], settings: method === 'PUT' ? (body as {settings: LibrarySettings}).settings : settings} as T;
  });
  try {
    await f.source.select('selected');
    await f.source.change('selected', 'libraries.cwWeeks', 24);
    assert.equal(f.source.getSnapshot().sonic, undefined);assert.equal(f.source.read('selected', 'libraries.sonicAnalysis'), undefined);
    await f.source.select();offered = true;await f.source.select('selected');
    const pending = f.source.change('selected', 'libraries.sonicAnalysis', true);await f.source.select();
    assert.equal(signal?.aborted, true);release();assert.equal(await pending, false);assert.equal(f.source.getSnapshot().sonic, undefined);
  } finally {f.source.dispose();}
});

test('library parse and ack failures retain the original command across reread and draft changes', async () => {
  for (const failure of ['parse', 'ack', 'conflict'] as const) {
    const writes: any[] = [], acknowledgements: string[] = [];
    let fail = true, revision = 7;
    const api: LibraryContentApi = {
      request: async <T,>(_path: string, method?: string, body?: unknown): Promise<T> => {
        if (method !== 'PUT') return document('selected', revision) as T;
        writes.push(structuredClone(body));
        if (fail && failure === 'conflict') throw Object.assign(new Error('conflict'), {status: 409});
        if (fail && failure === 'parse') return document('foreign', 8) as T;
        return {...document('selected', 8), settings: (body as any).settings} as T;
      },
      acknowledgeUserMutation: async id => {acknowledgements.push(id);if (fail && failure === 'ack') throw new Error('ack storage failed');},
    };
    const source = new SettingsLibrarySource(api, () => true, () => {}, () => {});
    try {
      await source.select('selected');
      await assert.rejects(source.change('selected', 'libraries.cwWeeks', 20));
      assert.equal(acknowledgements.length, failure === 'ack' ? 1 : 0);
      revision = 11;await source.select('selected');
      await assert.rejects(source.change('selected', 'libraries.cwWeeks', 24), {code: 'mutation_needs_review'});
      assert.equal(writes.length, 1);
      // A different draft must not release the original request, even after a read.
      await source.select('selected');fail = false;
      assert.equal(await source.change('selected', 'libraries.cwWeeks', 20), true);
      assert.deepEqual(writes[1], writes[0]);assert.equal(writes[1].expectedRevision, 7);
      assert.equal(acknowledgements.at(-1), writes[0].operationId);
    } finally {source.dispose();}
  }
});

test('library wrapper never acknowledges a parsed answer after its opening closes', async () => {
  let finish!: (value: LibraryAdminDocument) => void, acknowledgements = 0;
  const source = new SettingsLibrarySource({
    request: async <T,>(_path: string, method?: string): Promise<T> => method === 'PUT' ? new Promise(resolve => {finish = resolve;}) as Promise<T> : document('selected') as T,
    acknowledgeUserMutation: async () => {acknowledgements++;},
  }, () => true, () => {}, () => {});
  try {
    await source.select('selected');const saving = source.change('selected', 'libraries.cwWeeks', 20);
    await source.select();finish(document('selected', 8));assert.equal(await saving, false);
    assert.equal(acknowledgements, 0);
  } finally {source.dispose();}
});

test('library frozen command releases only after real CAS root review and Keep as is', async () => {
  let state = emptyUserMutationState(), conflict = true;const writes: any[] = [];
  const store = {read: async () => structuredClone(state), compareAndSwap: async (revision: number, next: UserMutationState) => {if (revision !== state.revision) return false;state = structuredClone(next);return true;}};
  const session = {accessToken: 'test-token', sessionFamilyId: 'family', deviceId: 'device', viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}} as LocalSession;
  const api = new HttpLocalApi('http://settings.test', session.accessToken, async (url, init) => {
    const path = new URL(String(url)).pathname;
    if (path === '/v1/user-mutations/policy') return Response.json({serverTimeMs: Date.now()});
    if (path.startsWith('/v1/user-mutations/')) {const entry = state.entries[0];return conflict || !entry ? Response.json({error: {code: 'receipt_unknown'}}, {status: 404}) : Response.json({key: entry.key, operation: entry.operation, status: 'applied', targetId: 'selected', revision: 8, committedMs: Date.now(), expiresMs: Date.now() + 2592000000});}
    assert.equal(path, '/v1/admin/libraries/selected/settings');
    if (init?.method === 'PUT') {const body = JSON.parse(String(init.body));writes.push(body);if (conflict) return Response.json({error: {code: 'revision_mismatch'}}, {status: 409});return Response.json({...document('selected', 8), settings: body.settings});}
    return Response.json(document('selected'));
  });
  api.setUserMutationSession(session);const core = api.configureUserMutations(store), source = new SettingsLibrarySource(api, () => true, () => {}, () => {}), model = new UserMutationRecoveryModel({api, coordinator: core, scope: api.getUserMutationScope});
  try {
    await source.select('selected');await assert.rejects(source.change('selected', 'libraries.cwWeeks', 20));assert.equal(writes.length, 1);
    const original = state.entries[0];assert.equal(original.state, 'review');await source.select('selected');
    await assert.rejects(source.change('selected', 'libraries.cwWeeks', 24));assert.equal(writes.length, 1);
    await assert.rejects(model.keepAsIs(original.key));await model.review(original.key);await model.keepAsIs(original.key);
    assert.equal(state.entries.length, 0);conflict = false;await source.select('selected');
    assert.equal(await source.change('selected', 'libraries.cwWeeks', 24), true);assert.equal(writes.length, 2);
    assert.notEqual(writes[1].operationId, writes[0].operationId);assert.equal(state.entries.length, 0);
  } finally {model.dispose();source.dispose();core.dispose();}
});

test('library readiness cannot release custody or read scope after opening retirement', async () => {
  let active = true, hold = false, finish!: () => void, getters = 0, writes = 0;
  const scope = {serverId: 'server', origin: 'http://settings.test', authority: 'local', accountId: 'account', profileId: 'profile', familyId: 'family'};
  const api: LibraryContentApi = {
    request: async <T,>(_path: string, method?: string): Promise<T> => {if (method === 'PUT') {writes++;throw new Error('lost response');}return document('selected') as T;},
    awaitUserMutationsReady: async () => {if (hold) await new Promise<void>(resolve => {finish = resolve;});},
    getUserMutationScope: () => {getters++;return scope;},
    getUserMutationCoordinator: () => {getters++;return {snapshot: () => []} as any;},
  };
  const source = new SettingsLibrarySource(api, () => active, () => {}, () => {});
  try {
    await source.select('selected');await assert.rejects(source.change('selected', 'libraries.cwWeeks', 20));await source.select('selected');
    hold = true;const before = getters, retry = source.change('selected', 'libraries.cwWeeks', 24);active = false;finish();
    assert.equal(await retry, false);assert.equal(getters, before);assert.equal(writes, 1);
  } finally {source.dispose();}
});

test('quiet selected library settings bypass selection cache and preserve unrelated source state',async()=>{
 let revision=7,gets=0;const f=fixture(async<T>()=>{gets++;return document('selected',revision) as T;});
 try{await f.source.select('selected');await f.source.select('selected');assert.equal(gets,1);revision=8;const before=f.source.getSnapshot();assert.deepEqual(await f.source.refreshSettings(),{kind:'adopted'});assert.equal(gets,2);assert.equal(f.source.getSnapshot().document?.revision,8);assert.equal(f.source.getSnapshot().libraryId,before.libraryId);assert.equal(f.source.getSnapshot().error,before.error);assert.equal(f.source.getSnapshot().saving,false);}finally{f.source.dispose();}
});

test('selected library quiet reads cannot overwrite a write that starts and finishes during I/O',async()=>{
 let held=false,release!:(value:LibraryAdminDocument)=>void;const f=fixture(async<T>(_path:string,method?:string,body?:any)=>{if(method==='PUT')return {...document('selected',8),settings:body.settings} as T;if(held)return new Promise(resolve=>{release=resolve;}) as Promise<T>;return document('selected') as T;});
 try{await f.source.select('selected');held=true;const pending=f.source.refreshSettings();await f.source.change('selected','libraries.cwWeeks',20);const before=f.source.getSnapshot();release(document('selected',99));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(f.source.getSnapshot(),before);assert.equal(f.source.getSnapshot().document?.revision,8);}finally{f.source.dispose();}
});
