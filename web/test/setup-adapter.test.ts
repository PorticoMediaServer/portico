import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, sign} from 'node:crypto';
import {HttpLocalApi, transportJSON} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';
import {createMemorySetupStore, type SetupRecord} from '@core/setup-store.ts';
import type {SetupPortContext} from '@core/setup-controller.ts';
import {createWebSetupAdapter} from '../src/platform/setup-adapter.ts';

// The public claim vector's seed key signs each setup start as the server would, for whatever start the adapter makes.
const vector = JSON.parse(readFileSync(new URL('../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const claim = {...JSON.parse(vector.claimPayload), purpose: 'setup'}, parent = JSON.parse(vector.startPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const signed = (v: unknown) => {const raw = transportJSON(v); return {payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), key).toString('base64url')};};
const REQUEST = '11111111-2222-4333-8444-555555555555', NOW = Date.parse('2030-01-02T03:04:07Z');
const installationId = parent.installationId as string;

type Call = {path: string; method: string; token?: string; installation?: string; body?: unknown};
async function fixture(t: TestContext, options: {local?: {record?: SetupRecord}; answers?: Record<string, (body: unknown) => Response>} = {}) {
  setInstallationIdentity({installationId, name: 'Browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const pin = {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))};
  const origin = claim.address as string, calls: Call[] = [], navigated: string[] = [];
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
  const failure = (status: number, code: string) => json({error: {code, message: code, retryable: false}}, status);
  let setupState: unknown = {status: 'ready'};
  const answers: Record<string, (body: unknown) => Response> = {
    'GET /v1/setup/state': () => json(setupState),
    'POST /v1/setup/browser': () => json({serverId: pin.serverId, setupToken: 'issued-setup-token'}),
    'POST /v1/setup': () => json({variant: 'claim-required', claimRequired: true, serverId: pin.serverId, requestId: REQUEST, authMode: 'hosted', revision: '2'}),
    'POST /v1/setup/approval': body => {
      const start = (body as {setupStart: {state: string; codeChallenge: string; installationId: string}}).setupStart;
      const payload = {kind: 'portico.setup-start.v1', audience: claim.audience, claimHash,
        operationId: claim.operationId, serverId: claim.serverId, publicKey: claim.publicKey, localGeneration: claim.localGeneration, address: claim.address, purpose: 'setup',
        setupRequestId: REQUEST, setupRevision: '2', state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId,
        serverChallenge: parent.serverChallenge, issuedAt: parent.issuedAt, expiresAt: parent.expiresAt};
      const code = 'claim_' + 'K'.repeat(43);
      return json({state: 'prepared', identity: {serverId: claim.serverId, localGeneration: claim.localGeneration}, operation: {operationId: claim.operationId, localGeneration: claim.localGeneration, revision: '2'},
        accountId: 'account-unbound', installationAcknowledged: false, approvalRequired: true, actions: ['cancel', 'approve'],
        approvalUrl: claim.audience + '/claim#' + new URLSearchParams({code, operation: claim.operationId, server: claim.name}), claimCode: code,
        setupContinuation: {transactionId: 'setup_transaction', phase: 'prepared', expiresAt: parent.expiresAt, claim: signed(claim), start: signed(payload)}});
    },
    'POST /v1/setup/abandon': () => json({status: 'ready'}),
    ...options.answers,
  };
  const claimHash = routeBase64(await webRouteCrypto.sha256(Buffer.from(transportJSON(claim))));
  const fetcher: typeof fetch = async (url, init) => {
    const u = new URL(String(url)), method = init?.method ?? 'GET', headers = new Headers(init?.headers);
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({path: u.pathname, method, token: headers.get('Authorization')?.replace('Bearer ', ''), installation: headers.get('X-Portico-Installation-Id') ?? undefined, body});
    if (method === 'POST' && u.pathname === '/v1/setup/approval' && headers.get('X-Portico-Installation-Id') !== installationId) return failure(400, 'invalid_request');
    const answer = answers[`${method} ${u.pathname}`];
    return answer ? answer(body) : failure(404, 'not_found');
  };
  const route = {logicalOrigin: origin, pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  let record: AppSessionRecord = {version: 1, revision: 0, servers: []};
  const sessions = new AppSessionStore({read: async () => record, change: async update => {record = update(record)!;}});
  const store = createMemorySetupStore(options.local ?? {});
  const adapter = createWebSetupAdapter({hostedOrigin: claim.audience, pageOrigin: () => origin,
    pair: async () => {const api = new HttpLocalApi(origin, '', fetcher); api.useRoutes(route as never); return {server: {id: pin.serverId, name: 'Den'} as never, api, pin, dispose: () => {}};},
    sessions: () => sessions, environment: () => ({}) as never, restore: async () => undefined, remember: async () => {}, store: () => store,
    installationId: () => installationId, navigate: url => navigated.push(url), now: () => NOW, newRequestId: () => REQUEST});
  t.after(() => adapter.dispose());
  const context = (): SetupPortContext => ({signal: new AbortController().signal, intentId: 'intent-1'});
  const bumpSelection = async () => {record = {...record, revision: record.revision + 1};};
  return {adapter, ports: adapter.ports, calls, navigated, store, context, setState: (value: unknown) => {setupState = value;}, bumpSelection};
}
const own = (phase: string) => ({status: 'in_progress', setup: {variant: phase, requestId: REQUEST, revision: '2'}});
const hostedRecord: SetupRecord = {version: 2, serverId: claim.serverId, requestId: REQUEST, setupToken: 'saved-setup-token', tokenAt: 1, name: 'Den', authMode: 'hosted'};

test('a read pairs first, then asks with the saved setup token and returns the records it read', async t => {
  const f = await fixture(t, {local: {record: hostedRecord}});
  f.setState(own('awaiting_approval'));
  const read = await f.ports.readState(f.context());
  assert.equal(read.server.kind, 'state');
  assert.deepEqual(read.local.record, hostedRecord);
  assert.deepEqual(f.calls.map(c => `${c.method} ${c.path} ${c.token ?? ''}`), ['GET /v1/setup/state saved-setup-token']);
  // Without a record the read carries no token.
  const fresh = await fixture(t);
  await fresh.ports.readState(fresh.context());
  assert.equal(fresh.calls[0]!.token, undefined);
});

test('a refused setup token reads as superseded, never as a failure to read', async t => {
  const f = await fixture(t, {local: {record: hostedRecord}, answers: {'GET /v1/setup/state': () => new Response(JSON.stringify({error: {code: 'unauthorized', message: 'x', retryable: false}}), {status: 401, headers: {'Content-Type': 'application/json'}})}});
  assert.equal((await f.ports.readState(f.context())).server.kind, 'tokenRefused');
});

test('Continue: start, the record written before initialize, one approval call, and the verified handoff bound with its link', async t => {
  const f = await fixture(t);
  await f.ports.readState(f.context());
  assert.deepEqual(await f.ports.start(f.context()), {kind: 'startDone'});
  const writes: unknown[] = [];
  const change = f.store.change.bind(f.store);
  f.store.change = async (expect, next) => {writes.push({expect, next, sentSoFar: f.calls.length}); return change(expect, next);};
  assert.deepEqual(await f.ports.initializeHosted('Den', undefined, f.context()), {kind: 'initializeDone'});
  const setupCall = f.calls.findIndex(c => c.path === '/v1/setup' && c.method === 'POST');
  assert.ok((writes[0] as {sentSoFar: number}).sentSoFar <= setupCall, 'the record is saved before the request');
  assert.deepEqual(f.calls[setupCall]!.body, {requestId: REQUEST, setupToken: 'issued-setup-token', name: 'Den', authMode: 'hosted', interactive: true});
  const ready = await f.ports.requestApproval(true, f.context());
  assert.equal(ready.kind, 'approvalReady');
  const approval = f.calls.filter(c => c.path === '/v1/setup/approval');
  assert.equal(approval.length, 1);
  assert.equal(approval[0]!.token, 'issued-setup-token');
  assert.equal(approval[0]!.installation, installationId, 'the server refuses an approval request without this installation');
  const attempt = f.store.peek().attempt!;
  assert.equal(attempt.handoff?.transactionId, 'setup_transaction');
  assert.equal(attempt.approvalUrl, (ready as {approvalUrl: string}).approvalUrl);
  assert.equal(attempt.requestId, REQUEST);
  // Asking again in this tab reuses the same start, so the server's stored pair still matches.
  await f.ports.requestApproval(true, f.context());
  assert.equal(f.store.peek().attempt!.attemptId, attempt.attemptId);
});

test('a server selection change during Continue stops it before anything is bound', async t => {
  const f = await fixture(t);
  await f.ports.readState(f.context());
  await f.ports.start(f.context());
  await f.ports.initializeHosted('Den', undefined, f.context());
  await f.bumpSelection();
  await assert.rejects(f.ports.requestApproval(true, f.context()), /changed/);
  assert.equal(f.calls.filter(c => c.path === '/v1/setup/approval').length, 0);
});

test('Back sends the setup\'s own request and revision; a server that has moved on refuses it', async t => {
  const f = await fixture(t, {local: {record: hostedRecord}});
  f.setState(own('started'));
  await f.ports.readState(f.context());
  assert.deepEqual(await f.ports.abandon(f.context()), {kind: 'abandoned'});
  assert.deepEqual(f.calls.at(-1)!.body, {requestId: REQUEST, revision: '2', setupToken: 'saved-setup-token'});
  const late = await fixture(t, {local: {record: hostedRecord}, answers: {'POST /v1/setup/abandon': () => new Response(JSON.stringify({error: {code: 'setup_state_changed', message: 'x', retryable: false}}), {status: 409, headers: {'Content-Type': 'application/json'}})}});
  late.setState(own('approved'));
  await late.ports.readState(late.context());
  assert.deepEqual(await late.ports.abandon(late.context()), {kind: 'abandonRefused'});
});

test('the exchange on the web needs the Return: the person is sent back to Portico Services for it', async t => {
  const f = await fixture(t);
  await f.ports.readState(f.context());
  await f.ports.start(f.context());
  await f.ports.initializeHosted('Den', undefined, f.context());
  const ready = await f.ports.requestApproval(true, f.context()) as {approvalUrl: string};
  const abort = new AbortController();
  const leaving = f.ports.exchange({signal: abort.signal, intentId: 'i'});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.navigated, [ready.approvalUrl]);
  abort.abort(new Error('left'));
  await assert.rejects(leaving, /left/);
});

test('a public http address is refused before any start request', async t => {
  const f = await fixture(t);
  const adapter = createWebSetupAdapter({hostedOrigin: claim.audience, pageOrigin: () => 'http://8.8.8.8:32500',
    pair: async () => ({server: {id: claim.serverId, name: 'Den'} as never, api: new HttpLocalApi('http://8.8.8.8:32500', '', async () => {throw new Error('must not send');}), pin: {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: 'f'}, dispose: () => {}}),
    sessions: () => new AppSessionStore({read: async () => ({version: 1, revision: 0, servers: []}), change: async () => {}}), environment: () => ({}) as never,
    restore: async () => undefined, remember: async () => {}, store: () => f.store, installationId: () => installationId, navigate: () => {}});
  t.after(() => adapter.dispose());
  assert.deepEqual(await adapter.ports.start(f.context()), {kind: 'addressNotAllowed'});
});
