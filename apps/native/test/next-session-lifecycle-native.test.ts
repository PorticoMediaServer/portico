import assert from 'node:assert/strict';
import test from 'node:test';
import * as core from '../../../packages/client-core/src';
import * as cleanup from '../../../packages/client-core/src/cleanup';
import * as rememberMarker from '../../../packages/client-core/src/remember-marker';
import * as serverConnections from '../../../packages/client-core/src/server-connections';
import * as timer from '../../../packages/client-core/src/timer';
import {AppSessionStore, type AppServerSession, type AppSessionRecord} from '../../../packages/client-core/src/app-sessions';
import {SessionLifecycle, type LifecyclePorts} from '../../../packages/client-core/src/session-lifecycle-runtime';
import * as identityFailure from '../src-next/platform/identity-failure';
import * as pickFailure from '../src-next/platform/pick-failure';
import * as signInEnded from '../src-next/platform/sign-in-ended';
import {screenModule} from './helpers/screen-harness.mjs';

// C2a: the native LifecyclePorts, over an in-memory envelope (a real AppSessionStore) and fakes for
// every platform dependency. Nothing here touches the device, the network or a native module.

const FUTURE = '2099-01-01T00:00:00Z', PAST = '2001-01-01T00:00:00Z';
const server = (id: string, profileId = 'p1', family = 'family-' + id): AppServerSession => ({signIn: 'local', connection: {version: '1',
  selectionId: id.padEnd(32, '_'), logicalOrigin: `https://${id}.test`, pin: {serverId: id, publicKey: 'p', fingerprint: 'fingerprint-' + id},
  routes: {paired: [`https://${id}.test`]},
  session: {accessToken: 'synthetic-access-' + id, expiresAt: FUTURE, authorizationHorizon: FUTURE, sessionFamilyId: family, tokenGeneration: '1',
    viewer: {serverId: id, accountId: 'membership-' + id, profileId, authority: 'local', role: 'owner'}}}});
const route = () => {
  const route = {disposed: 0, pin: {serverId: 'a'}, dispose() {route.disposed++;}, getSnapshot: () => ({phase: 'ready'})};
  return route;
};
const snapshot = (pinRequired = false, extra: object = {}) => ({account: {id: 'membership-a', role: 'owner', primaryProfileId: 'p1'}, rememberProfiles: true,
  profiles: [{id: 'p1', name: 'Main', primary: true, pinRequired}, {id: 'p2', name: 'Kids', primary: false, pinRequired: false}], ...extra});
const issuedSession = (profileId = 'p2', family = 'family-new') => ({...server('a', profileId, family).connection.session, accessToken: 'issued-token'});
const flush = async () => {for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve));};

type Options = {
  entries?: AppServerSession[]; selected?: string; displays?: boolean; prepare?: () => Promise<boolean>; hint?: object | undefined;
  restore?: (signal: AbortSignal) => Promise<unknown>; account?: unknown; accountError?: unknown; remember?: boolean; mismatch?: boolean;
  assertCurrent?: () => Promise<void>; marker?: AppServerSession['remembered'];
  /** Ending the replaced family never settles (R-12: it must not hold up the pick). */
  revokeNever?: boolean;
};

