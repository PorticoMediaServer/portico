import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://authority.example', serverId = 'srv_' + 's'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
function session(family = 'restricted-family'): LocalSession {
  return {accessToken: 'access-' + family, refreshToken: 'refresh-credential-' + family, sessionFamilyId: family, tokenGeneration: '1',
    installationId: 'i'.repeat(43), deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
    viewer: {serverId, authority: 'local', accountId: 'account-a', profileId: 'main', role: 'account'}};
}
const record = (value: LocalSession): RememberedServer => ({version: '1', logicalOrigin: origin, pin, session: value, routes: {paired: [origin]}});
const settle = () => new Promise(resolve => setImmediate(resolve));
async function fixture(required = true, recovery = false) {
  let profileRevision = 1, pinRevision = 1, directGate: Promise<void> | undefined, releaseDirect: (() => void) | undefined, directStarted = false;
  let raw: AppSessionRecord | undefined, disconnected = 0, failRead = false;
  let enrolGate: Promise<void> | undefined, releaseEnrol: (() => void) | undefined, enrolStarted = false;
  const hints = new Set<() => void>(), tokens: string[] = [], calls: string[] = [], selections: unknown[][] = [];
  const hint = () => {for (const changed of hints) changed();};
  const store = new AppSessionStore({read: async () => raw, change: async update => {raw = update(raw);hint();}});
  const initial = session(), system = {id: serverId, name: 'Actual server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const snapshot = () => ({authority: 'local', serverId, canManage: recovery, twoStepSetupRequired: required,
    capabilities: {manageAccount: recovery, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'account-a', username: 'actual-user', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: profileRevision, pinRevision, pinRequired: recovery}]});
  const connection = {pin, dispose() {disconnected++;}, getSnapshot: () => ({phase: 'ready'})};
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => connection as never;api.setAccessToken = token => {tokens.push(token);};
  api.request = (async path => {
    calls.push(path);
    if (path === '/v1/system') return system;
    if (path === '/v1/direct') {directStarted = true; await directGate; return snapshot();}
    if (path === '/v1/direct/profiles/main/pin-reset') {profileRevision++;pinRevision++;return undefined;}
    if (path === '/v1/direct/two-factor/enrol') {enrolStarted = true;await enrolGate;return {secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Actual?secret=ABCDEFGHIJKLMNOP', recoveryCodes: ['ephemeral-code']};}
    throw new Error('Unexpected fixture route');
  }) as HttpLocalApi['request'];
  const save = async (value: LocalSession) => store.connectionStorage(serverId, {signIn: 'local', expectedRevision: (await store.read()).revision}).change(() => record(value));
  await save(initial);
  const selected = store.selected.bind(store);
  store.selected = async () => {if (failRead) throw new Error('fixture-read-failure');return selected();};
  const chosen = {...session('viewer-family'), viewer: {...initial.viewer, role: 'member'}};
  const entry = new BrowserAccountEntry({store, actions: {switchProfile: async (...args: unknown[]) => {selections.push(args);await save(chosen);return chosen;}} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}), restore: async captured => ({api, session: captured!}),
    inspect: async () => pin, connect: (async () => ({api, connection})) as never, remember: async () => {},
    observeSelection: changed => {hints.add(changed);return () => {hints.delete(changed);};},
  });
  await entry.loadProfiles();await settle();
  return {entry, store, initial, chosen, save, hint, hints, tokens, calls, selections,
    holdDirect: () => {directStarted = false;directGate = new Promise(resolve => {releaseDirect = resolve;});}, releaseDirect: () => releaseDirect?.(), get directStarted() {return directStarted;}, get disconnected() {return disconnected;},
    failRead: () => {failRead = true;},
    holdEnrol: () => {enrolGate = new Promise(resolve => {releaseEnrol = resolve;});}, releaseEnrol: () => releaseEnrol?.(), get enrolStarted() {return enrolStarted;}};
}
test('idle family replacement clears displayed secrets and the captured route without touching the successor', async () => {
  const f = await fixture();let displayedSecret: string | undefined = 'displayed-key', retired = 0;
  f.entry.subscribeAuthorityRetired(() => {displayedSecret = undefined;retired++;});
  await f.save(session('successor'));await settle();
  assert.equal(displayedSecret, undefined);assert.equal(retired, 1);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor');assert.equal(f.disconnected, 1);assert.equal(f.hints.size, 0);f.entry.dispose();
});
test('same-scope token rotation updates the captured bearer without retiring an enrollment view', async () => {
  const f = await fixture();let retired = 0;f.entry.subscribeAuthorityRetired(() => {retired++;});
  await f.save({...f.initial, accessToken: 'rotated-access', tokenGeneration: '2'});await settle();f.hint();await settle();
  assert.equal(retired, 0);assert.equal(f.tokens.at(-1), 'rotated-access');assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');
  assert.equal(f.entry.getSnapshot().origin, origin);assert.deepEqual(f.entry.getSnapshot().pin, pin);f.entry.dispose();assert.equal(retired, 1);assert.equal(f.hints.size, 0);
});
test('signout and unreadable private selection both retire an idle setup lifetime', async () => {
  for (const failure of [false, true]) {
    const f = await fixture();let retired = 0;f.entry.subscribeAuthorityRetired(() => {retired++;});
    if (failure) {f.failRead();f.hint();} else await f.store.signOutServer(serverId, f.initial.sessionFamilyId);
    await settle();assert.equal(retired, 1);assert.equal(f.entry.getSnapshot().phase, 'welcome');assert.equal(f.hints.size, 0);f.entry.dispose();
  }
});
test('an idle authority change prevents a late enrollment from returning its secret', async () => {
  const f = await fixture();f.holdEnrol();const enrolling = f.entry.beginRequiredTwoStep('fixture-input');
  await settle();assert.equal(f.enrolStarted, true);
  await f.save(session('successor'));await settle();f.releaseEnrol();assert.equal(await enrolling, undefined);
  assert.equal(f.entry.getSnapshot().phase, 'welcome');assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /ABCDEFGHIJKLMNOP|ephemeral-code|fixture-input/);f.entry.dispose();
});
test('own profile admission ignores its own committed hint and starts the new viewer lifetime', async () => {
  const f = await fixture(false);let retired = 0;f.entry.subscribeAuthorityRetired(() => {retired++;});
  await f.entry.chooseProfile('main', '1234', true);await settle();
  assert.equal(f.entry.getSnapshot().phase, 'ready');assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, f.chosen.sessionFamilyId);
  assert.equal(f.selections[0][2], 'main');assert.equal(f.selections[0][3], '1234');assert.equal(f.selections[0][6], true);
  assert.equal(retired, 1);assert.equal(f.hints.size, 1);f.entry.dispose();assert.equal(retired, 2);
});
test('a failing view subscriber cannot prevent other secret owners from clearing', async () => {
  const f = await fixture();let retired = 0;f.entry.subscribeAuthorityRetired(() => {throw new Error('fixture-view-error');});
  f.entry.subscribeAuthorityRetired(() => {retired++;});f.entry.subscribe(() => {throw new Error('fixture-render-error');});
  await f.save(session('successor'));await settle();assert.equal(retired, 1);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
});
test('retiring while an old selection read is delayed cannot cancel a newly captured lifetime', async () => {
  const f = await fixture();const selected = f.store.selected.bind(f.store), old = await selected();
  let release!: (value: typeof old) => void;
  f.store.selected = () => new Promise(resolve => {release = resolve;});f.hint();
  f.entry.cancel();f.store.selected = selected;await f.entry.loadProfiles();await settle();
  release(undefined);await settle();assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');assert.equal(f.hints.size, 1);f.entry.dispose();
});
test('profile recovery uses only a current eligible picker and retires with its captured entry', async () => {
  const required = await fixture();assert.throws(() => required.entry.profileRecovery('main'));required.entry.dispose();
  const f = await fixture(false);assert.throws(() => f.entry.profileRecovery('missing-profile'));
  const recovery = f.entry.profileRecovery('main');assert.equal(recovery.getSnapshot().phase, 'password');
  const before = f.calls.length;await f.save(session('successor'));await settle();
  await recovery.password('fixture-input');assert.equal(f.calls.length, before);assert.equal(f.entry.getSnapshot().phase, 'welcome');recovery.dispose();f.entry.dispose();
});

