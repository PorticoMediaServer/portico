import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {ServerClaimLookupResponse} from '../../../packages/contracts/src/hosted/types';

// Regression source only. The real source command is retained; a held claim
// boundary lets the original form retire before any credential publication.
test('native setup completion refuses a retired opening and releases only its original returned custody', async t => {
  const {routePath} = await import('../../../packages/contracts/src/hosted/routes.ts');
  let lookupResponse: ServerClaimLookupResponse | undefined;
  const app = await standardHomeApp({setup: true, nativeResponse: async request =>
    lookupResponse && request.path === routePath('server_claim_lookup') ? {status: 200, body: lookupResponse} : undefined});
  const {NativeServerSignIn} = await import('../src-next/platform/server-sign-in.ts');
  try {
    for (const mode of ['pre-aborted', 'retired', 'aborted'] as const) await t.test(mode, async () => {
      const source = new NativeServerSignIn();
      await source.inspect('https://standard-home.test');
      let calls = 0, released = 0, accepted = 0, live = true;
      let finish!: (completion: unknown) => void;
      // No token/session fixture is needed: original lifetime must refuse before
      // inspecting or installing this returned grant, and release its own handle.
      const claim = {finishInstalled: () => {calls++; return new Promise(resolve => {finish = resolve;});}, dispose() {}};
      (source as unknown as {hostedSetup: unknown}).hostedSetup = claim;
      const life = new AbortController(); if (mode === 'pre-aborted') life.abort();
      const before = app.requests.length;
      const pending = source.finishHostedSetup({signal: life.signal, isCurrent: () => live});
      const rejected = assert.rejects(pending);
      if (mode !== 'pre-aborted') {
        if (mode === 'retired') live = false; else life.abort();
        finish({api: {getRouteConnection: () => undefined}, release: async () => {released++;}, accept: () => {accepted++;}});
      }
      await rejected;
      assert.equal(calls, mode === 'pre-aborted' ? 0 : 1);
      assert.equal(released, mode === 'pre-aborted' ? 0 : 1); assert.equal(accepted, 0);
      assert.equal(app.requests.length, before, 'retired completion sends no new setup or credential request');
      source.dispose();
    });
    await t.test('setup approval retains healthy lookup and original-opening refusal with native cancellation', () => nativeApproval(app, value => {lookupResponse = value;}));
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});

async function nativeApproval(app: Awaited<ReturnType<typeof standardHomeApp>>, offerLookup: (value: ServerClaimLookupResponse) => void) {
  const {NativeSetupApproval} = await import('../src-next/platform/setup-approval.ts');
  const nativeAbort = createRequire(import.meta.url)('abort-controller');
  const originalController = globalThis.AbortController, originalSignal = globalThis.AbortSignal;
  globalThis.AbortController = nativeAbort.AbortController;
  globalThis.AbortSignal = nativeAbort.AbortSignal;
  try {
    for (const mode of ['source', 'opening', 'disposed', 'not-current'] as const) {
      const source = new AbortController(), opening = new AbortController();
      assert.equal(typeof source.signal.throwIfAborted, 'undefined');
      let sourceChecks = 0, openingChecks = 0, approvals = 0, declined = 0;
      if (mode === 'source') source.abort();
      if (mode === 'opening') opening.abort();
      const approval = new NativeSetupApproval({code: 'ABCD-EFGH', serverId: 'srv_' + 'a'.repeat(43), name: 'Original server', origin: 'https://original.test', transactionId: 'original-setup', expiresAt: new Date(Date.now() + 60000).toISOString()},
        async () => {sourceChecks++;}, source.signal,
        {signal: opening.signal, current: () => {openingChecks++; return mode !== 'not-current';}, canApprove: () => {approvals++; return true;}}, () => {declined++;});
      const before = app.requests.length;
      try {
        if (mode === 'disposed') approval.dispose();
        await assert.rejects(approval.review(), error => error instanceof Error && (mode === 'not-current' ? (error as {code?: string}).code === 'invalid_return' : error.name === 'AbortError'));
        assert.equal(sourceChecks, 1); assert.equal(openingChecks, mode === 'not-current' ? 1 : 0); assert.equal(approvals, 0); assert.equal(declined, 0);
        if (mode === 'not-current') {assert.equal(source.signal.aborted, false); assert.equal(opening.signal.aborted, false);}
        assert.equal(app.requests.length, before, 'original cancellation refuses before credentials or lookup transport');
      } finally {approval.dispose();}
    }
    const {NativeModules} = await import('react-native');
    const {nativeAccounts} = await import('../src-next/platform/accounts.ts');
    const {hostedUrl} = await import('../src-next/platform/account-authority.ts');
    const {routePath} = await import('../../../packages/contracts/src/hosted/routes.ts');
    const runtime = NativeModules.PorticoRuntime;
    const savedLoad = Object.getOwnPropertyDescriptor(runtime, 'loadCredentials'), savedSave = Object.getOwnPropertyDescriptor(runtime, 'saveCredentials');
    const session = {accessToken: 'fixture_hosted_access_only', refreshToken: 'fixture_hosted_refresh_only', familyId: 'original-hosted-family',
      expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
      account: {id: 'original-hosted-account', username: 'fixture-owner', displayName: 'Fixture owner'}};
    let credentialReads = 0;
    const credentials = {version: 1, revocations: [], active: {session, authority: session.refreshToken, context: {}}};
    runtime.loadCredentials = async (origin: string) => {assert.equal(origin, hostedUrl); credentialReads++; return JSON.stringify(credentials);};
    runtime.saveCredentials = async () => {assert.fail('healthy unexpired review must not replace credentials');};
    const source = new AbortController(), opening = new AbortController();
    const expected = {code: 'ABCD-EFGH', serverId: 'srv_' + 'A'.repeat(43), name: 'Original server', origin: 'https://original.test', transactionId: 'original-setup', expiresAt: new Date(Date.now() + 60000).toISOString()};
    offerLookup({code: expected.code, serverId: expected.serverId, name: expected.name, expiresAt: expected.expiresAt,
      address: expected.origin, purpose: 'setup', status: 'pending', setupTransactionId: expected.transactionId, requestedAt: new Date(Date.now() - 1000).toISOString()});
    let originalChecks = 0;
    const approval = new NativeSetupApproval(expected, async () => {originalChecks++;}, source.signal,
      {signal: opening.signal, current: () => true, canApprove: () => true}, () => {assert.fail('pending original lookup is not declined');});
    try {
      const before = app.requests.length;
      assert.deepEqual(await approval.review(), {approved: false, serverName: expected.name, address: expected.origin, accountName: session.account.displayName});
      assert.ok(originalChecks >= 5); assert.ok(credentialReads >= 3);
      assert.equal(nativeAccounts.accountCredentials().getSnapshot().session?.familyId, session.familyId);
      const calls = app.requests.slice(before); assert.equal(calls.length, 1);
      assert.equal(calls[0].url, hostedUrl + routePath('server_claim_lookup')); assert.equal(calls[0].method, 'POST');
      assert.deepEqual(calls[0].body, {code: expected.code}); assert.equal(calls[0].headers.authorization, 'Bearer ' + session.accessToken);
      assert.equal(source.signal.aborted, false); assert.equal(opening.signal.aborted, false);
      // O-163: an approved claim reports `setupInstalled`; setup never names a return registration.
      const base = {code: expected.code, serverId: expected.serverId, name: expected.name, expiresAt: expected.expiresAt,
        address: expected.origin, purpose: 'setup', setupTransactionId: expected.transactionId, requestedAt: new Date(Date.now() - 1000).toISOString()};
      offerLookup({...base, status: 'approved', setupInstalled: true});
      assert.equal((await approval.review()).approved, true);
      for (const refused of [{...base, status: 'approved', setupInstalled: true, returnRegistration: {id: 'ret_' + 'a'.repeat(43), origin: 'https://managed.test'}},
        {...base, status: 'pending', setupInstalled: true}]) {
        offerLookup(refused as ServerClaimLookupResponse);
        await assert.rejects(approval.review(), error => (error as {code?: string}).code === 'invalid_return');
      }
    } finally {
      approval.dispose();
      if (savedLoad) Object.defineProperty(runtime, 'loadCredentials', savedLoad); else delete runtime.loadCredentials;
      if (savedSave) Object.defineProperty(runtime, 'saveCredentials', savedSave); else delete runtime.saveCredentials;
    }
  } finally {
    globalThis.AbortController = originalController; globalThis.AbortSignal = originalSignal;
  }
}