async function fixture(options: Options = {}) {
  const entries = options.entries ?? [server('a')];
  if (options.marker) entries[0] = {...entries[0], remembered: options.marker};
  let record: AppSessionRecord | undefined = {version: 1, revision: 1, selectedServerId: options.selected ?? entries[0]?.connection.pin.serverId, servers: entries};
  if (!entries.length) record = {version: 1, revision: 1, servers: []};
  const listeners = new Set<() => void>();
  const sessions = new AppSessionStore({read: async () => structuredClone(record), change: async update => {
    const next = update(structuredClone(record)); record = structuredClone(next); for (const listener of [...listeners]) listener();
  }});
  const log: string[] = [], notes: string[] = [];
  const apis: any[] = [];
  const makeApi = (token = 'tok') => {
    const live = route();
    const api = {baseUrl: 'https://a.test', route: live, token, requests: [] as string[], logouts: 0, tokens: [] as string[],
      getRouteConnection: () => live, useRoutes() {}, setAccessToken(value: string) {api.tokens.push(value); api.token = value;},
      async request(path: string) {
        api.requests.push(path);
        if (options.accountError && api.token === 'tok') throw options.accountError;
        return api.token === 'issued-token' ? snapshot(false, options.account as object) : options.account ?? snapshot(true);
      },
      async logout() {api.logouts++;}};
    apis.push(api); return api;
  };
  const entry = () => record!.servers.find(value => value.connection.pin.serverId === record!.selectedServerId);
  const old = () => entry()!.connection.session;
  const selectArgs: unknown[] = [], remembers: unknown[] = [], revokes: unknown[] = [], issuedLogouts: unknown[] = [], signOuts: unknown[] = [], forgets: unknown[] = [], downloads: unknown[] = [], drains: unknown[] = [], fences: string[] = [];
  const scheduler = timer.createManualScheduler(), installation = {installationId: 'install'};
  const account = {assertCurrent: options.assertCurrent ?? (async () => {}), assertSameAccount: () => {if (options.mismatch) throw Object.assign(new Error('mismatch'), {code: 'identity_mismatch'});}};
  const modules = {
    react: {default: {}},
    '../../../../packages/client-core/src': core,
    '../../../../packages/client-core/src/cleanup': cleanup,
    '../../../../packages/client-core/src/installation': {installationIdentity: () => ({installationId: 'install'})},
    '../../../../packages/client-core/src/profile-management': {parseDirectSnapshot: (raw: unknown) => raw, selectDirectProfile: async () => ({})},
    '../../../../packages/client-core/src/remember-marker': rememberMarker,
    '../../../../packages/client-core/src/server-connections': {restoreNativeSession: serverConnections.restoreNativeSession},
    '../../../../packages/client-core/src/timer': timer,
    '../../../../packages/contracts/src/routes': {routePath: (name: string) => name},
    '../../platform/offline-downloads': {offlineDownloads: {}, offlineOwnerKey: () => 'owner'},
    '../screens/accounts/sign-out-downloads': {removeCapturedDownloads: async () => 0},
    '../shell/account-binding': {createNativeShellAccountBinding: () => account, profileSessionRole: () => 'owner'},
    './accounts': {nativeAccounts: {}}, './account-authority': {hostedUrl: 'https://hosted.test'},
    './app-session-storage': {nativeAppSessionStorage: {}, nativeAppSessions: {}, readNativeSelectedViewer: async () => null, subscribeNativeAppSessions: () => () => {}},
    './error-capture': {journal: {note: () => {}}}, './identity-failure': identityFailure, './installation': {beginNativeInstallation: () => ({})},
    './pick-failure': pickFailure, './secure-records': {isSecureRecordsLost: () => false, prepareSecureRecords: async () => false, resetSecureRecords: async () => true},
    './server-connections': {nativeNetworkFetch: {}, nativeServerEnvironment: () => ({}), restoreNativeConnection: async () => undefined},
    './server-display': {readNativeServerDisplays: async () => []}, './session-revocation': {drainRevocations: async () => 0, revokeInBackground: async () => {}},
    './sign-in-ended': signInEnded, './stale-routes': {purgeStaleRoutes: (value: unknown) => value},
  };
  const {createNativeLifecyclePorts} = await screenModule(new URL('../src-next/platform/session-lifecycle-native.ts', import.meta.url), modules) as
    typeof import('../src-next/platform/session-lifecycle-native.ts');
  const hint = 'hint' in options ? options.hint : {serverId: 'a', accountId: 'membership-a', familyId: 'family-a', fingerprint: 'fingerprint-a', origin: 'https://a.test',
    serverName: 'Home', username: 'justin', signIn: 'local'};
  const ports = createNativeLifecyclePorts({
    fenceShell: () => {fences.push('fence');},
    sessions, storage: {change: async update => {record = structuredClone(update(structuredClone(record))!);}}, scheduler,
    subscribeRecords: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    prepareSecureRecords: options.prepare ?? (async () => false),
    isSecureRecordsLost: error => (error as {code?: unknown} | null)?.code === 'secure_records_key_lost', resetSecureRecords: async () => {log.push('reset'); return true;},
    readServerDisplays: (async (values: readonly AppServerSession[]) => options.displays === false ? (() => {throw new Error('no names');})()
      : values.map(value => ({serverId: value.connection.pin.serverId, name: 'Server ' + value.connection.pin.serverId}))) as any,
    readSignInHint: (async () => hint) as any,
    beginInstallation: (() => ({prepare: async () => ({saved: {viewer: {serverUrl: old().viewer.serverId === 'a' ? 'https://a.test' : '', session: old()}},
      connection: entry()!.connection, env: {}}), current: () => true, dispose: () => {log.push('installation.dispose');}})) as any,
    installationIdentity: (() => installation) as any,
    restoreConnection: (async (_origin: string, _creds: unknown, _expected: unknown, _env: unknown, signal: AbortSignal) => {
      if (options.restore) await options.restore(signal);
      return {api: makeApi(), session: old()};
    }) as any,
    restoreSession: (async () => ({api: makeApi(), session: old()})) as any,
    environment: (() => ({})) as any,
    readSelectedViewer: (async () => ({serverUrl: 'https://a.test', session: old()})) as any,
    purgeStaleRoutes: ((value: unknown) => value) as any,
    selectProfile: (async (_api: unknown, _scope: unknown, profileId: string, input: unknown) => {
      selectArgs.push({profileId, input});
      return {session: issuedSession(profileId), ...(options.remember ? {remembered: {expiresAt: FUTURE}} : {})};
    }) as any,
    logoutIssued: async (_api, issued) => {issuedLogouts.push(issued.accessToken);},
    revokeInBackground: ((record: unknown, live: unknown) => {revokes.push({record, live}); return options.revokeNever ? new Promise(() => {}) : Promise.resolve();}) as any,
    drainRevocations: (async (serverId: string) => {drains.push(serverId); return 0;}) as any,
    removeDownloads: async captured => {downloads.push(captured.connection.pin.serverId);},
    accounts: {
      onCancel: () => () => {}, admission: async () => ({signIn: 'local', expectedRevision: record!.revision}),
      remember: async (_api: unknown, issued: any, admission: any) => {
        remembers.push(issued.sessionFamilyId);
        await sessions.connectionStorage('a', admission).change(current => ({...current!, selectionId: 'n'.repeat(32), session: issued}));
      },
      switchServer: async (serverId: string) => {await sessions.select(serverId);},
      signOutServer: async (serverId: string, familyId: string) => {signOuts.push([serverId, familyId]); await sessions.signOutServer(serverId, familyId);},
      forgetServer: async (binding: any) => {forgets.push(binding); await sessions.signOutServer(binding.serverId, binding.familyId);},
      signOutPorticoAccount: async (scope: unknown) => {signOuts.push(scope);},
      accountCredentials: () => ({synchronize: async () => {}, getSnapshot: () => ({session: {account: {id: 'portico-account'}, familyId: 'portico-family'}})}),
    } as any,
    now: () => Date.parse('2026-10-31T12:00:00Z'), note: line => {notes.push(line);},
  });
  return {ports, sessions, store: () => structuredClone(record!), entry, apis, selectArgs, remembers, revokes, issuedLogouts, signOuts, forgets, downloads, drains,
    fences, log, notes, scheduler, listeners, setRecord: (value: AppSessionRecord) => {record = value;}};
}