test('recovered profile opens only through its original committed picker and main family', async () => {
  const f = await fixture(false, true), flow = f.entry.profileRecovery('main');
  await flow.reset('2468');assert.equal(flow.getSnapshot().phase,'done');
  await flow.openProfile('2468',()=>true);
  assert.equal(f.selections.length,1);assert.deepEqual(f.selections[0][1],f.initial);assert.equal(f.selections[0][2],'main');assert.equal(f.selections[0][6],false);
  flow.dispose();f.entry.dispose();
});
test('retired recovery view during the profile read cannot dispatch a successor choice', async () => {
  const f = await fixture(false, true);let current = true;
  const flow = f.entry.profileRecovery('main',()=>current);await flow.reset('2468');
  f.holdDirect();const pending=flow.openProfile('2468',()=>current);await settle();assert.equal(f.directStarted,true);
  current=false;f.releaseDirect();await pending;
  assert.equal(f.selections.length,0);assert.equal(f.entry.getSnapshot().busy,false);flow.dispose();f.entry.dispose();
});
test('same-server account-family replacement during recovered-profile read refuses before selection', async () => {
  const f = await fixture(false, true), flow=f.entry.profileRecovery('main');await flow.reset('2468');
  f.holdDirect();const pending=flow.openProfile('2468',()=>true);await settle();assert.equal(f.directStarted,true);
  await f.save(session('successor'));f.releaseDirect();await pending;
  assert.equal(f.selections.length,0);assert.equal((await f.store.selected())?.connection.session.sessionFamilyId,'successor');
  flow.dispose();f.entry.dispose();
});


test('recovered profile captures the rotated bearer only within its original private admission', async () => {
  const f=await fixture(false,true),flow=f.entry.profileRecovery('main');await flow.reset('2468');
  const rotated={...f.initial,accessToken:'rotated-access',tokenGeneration:'2'};
  await f.save(rotated);await settle();f.hint();await settle();
  await flow.openProfile('2468',()=>true);
  assert.equal(f.selections.length,1);assert.deepEqual(f.selections[0][1],rotated);
  flow.dispose();f.entry.dispose();
});
test('recovery refuses a same-family replacement authorization horizon', async () => {
  const f=await fixture(false,true),flow=f.entry.profileRecovery('main');await flow.reset('2468');
  await f.save({...f.initial,authorizationHorizon:'2099-03-01T00:00:00Z',tokenGeneration:'2'});await settle();f.hint();await settle();
  await flow.openProfile('2468',()=>true);assert.equal(f.selections.length,0);
  flow.dispose();f.entry.dispose();
});
