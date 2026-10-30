import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createHash, generateKeyPairSync, sign} from 'node:crypto';
import {buildHostedSignInStartURL} from '@core/hosted-sign-in-start.ts';
import type {RegisteredSignInWithContext} from '@core/same-tab-return.ts';
import {captureServicesEntry} from '../src/services/entry.ts';
import {continueHostedSignIn, restoreHostedSignIn, type SignInStore} from '../src/services/sign-in.ts';
import type {ServicesAccount} from '../src/services/session.ts';

const origin = 'https://account.example';
function fixture() {
  const keys = generateKeyPairSync('ed25519'), key = keys.publicKey.export({format: 'der', type: 'spki'}).subarray(-32);
  const token = (n: number) => Buffer.alloc(32, n).toString('base64url');
  const now = Date.now(), stamp = (time: number) => new Date(time).toISOString().replace(/(\.\d*?)0+Z$/, '$1Z').replace('.Z', 'Z');
  const facts = {kind: 'portico.registered-sign-in-prepare', version: '1',
    serverId: 'srv_' + createHash('sha256').update('portico.server.identity.v1\0').update(key).digest('base64url'),
    publicKey: key.toString('base64url'), fingerprint: createHash('sha256').update(key).digest('base64url'), serverName: 'Genuine server',
    registrationId: 'ret_' + token(2), registrationOrigin: 'https://server.example', hostedOrigin: origin, claimOperationId: 'installed_operation',
    challenge: token(5), nonce: token(6), installationId: '10000000-0000-4000-8000-000000000001', issuedAt: stamp(now), expiresAt: stamp(now + 120000)};
  const payload = Buffer.from(JSON.stringify(facts));
  const handoff: RegisteredSignInWithContext = {start: {serverId: facts.serverId, purpose: 'sign-in', claimOperationId: facts.claimOperationId,
    registrationId: facts.registrationId, state: token(3), codeChallenge: token(4), serverChallenge: facts.challenge, installationId: facts.installationId},
    registration: {id: facts.registrationId, origin: facts.registrationOrigin}, hostedOrigin: origin, expiresAt: facts.expiresAt,
    context: {payload: payload.toString('base64url'), signature: sign(null, payload, keys.privateKey).toString('base64url')}};
  let saved: Awaited<ReturnType<SignInStore['read']>>;
  const store: SignInStore = {read: async () => saved, change: async update => {saved = update(saved);}};
  let session: any = {account: {id: 'account', username: 'nana_r', displayName: ''}, familyId: 'family', accessToken: 'memory-only-test-access'};
  const calls: string[] = [], navigations: string[] = [];
  let afterRequest = () => {};
  const transaction = 'srt_' + 'x'.repeat(32);
  const account = {service: {synchronize: async () => {}, accessSession: async () => session, getSnapshot: () => ({session})},
    api: {origin, send: async (path: string) => {calls.push(path); afterRequest(); return path.endsWith('/start') ? {transactionId: transaction} :
      {location: facts.registrationOrigin + '/auth/portico-return#code=src_' + 'z'.repeat(32) + '&state=' + handoff.start.state + '&transaction=' + transaction};}}} as unknown as ServicesAccount;
  return {handoff, store, account, calls, navigations, saved: () => saved, href: buildHostedSignInStartURL(handoff),
    replaceFamily: () => {session = {...session, familyId: 'replacement'};}, after: (run: () => void) => {afterRequest = run;}};
}
test('ordinary entry scrubs synchronously, verifies signed context and persists without issuing an answer', async () => {
  const f = fixture(), page = {href: f.href, pathname: '/', search: '', hash: new URL(f.href).hash, replace() {throw new Error('native dispatch');}};
  const history = {state: null, replaceState: () => {page.hash = '';}};
  const entry = captureServicesEntry(page, history, origin);
  assert.equal(page.hash, ''); assert.equal(f.saved(), undefined); assert.deepEqual(f.calls, []);
  const pending = await restoreHostedSignIn(f.store, origin, entry.signIn);
  assert.equal(pending?.serverName, 'Genuine server'); assert.deepEqual(f.calls, []);
  assert.equal((await restoreHostedSignIn(f.store, origin))?.serverName, 'Genuine server', 'provider reload uses encrypted pending, unchanged expiry');
  assert.equal(f.saved()?.href, f.href);
});
test('only explicit Continue starts/completes, then clears its pending and navigates to the genuine registered origin', async () => {
  const f = fixture(), pending = await restoreHostedSignIn(f.store, origin, {href: f.href});
  assert.deepEqual(f.calls, []);
  await continueHostedSignIn(f.account, pending!, {accountId: 'account', familyId: 'family'}, f.store,
    new AbortController().signal, value => f.navigations.push(value));
  assert.equal(f.calls.length, 2); assert.equal(f.calls[0], '/v1/server-returns/start'); assert.match(f.calls[1], /\/complete$/);
  assert.equal(f.saved(), undefined); assert.equal(f.navigations.length, 1); assert.match(f.navigations[0], /^https:\/\/server.example\/auth\/portico-return#/);
});
test('a family replacement after Start prevents Complete and navigation without consuming pending', async () => {
  const f = fixture(), pending = await restoreHostedSignIn(f.store, origin, {href: f.href}); f.after(f.replaceFamily);
  await assert.rejects(continueHostedSignIn(f.account, pending!, {accountId: 'account', familyId: 'family'}, f.store,
    new AbortController().signal, value => f.navigations.push(value)));
  assert.equal(f.calls.length, 1); assert.equal(f.navigations.length, 0); assert(f.saved());
});
test('invalid new context clears predecessor; cancellation and signed-name forgery issue no requests', async () => {
  const f = fixture(); await restoreHostedSignIn(f.store, origin, {href: f.href});
  await assert.rejects(restoreHostedSignIn(f.store, origin, {})); assert.equal(f.saved(), undefined);
  const g = fixture(), pending = await restoreHostedSignIn(g.store, origin, {href: g.href});
  const controller = new AbortController(); controller.abort();
  await assert.rejects(continueHostedSignIn(g.account, pending!, {accountId: 'account', familyId: 'family'}, g.store, controller.signal, value => g.navigations.push(value)));
  assert.equal(g.calls.length, 0);
  const raw = JSON.parse(Buffer.from(g.handoff.context.payload, 'base64url').toString()); raw.serverName = 'Forged';
  const forged = {...g.handoff, context: {...g.handoff.context, payload: Buffer.from(JSON.stringify(raw)).toString('base64url')}};
  await assert.rejects(restoreHostedSignIn(g.store, origin, {href: buildHostedSignInStartURL(forged)})); assert.equal(g.saved(), undefined);
});
test('the signed Hosted origin must be the canonical API origin before any bearer request', async () => {
  const f = fixture(), pending = await restoreHostedSignIn(f.store, origin, {href: f.href});
  Object.assign(f.account.api, {origin: 'https://different.example'});
  await assert.rejects(continueHostedSignIn(f.account, pending!, {accountId: 'account', familyId: 'family'}, f.store,
    new AbortController().signal, value => f.navigations.push(value)));
  assert.equal(f.calls.length, 0); assert.equal(f.navigations.length, 0); assert(f.saved());
});

for (const invalidSignature of [false, true]) test(`a late ${invalidSignature ? 'invalid' : 'valid'} capture cannot replace or clear its successor`, async () => {
  const first = fixture(), second = fixture();
  const href = invalidSignature ? buildHostedSignInStartURL({...first.handoff, context: {...first.handoff.context,
    signature: Buffer.alloc(64, 1).toString('base64url')}}) : first.href;
  let release!: () => void, entered!: () => void;
  const blocked = new Promise<void>(resolve => {release = resolve;}), started = new Promise<void>(resolve => {entered = resolve;});
  let firstWrite = true;
  const store: SignInStore = {read: first.store.read, change: async update => {
    await first.store.change(update);
    if (firstWrite) {firstWrite = false; entered(); await blocked;}
  }};
  const old = restoreHostedSignIn(store, origin, {href});
  // Attach the rejection handler before releasing asynchronous verification.
  const rejected = assert.rejects(old);
  await started;
  const current = await restoreHostedSignIn(store, origin, {href: second.href});
  release(); await rejected;
  assert.equal(current?.handoff.context.payload, second.handoff.context.payload);
  assert.equal(first.saved()?.href, second.href);
  assert.equal((await restoreHostedSignIn(store, origin))?.handoff.context.payload, second.handoff.context.payload);
  assert.equal(first.calls.length + second.calls.length, 0, 'restoration never issues an account answer');
});

test('finishing a return cannot clear a successor which happens to carry the same state', async () => {
  const first = fixture(), second = fixture();
  assert.equal(first.handoff.start.state, second.handoff.start.state);
  let replaceOnCommit = false;
  const store: SignInStore = {read: first.store.read, change: async update => {
    if (replaceOnCommit) {replaceOnCommit = false; await first.store.change(() => ({version: 1, href: second.href}));}
    await first.store.change(update);
  }};
  const pending = await restoreHostedSignIn(store, origin, {href: first.href});
  replaceOnCommit = true;
  await assert.rejects(continueHostedSignIn(first.account, pending!, {accountId: 'account', familyId: 'family'}, store,
    new AbortController().signal, value => first.navigations.push(value)));
  assert.equal(first.calls.length, 2);
  assert.equal(first.navigations.length, 0);
  assert.equal(first.saved()?.href, second.href);
});