test('readRecords: ok, reset and lost outcomes; prepare runs first, readHeld never prepares', async () => {
  const ok = await fixture();
  const read = await ok.ports.readRecords();
  assert.equal(read.outcome, 'ok'); assert.equal(read.servers, 1);
  assert.deepEqual(read.held?.server, {serverId: 'a', name: 'Server a', signIn: 'local'});
  assert.deepEqual(read.held?.viewer, {accountId: 'membership-a', profileId: 'p1', role: 'owner', familyId: 'family-a'});
  assert.equal(read.held?.remembered, false); assert.equal(read.held?.profilePinRequired, undefined);

  let prepared = 0;
  const reset = await fixture({prepare: async () => {prepared++; return true;}});
  assert.equal((await reset.ports.readRecords()).outcome, 'reset'); assert.equal(prepared, 1);
  await reset.ports.readHeld(); assert.equal(prepared, 1);

  // Only an unrelated failure rejects; a lost key is its own outcome (next test).
  const failing = await fixture({prepare: async () => {throw new Error('disk');}});
  await assert.rejects(failing.ports.readRecords(), /disk/);

  const none = await fixture({entries: []});
  assert.deepEqual(await none.ports.readRecords(), {outcome: 'ok', servers: 0});
  const unselected = await fixture({entries: [server('a'), server('b')]}); unselected.setRecord({...unselected.store(), selectedServerId: undefined});
  assert.deepEqual(await unselected.ports.readRecords(), {outcome: 'ok', servers: 2});
});

