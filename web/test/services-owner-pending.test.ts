import assert from 'node:assert/strict';
import {test} from 'node:test';
import {captureHostedOwnerClaim} from '../src/services/owner-claim.ts';
import {hostedOwnerPendingStore, restoreHostedOwnerPending, forgetHostedOwnerPending, scopedHostedOwnerClaim,
  type HostedOwnerPending, type HostedOwnerPendingStore} from '../src/services/owner-pending.ts';
import type {ServicesAccount} from '../src/services/session.ts';

const origin = 'https://accounts.example';
const code = 'claim_' + Buffer.alloc(32, 5).toString('base64url');
function capture() {
  const href = origin + '/claim#' + new URLSearchParams({code, operation: 'current_operation', server: 'Actual server'});
  const page = {href, pathname: '/claim', search: '', hash: new URL(href).hash};
  return captureHostedOwnerClaim(page, {state: null, replaceState: () => {page.hash = '';}}, origin)!;
}
function vault() {
  let saved: HostedOwnerPending | undefined, available = true;
  const store: HostedOwnerPendingStore = {assertAvailable: () => {if (!available) throw new Error('secure storage unavailable');},
    read: async () => saved, change: async update => {saved = update(saved);}};
  return {store, saved: () => saved, unavailable: () => {available = false;}};
}
function accountFixture() {
  let familyId = 'actual-family', installed = false;
  const requestedAt = new Date(Date.now() - 1000).toISOString(), expiresAt = new Date(Date.now() + 90000).toISOString();
  const calls: string[] = [];
  let beforeResponse = async (_path: string) => {};
  const account = {api: {origin, send: async (path: string) => {
    calls.push(path); await beforeResponse(path);
    if (path.endsWith('/lookup')) return {code, serverId: 'srv_' + Buffer.alloc(32, 1).toString('base64url'), name: 'Actual server',
      address: 'https://original.example', purpose: 'switch', requestedAt, expiresAt, status: installed ? 'approved' : 'pending',
      ownerTransactionId: 'opaque_transaction', ...(installed ? {returnRegistration: {id: 'ret_' + Buffer.alloc(32, 2).toString('base64url'), origin: 'https://managed.example'}} : {})};
    if (path.endsWith('/complete')) return {location: 'https://original.example/auth/portico-return#transaction=opaque_transaction'};
    return {code: 'claim_decided', status: 'decided'};
  }}, service: {synchronize: async () => {}, accessSession: async () => ({account: {id: 'actual-account'}, familyId, accessToken: 'memory-only-fixture'}),
    getSnapshot: () => ({session: {account: {id: 'actual-account'}, familyId, accessToken: 'memory-only-fixture'}})}} as unknown as ServicesAccount;
  return {account, calls, expiresAt, installed: () => {installed = true;}, replace: () => {familyId = 'replacement-family';},
    intercept: (work: typeof beforeResponse) => {beforeResponse = work;}};
}

