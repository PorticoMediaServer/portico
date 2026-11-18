import test from 'node:test';
import {accountAnswer, accountPath, systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import type {CapturedPorticoReturn} from '../src/platform/portico-return-dispatch.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://owner-return.example', serverId = 'srv_' + 's'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const hosted = {origin: 'https://account.example', accountId: 'approved-owner', key: {keyId: 'key', publicKey: 'k'.repeat(43)}};
const callback = {href: origin + '/auth/portico-return', fragment: '#transaction=actual-owner-transaction'};
const normalCallback = {href: callback.href, fragment: '#code=private-code&state=private-state&transaction=srt_' + 't'.repeat(43)};
const settle = () => new Promise(resolve => setImmediate(resolve));
const session = (family: string, role = 'account'): LocalSession => ({accessToken: 'access-' + family, refreshToken: 'refresh-' + family,
  sessionFamilyId: family, tokenGeneration: '1', installationId: 'i'.repeat(43), expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
  viewer: {serverId, authority: 'local', accountId: 'owner-account', profileId: 'main', role}});

async function fixture() {
  let raw: AppSessionRecord | undefined, portico = false, pending = true, remembers = 0, ownerFinishes = 0, cancels = 0;
  let gate: Promise<void> | undefined, release: (() => void) | undefined, pendingGate: Promise<void> | undefined, releasePending: (() => void) | undefined;
  let successor = false, mismatch = false, required = false, exchangeFailure = false, capturedAttempt = false;
  let notifications = 0, returnedRevision = -1, returnedNotifications = -1;
  const events: string[] = [], retired: string[] = [], hints = new Set<() => void>();
  const store = new AppSessionStore({read: async () => raw, change: async update => {const next = update(raw);if (next === raw) return;raw = next;notifications++;for (const hint of hints) hint();}});
  const record = (value: LocalSession, signedIn: boolean): RememberedServer => ({version: '1', logicalOrigin: origin, pin, session: value,
    ...(signedIn ? {hosted} : {}), routes: {paired: [origin]}});
  const save = async (value: LocalSession, signedIn = true) => store.connectionStorage(serverId, {signIn: signedIn ? 'portico' : 'local',
    ...(signedIn ? {hostedAccount: hosted} : {}), expectedRevision: (await store.read()).revision}).change(() => record(value, signedIn));
  await save(session('original-owner', 'owner'), false);
  const snapshot = (live = false): DirectSnapshot => ({authority: 'local', serverId, canManage: false,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'owner-account', username: 'actual_owner', primaryProfileId: 'main', role: 'owner', revision: 1, disabled: false, allowedLibraries: [], ...(portico ? {hostedAccountId: hosted.accountId} : {})},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false},
      {id: 'second', name: 'Second', primary: false, position: 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
    ...(live && required ? {twoStepSetupRequired: true} : {})});
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => ({pin, getSnapshot: () => ({phase: 'ready'}), dispose() {}}) as never;
  api.request = (async path => {
    events.push('read:' + path);
    if (path === '/v1/system') return systemAnswer({id: serverId, name: 'Actual server', version: '1', signIn: portico ? 'portico' : 'local', move: null, setupRequired: false, hostedConfigured: portico, hostedAttached: portico, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
    if (accountPath(path)) return accountAnswer(path, snapshot(true));
    throw new Error('Entry must not exchange, probe me, or admit again: ' + path);
  }) as HttpLocalApi['request'];
  const complete = async () => {
    portico = true;await save(session('returned-owner', 'account'));
    const result = {kind: 'signed-in' as const, snapshot: snapshot(), session: session('returned-owner', 'account'), accountScoped: true, passwordChangeRequired: false};
    if (mismatch) result.session = {...result.session, viewer: {...result.session.viewer, profileId: 'different-profile'}};
    if (successor) await save(session('successor'));
    returnedRevision = (await store.read()).revision;returnedNotifications = notifications;
    return {api, result};
  };
  const owner = {
    async pendingReturn() {events.push('owner:pending');const original = pending;await pendingGate;if (original) capturedAttempt = true;return original ? {kind: 'owner' as const, transactionId: 'actual-owner-transaction'} : undefined;},
    async finish(captured: CapturedPorticoReturn) {
      ownerFinishes++;events.push('owner:finish');assert.equal(captured, callback);assert.equal(pending, true, 'no cancellation between KIND selection and finish');
      assert.equal(hints.size, 0, 'suspend the old selected-family observer before durable owner admission');
      pending = false;capturedAttempt = false;await gate;if (exchangeFailure) throw new Error('terminal-exchange-failure');
      return complete();
    },
    async cancel() {cancels++;events.push('owner:cancel');if (capturedAttempt) pending = false;capturedAttempt = false;},
  };
  const entry = new BrowserAccountEntry({store, actions: {} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}),
    restore: async captured => {events.push('restore');return {api, session: captured!};},
    inspect: async () => pin, connect: (async () => ({api, connection: api.getRouteConnection()})) as never,
    remember: async () => {remembers++;},
    observeSelection: hint => {hints.add(hint);return () => {hints.delete(hint);};},
    retire: async (_api, value) => {retired.push(value.sessionFamilyId);},
  });
  return {entry, store, events, retired, hints, get remembers() {return remembers;}, get ownerFinishes() {return ownerFinishes;},
    get notifications() {return notifications;}, get returnedRevision() {return returnedRevision;}, get returnedNotifications() {return returnedNotifications;}, get cancels() {return cancels;}, get pending() {return pending;},
    noOwner: () => {pending = false;}, requireFactor: () => {required = true;}, supersede: () => {successor = true;}, mismatch: () => {mismatch = true;},
    failExchange: () => {exchangeFailure = true;},
    hold: () => {gate = new Promise(resolve => {release = resolve;});}, release: () => release?.(),
    holdPending: () => {pendingGate = new Promise(resolve => {releasePending = resolve;});}, releasePending: () => releasePending?.(),
  };
}

// The old server's owner-claim journal is gone: a callback that is not this tab's own account sign-in is refused.
test('a return without a protected owner pending is refused', async () => {
  const f = await fixture();f.noOwner();await f.entry.finishPorticoReturn(normalCallback);
  assert.equal(f.ownerFinishes, 0);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal((f.entry.getSnapshot().error as {code: string}).code, 'invalid_return');assert.equal(f.remembers, 0);f.entry.dispose();
});
test('a no-code fragment cannot invent owner authority without protected owner KIND', async () => {
  const f = await fixture();f.noOwner();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.ownerFinishes, 0);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'original-owner');f.entry.dispose();
});