test('readRecords reports a lost secure-records key as lost, and a missing display name does not', async () => {
  const lost = await fixture({prepare: async () => {throw Object.assign(new Error('lost'), {code: 'secure_records_key_lost'});}});
  assert.deepEqual(await lost.ports.readRecords(), {outcome: 'lost', servers: 0});
  const nameless = await fixture({displays: false});
  const read = await nameless.ports.readRecords();
  assert.equal(read.outcome, 'ok'); assert.equal(read.held?.server.name, '');
});

test('remembered is true only for the exact family and profile before expiry', async () => {
  const good = {profileId: 'p1', familyId: 'family-a', expiresAt: FUTURE};
  assert.equal((await (await fixture({marker: good})).ports.readRecords()).held?.remembered, true);
  assert.equal((await (await fixture({marker: {...good, expiresAt: PAST}})).ports.readRecords()).held?.remembered, false);
  assert.equal((await (await fixture({marker: {...good, familyId: 'family-other'}})).ports.readRecords()).held?.remembered, false);
  assert.equal((await (await fixture({marker: {...good, profileId: 'p2'}})).ports.readRecords()).held?.remembered, false);
  const pin = await fixture(); await pin.sessions.setProfilePinRequired('a', 'family-a', true);
  assert.equal((await pin.ports.readHeld()).held?.profilePinRequired, true);
});

test('connect restores the held sign-in, maps the profiles, and keeps the PIN state and connection', async () => {
  const fx = await fixture();
  const facts = await fx.ports.connect('a', new AbortController().signal);
  assert.deepEqual(facts, {rememberProfiles: true, profiles: [{id: 'p1', name: 'Main', primary: true, pinRequired: true}, {id: 'p2', name: 'Kids', primary: false, pinRequired: false}]});
  assert.deepEqual(fx.apis[0].requests, ['get_direct_account']);
  assert.equal(fx.entry()!.profilePinRequired, true);
  assert.equal(fx.ports.connection('a')?.api, fx.apis[0]); assert.equal(fx.ports.connection('b'), undefined);
  fx.ports.releaseConnection();
  assert.equal(fx.apis[0].route.disposed, 1); assert.deepEqual(fx.apis[0].tokens, ['']); assert.equal(fx.ports.connection('a'), undefined);
  assert.ok(fx.log.includes('installation.dispose'));
});

test('connect: our own PIN-state write does not wake observeRecords, another owner\'s write does', async () => {
  const fx = await fixture(); let woke = 0;
  const stop = fx.ports.observeRecords(() => {woke++;});
  await fx.ports.connect('a', new AbortController().signal);
  assert.equal(woke, 0);
  await fx.sessions.select('a'); assert.equal(woke, 1);
  stop(); await fx.sessions.select('a'); assert.equal(woke, 1);
  fx.ports.releaseConnection();
});