test('owner approval restores its scrubbed code without renewing the local budget or starting account work', async () => {
  const f = vault(), now = Date.now();
  const pending = await restoreHostedOwnerPending(f.store, origin, capture(), now);
  assert.equal(pending?.kind, 'owner-approval'); assert.equal(pending?.request.code, code);
  const restored = await restoreHostedOwnerPending(f.store, origin, undefined, now + 1000);
  assert.equal(restored?.attemptId, pending?.attemptId); assert.equal(restored?.deadline, now + 300000);
  await assert.rejects(restoreHostedOwnerPending(f.store, origin, undefined, now + 300000)); assert.equal(f.saved(), undefined);
});
test('secure storage refusal happens before pending read/write', async () => {
  let effects = 0;
  const store: HostedOwnerPendingStore = {assertAvailable: () => {throw new Error('secure context required');},
    read: async () => {effects++; return undefined;}, change: async () => {effects++;}};
  await assert.rejects(restoreHostedOwnerPending(store, origin, capture())); assert.equal(effects, 0);
});
test('owner factory checks the secure context before creating any tab locator', async () => {
  const names = ['location', 'isSecureContext', 'sessionStorage'] as const;
  const descriptors = names.map(name => Object.getOwnPropertyDescriptor(globalThis, name)); let locators = 0;
  try {
    Object.defineProperty(globalThis, 'location', {configurable: true, value: {protocol: 'http:', hostname: 'server.example'}});
    Object.defineProperty(globalThis, 'isSecureContext', {configurable: true, value: false});
    Object.defineProperty(globalThis, 'sessionStorage', {configurable: true, get() {locators++; throw new Error('locator read');}});
    const store = hostedOwnerPendingStore(); assert.equal(locators, 0);
    await assert.rejects(store.read(), {code: 'secure_context_required'}); assert.equal(locators, 0);
  } finally {names.forEach((name, index) => {const descriptor = descriptors[index]; if (descriptor) Object.defineProperty(globalThis, name, descriptor); else Reflect.deleteProperty(globalThis, name);});}
});
test('a fresh capture supersedes even an identical link, while old cancellation preserves the successor', async () => {
  const f = vault(), now = Date.now(), first = await restoreHostedOwnerPending(f.store, origin, capture(), now);
  const next = await restoreHostedOwnerPending(f.store, origin, capture(), now);
  assert.notEqual(first?.attemptId, next?.attemptId);
  assert.equal(await forgetHostedOwnerPending(f.store, first!), false); assert.equal(f.saved()?.attemptId, next?.attemptId);
  await assert.rejects(restoreHostedOwnerPending(f.store, origin, {})); assert.equal(f.saved(), undefined);
});
test('provider/document disposal retains protected pending, and authenticated review can only shorten its deadline', async () => {
  const f = vault(), a = accountFixture(), pending = await restoreHostedOwnerPending(f.store, origin, capture());
  const first = scopedHostedOwnerClaim(a.account, f.store, pending!); first.dispose(); await first.cancel();
  assert.equal(f.saved()?.attemptId, pending?.attemptId); assert.equal(a.calls.length, 0);
  const restored = await restoreHostedOwnerPending(f.store, origin), current = scopedHostedOwnerClaim(a.account, f.store, restored!);
  await current.review(); assert.equal(f.saved()?.deadline, Date.parse(a.expiresAt));
  assert.deepEqual(a.calls, ['/v1/server-claims/lookup']); current.dispose();
});
test('an in-flight old review cannot publish or shorten a newer pending capture', async () => {
  const f = vault(), a = accountFixture(), pending = await restoreHostedOwnerPending(f.store, origin, capture());
  let release!: () => void, entered!: () => void;
  const blocked = new Promise<void>(resolve => {release = resolve;}), started = new Promise<void>(resolve => {entered = resolve;});
  a.intercept(async () => {entered(); await blocked;});
  const controller = scopedHostedOwnerClaim(a.account, f.store, pending!), old = controller.review(), rejected = assert.rejects(old);
  // The old review stays in flight until its own response is released. Racing
  // the entry gate with the review keeps a premature settlement a named failure
  // instead of leaving `started` pending and stalling the whole file.
  const outcome = await Promise.race([started.then(() => 'started'), old.then(() => 'settled', () => 'settled')]);
  assert.equal(outcome, 'started', 'the old review cannot settle before its released response');
  const next = await restoreHostedOwnerPending(f.store, origin, capture()); release(); await rejected;
  assert.equal(f.saved()?.attemptId, next?.attemptId); assert.equal(f.saved()?.deadline, next?.deadline); controller.dispose();
});
test('explicit denial retires only its pending; a successful return spends its pending without creating a Server session', async () => {
  const f = vault(), a = accountFixture(), pending = await restoreHostedOwnerPending(f.store, origin, capture());
  const denied = scopedHostedOwnerClaim(a.account, f.store, pending!); await denied.review(); await denied.decide('deny'); assert.equal(f.saved(), undefined);
  const g = vault(), b = accountFixture(), next = await restoreHostedOwnerPending(g.store, origin, capture());
  const accepted = scopedHostedOwnerClaim(b.account, g.store, next!); await accepted.review(); await accepted.decide('approve');
  assert.equal(await accepted.complete(), undefined, 'no completion until the real installed registration exists');
  b.installed(); assert.equal(await accepted.complete(), 'https://original.example/auth/portico-return#transaction=opaque_transaction');
  assert.equal(g.saved(), undefined); await assert.rejects(accepted.complete());
  assert(b.calls.every(path => !path.endsWith('/start') && !path.endsWith('/exchange')));
});
test('a family replacement during final durable cleanup prevents delivery of a return URL', async () => {
  const f = vault(), a = accountFixture(), pending = await restoreHostedOwnerPending(f.store, origin, capture());
  let replaceOnForget = false;
  const store: HostedOwnerPendingStore = {...f.store, change: async update => {
    await f.store.change(update); if (replaceOnForget && !f.saved()) a.replace();
  }};
  const controller = scopedHostedOwnerClaim(a.account, store, pending!); await controller.review(); a.installed(); replaceOnForget = true;
  await assert.rejects(controller.complete(), {code: 'account_changed'}); assert.equal(f.saved(), undefined); controller.dispose();
});
test('a transient final account recheck keeps the original protected return attempt for exact retry', async () => {
  const f = vault(), a = accountFixture(), pending = await restoreHostedOwnerPending(f.store, origin, capture());
  let responseReceived = false, armed = false, failNextSync = false;
  a.intercept(async path => {if (path.endsWith('/complete')) responseReceived = true;});
  const store: HostedOwnerPendingStore = {...f.store, read: async () => {
    const value = await f.store.read();
    if (responseReceived && !armed) {armed = true; failNextSync = true;}
    return value;
  }};
  a.account.service.synchronize = async () => {if (failNextSync) {failNextSync = false; throw new Error('temporary account transport failure');}};
  const controller = scopedHostedOwnerClaim(a.account, store, pending!); await controller.review(); a.installed();
  await assert.rejects(controller.complete());
  assert.equal(f.saved()?.attemptId, pending!.attemptId, 'no return was delivered, so its exact proof stays pending');
  assert.equal(await controller.complete(), 'https://original.example/auth/portico-return#transaction=opaque_transaction');
  assert.equal(f.saved(), undefined); controller.dispose();
});
