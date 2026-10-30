import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import {dispatchPorticoReturn, type CapturedPorticoReturn} from '../src/platform/portico-return-dispatch.ts';
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

async function fixture(kind:'owner'|'setup'|'ambiguous'='owner', admittedSetupOwner=false) {
  let raw: AppSessionRecord | undefined, portico = false, pending = true, remembers = 0, ownerFinishes = 0, registeredFinishes = 0, cancels = 0;
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
      ...(!admittedSetupOwner ? [{id: 'second', name: 'Second', primary: false, position: 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}] : [])],
    ...(live && required ? {twoStepSetupRequired: true} : {})});
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => ({pin, getSnapshot: () => ({phase: 'ready'}), dispose() {}}) as never;
  api.request = (async path => {
    events.push('read:' + path);
    if (path === '/v1/system') return {id: serverId, name: 'Actual server', version: '1', signIn: portico ? 'portico' : 'local', move: null, setupRequired: false, hostedConfigured: portico, hostedAttached: portico, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
    if (path === '/v1/direct') return snapshot(true);
    throw new Error('Entry must not exchange, probe me, or admit again: ' + path);
  }) as HttpLocalApi['request'];
  const complete = async () => {
    portico = true;await save(session('returned-owner', admittedSetupOwner ? 'owner' : 'account'));
    const result = {kind: 'signed-in' as const, snapshot: snapshot(), session: session('returned-owner', admittedSetupOwner ? 'owner' : 'account'), accountScoped: !admittedSetupOwner, passwordChangeRequired: false};
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
  const registered = {prepare: async () => '', completeChallenge: async () => {throw new Error('owner return cannot enter generic MFA');}, cancel: async () => {},
    async finish(captured: CapturedPorticoReturn) {registeredFinishes++;dispatchPorticoReturn(captured, {kind: 'registered'});return complete();}};
  const entry = new BrowserAccountEntry({store, actions: {} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}),
    restore: async captured => {events.push('restore');return {api, session: captured!};},
    inspect: async () => pin, connect: (async () => ({api, connection: api.getRouteConnection()})) as never,
    remember: async () => {remembers++;}, ownerReturn: kind==='setup'?undefined:()=>owner,
    setupReturn: kind==='setup'?()=>({...owner,pendingReturn:async()=>{const p=await owner.pendingReturn();return p?{...p,kind:'setup' as const}:undefined;}}):kind==='ambiguous'?()=>({pendingReturn:async()=>({kind:'setup' as const,transactionId:'actual-setup-transaction'}),finish:async()=>{throw new Error('ambiguous setup must not finish');},cancel:async()=>{throw new Error('ambiguous setup must not cancel');}}):undefined,
    registered: () => ({controller: registered, installationId: () => 'i'.repeat(43)}),
    observeSelection: hint => {hints.add(hint);return () => {hints.delete(hint);};},
    retire: async (_api, value) => {retired.push(value.sessionFamilyId);},
  });
  return {entry, store, events, retired, hints, get remembers() {return remembers;}, get ownerFinishes() {return ownerFinishes;},
    get registeredFinishes() {return registeredFinishes;}, get notifications() {return notifications;}, get returnedRevision() {return returnedRevision;}, get returnedNotifications() {return returnedNotifications;}, get cancels() {return cancels;}, get pending() {return pending;},
    noOwner: () => {pending = false;}, requireFactor: () => {required = true;}, supersede: () => {successor = true;}, mismatch: () => {mismatch = true;},
    failExchange: () => {exchangeFailure = true;},
    hold: () => {gate = new Promise(resolve => {release = resolve;});}, release: () => release?.(),
    holdPending: () => {pendingGate = new Promise(resolve => {releasePending = resolve;});}, releasePending: () => releasePending?.(),
  };
}