test('classifyConnect: sign-in ended, identity codes, PIN re-entry, anything else', async () => {
  const ended = await fixture({restore: async () => {throw new serverConnections.SessionRefreshError('refresh_refused', 'Refused', true);}});
  await assert.rejects(ended.ports.connect('a', new AbortController().signal));
  assert.deepEqual(ended.ports.classifyConnect(new serverConnections.SessionRefreshError('refresh_refused', 'Refused', true)), {reason: 'signInEnded', username: 'justin'});
  // The screen that names the ending gets the lifecycle's own finding, for that server only.
  assert.equal((ended.ports.failure('a') as {hint?: {username?: string}} | undefined)?.hint?.username, 'justin');
  assert.equal(ended.ports.failure('b'), undefined);
  // A transient refresh failure before the access token expired is not an ending.
  assert.equal(ended.ports.classifyConnect(new serverConnections.SessionRefreshError('refresh_unavailable', 'Down', false)), undefined);
  assert.equal(ended.ports.failure('a'), undefined, 'a later, different outcome replaces it');
  assert.equal(ended.apis.length, 0);

  const noHint = await fixture({hint: undefined, restore: async () => {throw new Error('x');}});
  await assert.rejects(noHint.ports.connect('a', new AbortController().signal));
  assert.equal(noHint.ports.classifyConnect(new serverConnections.SessionRefreshError('refresh_refused', 'Refused', true)), undefined);

  const identity = await fixture({restore: async () => {throw Object.assign(new Error('changed'), {code: 'identity_mismatch'});}});
  await assert.rejects(identity.ports.connect('a', new AbortController().signal));
  for (const code of ['identity_mismatch', 'certificate_unverified', 'certificate_untrusted'])
    assert.deepEqual(identity.ports.classifyConnect(Object.assign(new Error(code), {code})), {reason: 'identity', code});
  assert.equal((identity.ports.failure('a') as {code?: string} | undefined)?.code, 'certificate_untrusted');
  assert.equal(identity.ports.classifyConnect(Object.assign(new Error('x'), {code: 'timeout'})), undefined);

  const gated = await fixture({accountError: new core.ApiError(403, 'profile_pin_reentry', 'Enter the PIN', false)});
  await assert.rejects(gated.ports.connect('a', new AbortController().signal), {code: 'profile_pin_reentry'});
  assert.deepEqual(gated.ports.classifyConnect(new core.ApiError(403, 'profile_pin_reentry', 'x', false)), {reason: 'pinReentry'});
  assert.equal(gated.ports.classifyConnect(new core.ApiError(403, 'forbidden', 'x', false)), undefined);
  assert.equal(gated.ports.classifyConnect(new Error('offline')), undefined);
  // A failed connection holds nothing.
  assert.equal(gated.ports.connection('a'), undefined); assert.equal(gated.apis[0].route.disposed, 1);
});

test('select without remembered: no marker, replaced family ended, nothing remembered', async () => {
  const fx = await fixture({marker: {profileId: 'p1', familyId: 'family-a', expiresAt: FUTURE}, remember: false});
  await fx.ports.connect('a', new AbortController().signal);
  const facts = await fx.ports.select({serverId: 'a', profileId: 'p2', pin: '1234', remember: false}, new AbortController().signal);
  assert.deepEqual(facts, {viewer: {accountId: 'membership-a', profileId: 'p2', role: 'owner', familyId: 'family-new'}, remembered: false});
  assert.deepEqual(fx.selectArgs, [{profileId: 'p2', input: {pin: '1234', installationId: 'install', remember: false}}]);
  assert.deepEqual(fx.remembers, ['family-new']);
  assert.equal(fx.entry()!.connection.session.sessionFamilyId, 'family-new');
  assert.equal(fx.entry()!.remembered, undefined);
  assert.equal(fx.entry()!.profilePinRequired, false);
  assert.equal(fx.revokes.length, 1); assert.equal((fx.revokes[0] as any).record.session.sessionFamilyId, 'family-a');
  assert.equal(fx.issuedLogouts.length, 0);
  // The connection the shell held was for the replaced family.
  assert.equal(fx.ports.connection('a'), undefined);
});

test('after a pick, reconnect opens the new sign-in and hands it over, so the shell opens from the lifecycle', async () => {
  const fx = await fixture();
  await fx.ports.connect('a', new AbortController().signal);
  await fx.ports.select({serverId: 'a', profileId: 'p2', remember: false}, new AbortController().signal);
  assert.equal(fx.ports.connection('a'), undefined, 'the pick released the old family\'s connection');
  const opened = await fx.ports.reconnect('a', new AbortController().signal);
  assert.equal(opened?.session.sessionFamilyId, 'family-new');
  assert.equal(opened?.selected.connection.session.sessionFamilyId, 'family-new');
  assert.equal(fx.ports.connection('a'), undefined, 'handed over: the shell owns it now');
});

