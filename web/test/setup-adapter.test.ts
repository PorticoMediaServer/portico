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

test('a public http address is refused before any start request', async t => {
  const f = await fixture(t);
  const adapter = createWebSetupAdapter({hostedOrigin: claim.audience, pageOrigin: () => 'http://8.8.8.8:32500',
    pair: async () => ({server: {id: claim.serverId, name: 'Den'} as never, api: new HttpLocalApi('http://8.8.8.8:32500', '', async () => {throw new Error('must not send');}), pin: {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: 'f'}, dispose: () => {}}),
    sessions: () => new AppSessionStore({read: async () => ({version: 1, revision: 0, servers: []}), change: async () => {}}), environment: () => ({}) as never,
    restore: async () => undefined, remember: async () => {}, store: () => f.store, installationId: () => installationId, navigate: () => {}});
  t.after(() => adapter.dispose());
  assert.deepEqual(await adapter.ports.start(f.context()), {kind: 'addressNotAllowed'});
});
