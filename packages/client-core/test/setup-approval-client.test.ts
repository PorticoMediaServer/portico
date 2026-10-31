import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, sign} from 'node:crypto';
import {transportJSON} from '../src/index.ts';
import {HttpLocalApi} from '../src/index.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {requestSetupApproval} from '../src/setup-approval-client.ts';
import type {SetupClaimHandoff} from '../src/setup-claim-client.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '../src/route-identity.ts';

// The same public interoperability seed as setup-claim-client.test.ts: setup-only proofs exercising the binding.
const vector = JSON.parse(readFileSync(new URL('../../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const claim = {...JSON.parse(vector.claimPayload), purpose: 'setup'}, parent = JSON.parse(vector.startPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const signed = (v: unknown) => {const raw = transportJSON(v); return {payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), key).toString('base64url')};};
const startPayload = {kind: 'portico.setup-start.v1', audience: claim.audience, claimHash: routeBase64(await webRouteCrypto.sha256(Buffer.from(transportJSON(claim)))), operationId: claim.operationId,
  serverId: claim.serverId, publicKey: claim.publicKey, localGeneration: claim.localGeneration, address: claim.address, purpose: 'setup',
  setupRequestId: '11111111-2222-4333-8444-555555555555', setupRevision: '2', state: parent.state, codeChallenge: parent.codeChallenge,
  installationId: parent.installationId, serverChallenge: parent.serverChallenge, issuedAt: parent.issuedAt, expiresAt: parent.expiresAt};
const claimProof = signed(claim), startProof = signed(startPayload);

async function fixture(t: TestContext, answer?: (raw: Record<string, unknown>) => unknown) {
  setInstallationIdentity({installationId: startPayload.installationId, name: 'Setup browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const pin = {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))};
  const start = {pin, origin: claim.address, hostedOrigin: claim.audience, setupRequestId: startPayload.setupRequestId, setupRevision: startPayload.setupRevision,
    state: startPayload.state, codeChallenge: startPayload.codeChallenge, installationId: startPayload.installationId};
  const operation = {operationId: claim.operationId, localGeneration: claim.localGeneration, revision: '2'};
  const code = 'claim_' + 'K'.repeat(43), transactionId = 'actual_setup_transaction';
  const approvalUrl = claim.audience + '/claim#' + new URLSearchParams({code, operation: claim.operationId, server: claim.name});
  const raw: Record<string, unknown> = {state: 'prepared', identity: {serverId: claim.serverId, localGeneration: claim.localGeneration}, operation, accountId: 'account-unbound',
    installationAcknowledged: false, approvalRequired: true, actions: ['cancel', 'approve'], approvalUrl, claimCode: code,
    setupContinuation: {transactionId, phase: 'prepared', expiresAt: startPayload.expiresAt, claim: {...claimProof}, start: {...startProof}}};
  const sent: {url: string; init?: RequestInit; body: unknown}[] = [], bound: SetupClaimHandoff[] = [];
  let current = true;
  const fetcher: typeof fetch = async (url, init) => {
    sent.push({url: String(url), init, body: JSON.parse(String(init?.body))});
    return new Response(JSON.stringify(answer ? answer(raw) : raw), {headers: {'Content-Type': 'application/json'}});
  };
  const api = new HttpLocalApi(start.origin, 'captured-setup-token', fetcher);
  api.useRoutes({logicalOrigin: start.origin, pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'})} as never);
  const ports = {assertCurrent: async () => {if (!current) throw new Error('attempt changed');}, bind: async (value: SetupClaimHandoff) => {if (!current) throw new Error('attempt changed'); bound.push(value);}};
  const now = () => Date.parse('2030-01-02T03:04:07Z');
  return {api, start, ports, sent, bound, approvalUrl, transactionId, operation, run: () => requestSetupApproval(api, start, ports, {now}), replace: () => {current = false;}};
}

test('one call: the setup token and installation travel, and the server\'s operation comes back bound to this start', async t => {
  const f = await fixture(t);
  const out = await f.run();
  assert.equal(out.approvalUrl, f.approvalUrl);
  assert.equal(f.sent.length, 1);
  assert.equal(f.sent[0].url, claim.address + '/v1/setup/approval');
  assert.deepEqual(f.sent[0].body, {setupStart: {state: f.start.state, codeChallenge: f.start.codeChallenge, installationId: f.start.installationId}});
  const headers = new Headers(f.sent[0].init?.headers);
  assert.equal(headers.get('Authorization'), 'Bearer captured-setup-token');
  assert.equal(headers.get('X-Portico-Installation-Id'), f.start.installationId);
  assert.equal(f.sent[0].init?.redirect, 'error');
  assert.deepEqual(f.bound, [out.handoff]);
  assert.deepEqual(out.handoff.expected, f.operation);
  assert.equal(out.handoff.binding.operationId, claim.operationId);
  assert.equal(out.handoff.transactionId, f.transactionId);
  assert.equal(Object.isFrozen(out.handoff), true);
});

test('a signed start for another start, another operation, or a renamed link is refused and nothing is bound', async t => {
  for (const [label, change] of [
    ['another operation named', (raw: Record<string, unknown>) => ({...raw, operation: {operationId: 'op_other', localGeneration: claim.localGeneration, revision: '2'}})],
    ['another link', (raw: Record<string, unknown>) => ({...raw, approvalUrl: claim.audience + '/claim#' + new URLSearchParams({code: 'claim_' + 'Z'.repeat(43), operation: claim.operationId, server: claim.name})})],
    ['no operation', (raw: Record<string, unknown>) => ({...raw, operation: undefined})],
    ['an installed answer to a fresh request', (raw: Record<string, unknown>) => ({...raw, state: 'installed'})],
  ] as const) {
    const f = await fixture(t, change);
    await assert.rejects(f.run(), /no longer matches/, label);
    assert.deepEqual(f.bound, [], label);
  }
  const other = await fixture(t);
  await assert.rejects(requestSetupApproval(other.api, {...other.start, codeChallenge: routeBase64(new Uint8Array(32).fill(7))}, other.ports,
    {now: () => Date.parse('2030-01-02T03:04:07Z')}), /no longer matches/, 'another code challenge');
  assert.deepEqual(other.bound, []);
});

test('a successor attempt stops it before the request and before binding', async t => {
  const f = await fixture(t);
  f.replace();
  await assert.rejects(f.run(), /attempt changed/);
  assert.equal(f.sent.length, 0);
  // A public http address never sends.
  const api = new HttpLocalApi('http://8.8.8.8:32500', 'token', async () => {throw new Error('must not send');});
  await assert.rejects(requestSetupApproval(api, {...f.start, origin: 'http://8.8.8.8:32500'}, f.ports));
});