test('a published pick resolves even when ending the replaced family never finishes (R-12)', async () => {
  const fx = await fixture({revokeNever: true});
  await fx.ports.connect('a', new AbortController().signal);
  const result = await Promise.race([fx.ports.select({serverId: 'a', profileId: 'p2', remember: false}, new AbortController().signal).then(() => 'resolved', () => 'rejected'),
    new Promise(resolve => setTimeout(() => resolve('blocked'), 300))]);
  assert.equal(result, 'resolved');
  assert.equal(fx.revokes.length, 1, 'the replaced family goes through the durable revocation queue');
  assert.equal(fx.issuedLogouts.length, 0, 'no direct logout on the path to Home');
});

test('select with remembered: the marker is written for the new family, with the server\'s expiry', async () => {
  const fx = await fixture({remember: true});
  await fx.ports.connect('a', new AbortController().signal);
  const facts = await fx.ports.select({serverId: 'a', profileId: 'p2', remember: true}, new AbortController().signal);
  assert.equal(facts.remembered, true);
  assert.deepEqual(fx.entry()!.remembered, {profileId: 'p2', familyId: 'family-new', expiresAt: FUTURE});
  assert.deepEqual(fx.selectArgs[0], {profileId: 'p2', input: {pin: undefined, installationId: 'install', remember: true}});
  const held = (await fx.ports.readHeld()).held!;
  assert.equal(held.remembered, true); assert.equal(held.viewer.profileId, 'p2');
  // Asked to remember but the server did not say so: no marker.
  const declined = await fixture({remember: false});
  await declined.ports.connect('a', new AbortController().signal);
  assert.equal((await declined.ports.select({serverId: 'a', profileId: 'p2', remember: true}, new AbortController().signal)).remembered, false);
  assert.equal(declined.entry()!.remembered, undefined);
});

test('select refuses an identity mismatch and ends the picked session only then', async () => {
  const mismatch = await fixture({mismatch: true});
  await mismatch.ports.connect('a', new AbortController().signal);
  await assert.rejects(mismatch.ports.select({serverId: 'a', profileId: 'p2', remember: false}, new AbortController().signal), {code: 'identity_mismatch'});
  assert.deepEqual(mismatch.issuedLogouts, ['issued-token']);
  assert.equal(mismatch.remembers.length, 0); assert.equal(mismatch.revokes.length, 0);
  assert.equal(mismatch.entry()!.connection.session.sessionFamilyId, 'family-a');
  assert.ok(mismatch.ports.connection('a'), 'a refused pick leaves the held connection alone');
  mismatch.ports.releaseConnection();

  // Any other failure after the pick leaves the issued session unpublished and unused, never signed out.
  const cancelled = await fixture({assertCurrent: (() => {let n = 0; return async () => {if (++n === 2) throw Object.assign(new Error('late'), {code: 'cancelled'});};})()});
  await cancelled.ports.connect('a', new AbortController().signal);
  await assert.rejects(cancelled.ports.select({serverId: 'a', profileId: 'p2', remember: false}, new AbortController().signal), {code: 'cancelled'});
  assert.equal(cancelled.issuedLogouts.length, 0); assert.equal(cancelled.remembers.length, 0);
  cancelled.ports.releaseConnection();

  // The held family changed under the pick.
  const moved = await fixture();
  await moved.ports.connect('a', new AbortController().signal);
  moved.setRecord({...moved.store(), servers: [server('a', 'p1', 'family-other')]});
  await assert.rejects(moved.ports.select({serverId: 'a', profileId: 'p2', remember: false}, new AbortController().signal), {code: 'cancelled'});
  assert.equal(moved.selectArgs.length, 0);
  moved.ports.releaseConnection();
});

test('clearMarker removes only the held family\'s marker', async () => {
  const fx = await fixture({marker: {profileId: 'p1', familyId: 'family-a', expiresAt: FUTURE}});
  assert.equal((await fx.ports.readHeld()).held?.remembered, true);
  await fx.ports.clearMarker('a');
  assert.equal(fx.entry()!.remembered, undefined);
  assert.equal((await fx.ports.readHeld()).held?.remembered, false);
  await fx.ports.clearMarker('missing');
});