test('protected owner KIND selects the same controller before any generic restore and never remembers again', async () => {
  const f = await fixture();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.events[0], 'owner:pending');assert.equal(f.events[1], 'owner:finish');
  assert.equal(f.ownerFinishes, 1);assert.equal(f.registeredFinishes, 0);assert.equal(f.remembers, 0);
  assert.equal((await f.store.selected())?.connection.session.viewer.role, 'account');
  assert.equal(f.entry.getSnapshot().phase, 'profiles');assert.equal(f.entry.getSnapshot().snapshot?.account.role, 'owner');
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /private-code|refresh-returned|access-returned|actual-owner-transaction/);f.entry.dispose();
});
test('an old entry observer retires before the owner controller publishes its own admission', async () => {
  const f = await fixture();await f.entry.loadProfiles();await settle();assert.equal(f.hints.size, 1);
  f.events.length = 0;await f.entry.finishPorticoReturn(callback);
  assert.deepEqual(f.events.slice(0, 2), ['owner:pending', 'owner:finish']);assert.equal(f.entry.getSnapshot().phase, 'profiles');f.entry.dispose();
});
test('owner return rereads live required-setup flags instead of promoting its account grant', async () => {
  const f = await fixture();f.requireFactor();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');assert.equal((await f.store.selected())?.connection.session.viewer.role, 'account');
  assert.equal(f.remembers, 0);f.entry.dispose();
});
test('a missing owner pending keeps the existing ordinary registered return branch', async () => {
  const f = await fixture();f.noOwner();await f.entry.finishPorticoReturn(normalCallback);
  assert.equal(f.ownerFinishes, 0);assert.equal(f.registeredFinishes, 1);assert.equal(f.entry.getSnapshot().phase, 'profiles');assert.equal(f.remembers, 0);f.entry.dispose();
});
test('a no-code fragment cannot invent owner authority without protected owner KIND', async () => {
  const f = await fixture();f.noOwner();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.ownerFinishes, 0);assert.equal(f.registeredFinishes, 1);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'original-owner');f.entry.dispose();
});
test('a mixed callback cannot exchange or cancel the legitimate selected owner journal', async () => {
  const f = await fixture();await f.entry.finishPorticoReturn({...callback, fragment: callback.fragment + '&code=not-owner-input'});
  assert.equal(f.ownerFinishes, 0);assert.equal(f.registeredFinishes, 0);assert.equal(f.pending, true);assert.equal(f.cancels, 0);
  assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();assert.equal(f.pending, true);
});
test('a selected successor is preserved and only the unused returned owner family is retired', async () => {
  const f = await fixture();f.supersede();await f.entry.finishPorticoReturn(callback);
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor');assert.deepEqual(f.retired, ['returned-owner']);
  assert.equal((await f.store.read()).revision, f.returnedRevision, 'unused-return cleanup must not invalidate the successor pending revision');
  assert.equal(f.notifications, f.returnedNotifications, 'a nonmatching cleanup is not a committed selection change');
  assert.equal(f.entry.getSnapshot().phase, 'welcome');assert.equal(f.remembers, 0);f.entry.dispose();
});
test('a mismatched returned viewer cannot be selected or repaired from the stored profile', async () => {
  const f = await fixture();f.mismatch();await f.entry.finishPorticoReturn(callback);
  assert.equal(await f.store.selected(), undefined);assert.deepEqual(f.retired, ['returned-owner']);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
});
test('cancelling an owner exchange quarantines only its late returned family and preserves a successor', async () => {
  for (const successor of [false, true]) {
    const f = await fixture();if (successor) f.supersede();f.hold();
    const work = f.entry.finishPorticoReturn(callback);await settle();f.entry.cancel();f.release();await work;
    assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, successor ? 'successor' : undefined);
    assert.equal((await f.store.read()).revision, f.returnedRevision + (successor ? 0 : 1));
    assert.equal(f.notifications, f.returnedNotifications + (successor ? 0 : 1));
    assert.deepEqual(f.retired, ['returned-owner']);assert.equal(f.ownerFinishes, 1);
    assert.equal(f.registeredFinishes, 0);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
  }
});
test('cancelling while protected pending is read cannot start the owner or registered exchange', async () => {
  const f = await fixture();f.holdPending();const work = f.entry.finishPorticoReturn(callback);await settle();f.entry.cancel();f.releasePending();await work;
  assert.equal(f.ownerFinishes, 0);assert.equal(f.registeredFinishes, 0);assert.equal(f.pending, false);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
});
test('a terminal owner exchange failure does not fall back to member admission', async () => {
  const f = await fixture();f.failExchange();await f.entry.finishPorticoReturn(callback);
  assert.equal(f.ownerFinishes, 1);assert.equal(f.registeredFinishes, 0);assert.equal(f.remembers, 0);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'original-owner');f.entry.dispose();
});

test('protected setup kind uses the setup controller without a Local owner continuation',async()=>{
 const f=await fixture('setup');await f.entry.finishPorticoReturn(callback);
 assert.equal(f.ownerFinishes,1);assert.equal(f.registeredFinishes,0);assert.equal(f.entry.getSnapshot().phase,'profiles');f.entry.dispose();
});
test('two protected continuation kinds refuse without URL selection or consuming either journal',async()=>{
 const f=await fixture('ambiguous');await f.entry.finishPorticoReturn(callback);
 assert.equal(f.ownerFinishes,0);assert.equal(f.registeredFinishes,0);assert.equal(f.cancels,0);assert.equal(f.pending,true);assert.equal(f.entry.getSnapshot().phase,'welcome');f.entry.dispose();
});


test('only protected setup with the real admitted sole owner can resume the first-library screen',async()=>{
 for(const kind of ['setup','owner'] as const){
  const f=await fixture(kind,true);await f.entry.finishPorticoReturn(callback);
  const state=f.entry.getSnapshot();
  assert.equal((await f.store.selected())?.connection.session.viewer.role,'owner');
  assert.equal(state.phase,kind==='setup'?'ready':'profiles');
  assert.equal(state.setupCompleted,kind==='setup'?true:undefined);
  assert.equal(f.remembers,0);f.entry.dispose();
 }
});
test('a required two-step setup cannot be bypassed by the first-library return hint',async()=>{
 const f=await fixture('setup',true);f.requireFactor();await f.entry.finishPorticoReturn(callback);
 assert.equal(f.entry.getSnapshot().phase,'two-step-setup');
 assert.equal(f.entry.getSnapshot().setupCompleted,undefined);f.entry.dispose();
});
