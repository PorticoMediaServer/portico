import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSignIn} from '@core/profile-management.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://return.example', serverId = 'srv_' + 's'.repeat(43), hostedOrigin = 'https://account.example';
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const hosted = {origin: hostedOrigin, accountId: 'hosted-account', key: {keyId: 'key', publicKey: 'k'.repeat(43)}};
const session = (family = 'returned-family'): LocalSession => ({accessToken: 'access-' + family, refreshToken: 'refresh-credential-' + family,
  sessionFamilyId: family, tokenGeneration: '1', installationId: 'i'.repeat(43), expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
  viewer: {serverId, authority: 'local', accountId: 'account-a', profileId: 'main', role: 'account'}});
const callback = {href: origin + '/auth/portico-return', fragment: '#code=kept-private&state=kept-private&transaction=kept-private'};
async function fixture() {
  let raw: AppSessionRecord | undefined, finishCalls = 0, factorCalls = 0, remembers = 0, challenge = false, cancels = 0, disposed = 0;
  let notifications = 0, returnedRevision = -1, returnedNotifications = -1;
  let supersede = false, hold: Promise<void> | undefined, release: (() => void) | undefined, restoringGate: Promise<void> | undefined, releaseRestore: (() => void) | undefined;
  const retired: string[] = [], hints = new Set<() => void>();
  const store = new AppSessionStore({read: async () => raw, change: async update => {const next = update(raw);if (next === raw) return;raw = next;notifications++;for (const fn of hints) fn();}});
  const record = (captured: LocalSession): RememberedServer => ({version: '1', logicalOrigin: origin, pin, session: captured, hosted, routes: {paired: [origin]}});
  const save = async (captured: LocalSession) => store.connectionStorage(serverId, {signIn: 'portico', hostedAccount: hosted, expectedRevision: (await store.read()).revision}).change(() => record(captured));
  const snapshot = {authority: 'local' as const, serverId, canManage: false,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'account-a', username: 'actual-user', hostedAccountId: 'hosted-account', primaryProfileId: 'main', role: 'member' as const, revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}]};
  const result = (): DirectSignIn => ({kind: 'signed-in', session: session(), snapshot, accountScoped: true, passwordChangeRequired: false});
  const system = {id: serverId, name: 'Actual server', version: '1', signIn: 'portico', move: null, setupRequired: false, hostedConfigured: true, hostedAttached: true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => ({pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {disposed++;}}) as never;
  api.request = (async path => {if (path === '/v1/system') return system;if (path === '/v1/direct') return snapshot;throw new Error('Unexpected second protocol/admission: ' + path);}) as HttpLocalApi['request'];
  const complete = async () => {await save(session());if (supersede) await save(session('successor'));returnedRevision = (await store.read()).revision;returnedNotifications = notifications;return {api, result: result()};};
  const controller = {prepare: async () => hostedOrigin + '/#canonical',
    finish: async captured => {assert.equal(captured, callback);finishCalls++;if (hold) await hold;return challenge ? {api, result: {kind: 'challenge' as const, token: 'private-factor-token', expiresAt: '2099-01-01T00:00:00Z'}} : complete();},
    completeChallenge: async (code: string) => {assert.equal(code, '123456');factorCalls++;return complete();}, cancel: async () => {cancels++;}};
  const entry = new BrowserAccountEntry({store, actions: {} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}), restore: async captured => {if (restoringGate) await restoringGate;return {api, session: captured!};},
    inspect: async () => pin, connect: (async () => ({api, connection: api.getRouteConnection()})) as never, remember: async () => {remembers++;},
    registered: () => ({controller, installationId: () => 'i'.repeat(43)}), observeSelection: fn => {hints.add(fn);return () => {hints.delete(fn);};},
    retire: async (_api, captured) => {retired.push(captured.sessionFamilyId);},
  });
  return {entry, store, save, retired, hints, get notifications() {return notifications;}, get returnedRevision() {return returnedRevision;}, get returnedNotifications() {return returnedNotifications;}, get finishCalls() {return finishCalls;}, get factorCalls() {return factorCalls;}, get remembers() {return remembers;}, get cancels() {return cancels;}, get disposed() {return disposed;},
    challenge: () => {challenge = true;}, supersede: () => {supersede = true;}, hold: () => {hold = new Promise(resolve => {release = resolve;});}, release: () => release?.(),
    holdRestore: () => {restoringGate = new Promise(resolve => {releaseRestore = resolve;});}, releaseRestore: () => releaseRestore?.()};
}
test('a genuine registered admission reloads only its exact selected family without remembering it again', async () => {
  const f = await fixture();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.entry.getSnapshot().phase, 'profiles');assert.equal(f.entry.getSnapshot().server?.name, 'Actual server');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'returned-family');assert.equal(f.finishCalls, 1);assert.equal(f.remembers, 0);
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /kept-private|access-returned|refresh-credential/);f.entry.dispose();
});
test('registered MFA uses the same private controller and never the generic factor/exchange or admission twice', async () => {
  const f = await fixture();f.challenge();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.entry.getSnapshot().phase, 'challenge');assert.equal(await f.store.selected(), undefined);
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /private-factor-token/);
  await f.entry.completeChallenge('123456');assert.equal(f.entry.getSnapshot().phase, 'profiles');
  assert.equal(f.finishCalls, 1);assert.equal(f.factorCalls, 1);assert.equal(f.remembers, 0);f.entry.dispose();
});
test('a selected successor after the registered grant is preserved while the unused family is retired', async () => {
  const f = await fixture();f.supersede();await f.entry.finishPorticoReturn(callback);
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor');assert.deepEqual(f.retired, ['returned-family']);
  assert.equal((await f.store.read()).revision, f.returnedRevision);assert.equal(f.notifications, f.returnedNotifications);
  assert.equal(f.entry.getSnapshot().phase, 'welcome');assert.equal(f.remembers, 0);f.entry.dispose();
});
test('cancelling the entry quarantines only the late admitted family without invalidating a successor', async () => {
  for (const successor of [false, true]) {
    const f = await fixture();if (successor) f.supersede();f.hold();
    const work = f.entry.finishPorticoReturn(callback);await new Promise(resolve => setImmediate(resolve));
    f.entry.cancel();assert.equal(f.cancels, 1);f.release();await work;
    assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, successor ? 'successor' : undefined);
    assert.equal((await f.store.read()).revision, f.returnedRevision + (successor ? 0 : 1));
    assert.equal(f.notifications, f.returnedNotifications + (successor ? 0 : 1));
    assert.deepEqual(f.retired, ['returned-family']);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
  }
});
test('a selection replaced during restoration never becomes the completed return profile context', async () => {
  const f = await fixture();f.holdRestore();const work = f.entry.finishPorticoReturn(callback);
  await new Promise(resolve => setImmediate(resolve));await f.save(session('successor'));f.releaseRestore();await work;
  assert.equal(f.entry.getSnapshot().snapshot, undefined);assert.equal(f.entry.getSnapshot().phase, 'profiles');assert.ok(f.entry.getSnapshot().error);
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor');assert.equal(f.remembers, 0);f.entry.dispose();
});