test('signOutServer ends the family, removes downloads when asked, and returns what remains', async () => {
  const fx = await fixture({entries: [server('a'), server('b')]});
  const read = await fx.ports.signOutServer('a', 'family-a', true, new AbortController().signal);
  assert.deepEqual(fx.signOuts, [['a', 'family-a']]); assert.deepEqual(fx.downloads, ['a']);
  assert.equal(read.servers, 1); assert.equal(read.held, undefined);
  const keep = await fixture({entries: [server('a'), server('b')]});
  assert.equal((await keep.ports.signOutServer('b', 'family-b', false, new AbortController().signal)).servers, 1);
  assert.deepEqual(keep.downloads, []);
  const last = await fixture();
  assert.deepEqual(await last.ports.signOutServer('a', 'family-a', false, new AbortController().signal), {servers: 0});
});

test('signOutAccount, forgetServer, storeSelect and fenceShell', async () => {
  const fx = await fixture({entries: [server('a'), server('b')]});
  await fx.ports.storeSelect('b', new AbortController().signal);
  assert.equal(fx.store().selectedServerId, 'b');
  const account = await fx.ports.signOutAccount(new AbortController().signal);
  assert.deepEqual(fx.signOuts, [{accountId: 'portico-account', familyId: 'portico-family'}]); assert.equal(account.servers, 2);
  const forgot = await fx.ports.forgetServer('b', new AbortController().signal);
  assert.deepEqual(fx.forgets, [{serverId: 'b', familyId: 'family-b', fingerprint: 'fingerprint-b'}]); assert.equal(forgot.servers, 1);
  await assert.rejects(fx.ports.forgetServer('gone', new AbortController().signal), {code: 'cancelled'});
  fx.ports.fenceShell(); assert.deepEqual(fx.fences, ['fence']);
});

test('drainRevocations waits, never blocks, and is cancelled by releasing the connection', async () => {
  const fx = await fixture();
  await fx.ports.connect('a', new AbortController().signal);
  fx.ports.drainRevocations('a'); fx.ports.drainRevocations('other');
  await flush(); assert.deepEqual(fx.drains, []);
  await fx.scheduler.advance(9_999); assert.deepEqual(fx.drains, []);
  await fx.scheduler.advance(1); assert.deepEqual(fx.drains, ['a']);
  fx.ports.drainRevocations('a'); fx.ports.releaseConnection();
  await fx.scheduler.advance(20_000); assert.deepEqual(fx.drains, ['a']);
});

test('log writes codes only', async () => {
  const fx = await fixture();
  fx.ports.log!({t: 12, from: 'starting', event: 'launch', to: 'connecting', row: 5, generation: 2});
  assert.deepEqual(fx.notes, ['portico_session {"t":12,"from":"starting","event":"launch","to":"connecting","row":5,"generation":2}']);
});

test('a SessionLifecycle over these ports: remembered launch reaches ready, unremembered reaches choosingProfile', async () => {
  const remembered = await fixture({marker: {profileId: 'p1', familyId: 'family-a', expiresAt: FUTURE}});
  const lifecycle = new SessionLifecycle(remembered.ports as LifecyclePorts);
  lifecycle.start(); await flush();
  assert.equal(lifecycle.getSnapshot().state.kind, 'ready');
  assert.equal((lifecycle.getSnapshot().state as any).remembered, true);
  lifecycle.dispose(); remembered.ports.releaseConnection();

  const plain = await fixture();
  const picker = new SessionLifecycle(plain.ports as LifecyclePorts);
  picker.start(); await flush();
  const state = picker.getSnapshot().state as any;
  assert.equal(state.kind, 'choosingProfile'); assert.deepEqual(state.profiles.map((value: any) => value.id), ['p1', 'p2']);
  // Picking the unprotected profile goes through the port and publishes the new family.
  await picker.actions.pickProfile.start('p2'); await flush();
  assert.equal(plain.selectArgs.length, 1);
  assert.equal(picker.getSnapshot().state.kind, 'ready');
  assert.equal((picker.getSnapshot().state as any).viewer.familyId, 'family-new');
  picker.dispose(); plain.ports.releaseConnection();
});
