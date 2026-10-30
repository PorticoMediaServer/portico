import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, sign} from 'node:crypto';
import {transportJSON} from '../src/index.ts';
import {HttpLocalApi} from '../src/index.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {prepareSetupClaimHandoff, type SetupClaimHandoff} from '../src/setup-claim-client.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '../src/route-identity.ts';

// Same public interoperability seed as the canonical Go claim vector. These
// setup-only proofs exercise binding, not an installed grant.
const vector = JSON.parse(readFileSync(new URL('../../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const claim = {...JSON.parse(vector.claimPayload), purpose: 'setup'}, parent = JSON.parse(vector.startPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const signed = (v: unknown) => {const raw=transportJSON(v); return {payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), key).toString('base64url')};};
const start = {kind: 'portico.setup-start.v1', audience: claim.audience, claimHash: routeBase64(await webRouteCrypto.sha256(Buffer.from(transportJSON(claim)))), operationId: claim.operationId,
 serverId: claim.serverId, publicKey: claim.publicKey, localGeneration: claim.localGeneration, address: claim.address, purpose: 'setup',
 setupRequestId: '11111111-2222-4333-8444-555555555555', setupRevision: '2', state: parent.state, codeChallenge: parent.codeChallenge,
 installationId: parent.installationId, serverChallenge: parent.serverChallenge, issuedAt: parent.issuedAt, expiresAt: parent.expiresAt};
vector.claim=signed(claim); vector.start=signed(start);
async function fixture(t: TestContext) {
  setInstallationIdentity({installationId: start.installationId, name: 'Setup browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const expected = {operationId: claim.operationId, localGeneration: claim.localGeneration, revision: '2'};
  const binding = {pin: {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))},
    origin: claim.address, hostedOrigin: claim.audience, operationId: claim.operationId, localGeneration: claim.localGeneration,
    setupRequestId: start.setupRequestId, setupRevision: start.setupRevision, state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId};
  const code = 'claim_' + 'K'.repeat(43), transactionId = 'actual_setup_transaction';
  const approvalUrl = claim.audience + '/claim#' + new URLSearchParams({code, operation: claim.operationId, server: claim.name});
  const raw = {state: 'prepared', identity: {serverId: claim.serverId, localGeneration: claim.localGeneration}, operation: {...expected}, accountId: 'account-unbound',
    installationAcknowledged: false, approvalRequired: true, actions: ['cancel', 'approve'], approvalUrl, claimCode: code,
    setupContinuation: {transactionId, phase: 'prepared', expiresAt: start.expiresAt, claim: {...vector.claim}, start: {...vector.start}}};
  const sent: {url: string; init?: RequestInit; body: unknown}[] = [], bound: SetupClaimHandoff[] = [];
  let current = true, time = Date.parse('2030-01-02T03:04:07Z'), beforeAnswer = async () => {}, beforeBind = async () => {}, afterCheck = async () => {};
  const fetcher: typeof fetch = async (url, init) => {
    sent.push({url: String(url), init, body: JSON.parse(String(init?.body))});
    await beforeAnswer();
    return new Response(JSON.stringify(raw), {headers: {'Content-Type': 'application/json'}});
  };
  const api = new HttpLocalApi(binding.origin, 'captured-setup-bearer', fetcher);
  const route = {logicalOrigin: binding.origin, pin: binding.pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'})};
  api.useRoutes(route as never);
  const ports = {assertPendingCurrent: async () => {if (!current) throw new Error('pending changed'); await afterCheck();},
    bindPending: async (value: SetupClaimHandoff) => {await beforeBind(); if (!current) throw new Error('pending changed'); bound.push(value);}};
  return {api, route, expected, binding, ports, raw, sent, bound, approvalUrl, transactionId,
    start: (signal?: AbortSignal) => prepareSetupClaimHandoff(api, expected, binding, ports, {signal, now: () => time}),
    replace: () => {current = false;}, expire: () => {time = Date.parse(start.expiresAt);},
    beforeAnswer: (fn: () => Promise<void>) => {beforeAnswer = fn;}, beforeBind: (fn: () => Promise<void>) => {beforeBind = fn;},
    afterCheck: (fn: () => Promise<void>) => {afterCheck = fn;}};
}

test('initial setup web-approval carries the exact installation and commits verified full proofs before URL delivery', async t => {
  const f = await fixture(t);
  const url = await f.start();
  assert.equal(url, f.approvalUrl); assert.equal(f.sent.length, 1); assert.equal(f.bound.length, 1);
  assert.equal(f.sent[0].url, claim.address + '/v1/networking/claim/web-approval');
  assert.deepEqual(f.sent[0].body, {expected: f.expected, setupStart: {state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId}});
  const headers = new Headers(f.sent[0].init?.headers);
  assert.equal(headers.get('Authorization'), 'Bearer captured-setup-bearer');
  assert.equal(headers.get('X-Portico-Installation-Id'), start.installationId);
  assert.equal(f.sent[0].init?.redirect, 'error'); assert.equal(f.sent[0].init?.credentials, 'omit');
  assert.deepEqual(f.bound[0].claim, vector.claim); assert.deepEqual(f.bound[0].start, vector.start);
  assert.equal(f.bound[0].transactionId, f.transactionId); assert.equal(f.bound[0].expiresAt, start.expiresAt);
  assert.equal(Object.isFrozen(f.bound[0]), true); assert.equal(Object.isFrozen(f.bound[0].start), true);
  for (const privateValue of [start.state, start.codeChallenge, start.installationId, start.serverChallenge, vector.start.payload, vector.claim.signature]) assert.equal(url.includes(privateValue), false);
});

test('secure pending failure and network HTTP refuse before any request', async t => {
  const f = await fixture(t);
  f.replace(); await assert.rejects(f.start(), /pending changed/);
  const unavailable = {...f.ports, assertPendingCurrent: async () => {throw new Error('secure storage unavailable');}};
  await assert.rejects(prepareSetupClaimHandoff(f.api, f.expected, f.binding, unavailable), /secure storage unavailable/);
  // A public IP literal over http is refused even when the api is bound to that very origin.
  for (const origin of ['http://8.8.8.8:32500', 'http://[2001:db8::1]:32500', 'http://[::ffff:7f00:1]:32500']) {
    const api = new HttpLocalApi(origin, 'other-bearer', async () => {throw new Error('must not send');});
    await assert.rejects(prepareSetupClaimHandoff(api, f.expected, {...f.binding, origin}, f.ports));
  }
  // A LAN or DNS http address passes the address rule; this fixture has no route or signed proof for it, so it still stops before any request.
  for (const origin of ['http://192.168.1.20:32400', 'http://tower.local:32400'])
    await assert.rejects(prepareSetupClaimHandoff(new HttpLocalApi(origin, 'other-bearer', async () => {throw new Error('must not send');}), f.expected, {...f.binding, origin}, f.ports));
  assert.equal(f.sent.length, 0); assert.equal(f.bound.length, 0);
});

for (const kind of ['installation', 'route', 'counter', 'state', 'origin', 'setup-request', 'setup-revision'] as const) test('captured ' + kind + ' mismatch never starts a second operation', async t => {
  const f = await fixture(t);
  if (kind === 'installation') setInstallationIdentity(undefined);
  if (kind === 'route') f.route.pin = {...f.binding.pin, fingerprint: 'A'.repeat(43)};
  if (kind === 'counter') f.expected.revision = '9223372036854775808';
  if (kind === 'state') f.binding.state = 'not-a-canonical-state';
  if (kind === 'setup-request') f.binding.setupRequestId = 'invalid-request';
  if (kind === 'setup-revision') f.binding.setupRevision = '0';
  if (kind === 'origin') f.binding.origin = 'https://another-route.example.test';
  await assert.rejects(f.start()); assert.equal(f.sent.length, 0); assert.equal(f.bound.length, 0);
});

for (const kind of ['transaction', 'expiry', 'proof', 'registration', 'operation', 'account', 'phase', 'duplicate-url', 'foreign-url', 'name', 'code'] as const)
  test('untrusted ' + kind + ' response cannot bind protected state or navigate', async t => {
    const f = await fixture(t);
    if (kind === 'transaction') f.raw.setupContinuation.transactionId = '';
    if (kind === 'expiry') f.raw.setupContinuation.expiresAt = '2030-01-02T03:09:06Z';
    if (kind === 'proof') f.raw.setupContinuation.start.signature = f.raw.setupContinuation.claim.signature;
    if (kind === 'registration') Object.assign(f.raw.setupContinuation, {returnRegistration: {id: 'invented', origin: claim.address}});
    if (kind === 'operation') f.raw.operation.operationId = 'other-operation';
    if (kind === 'account') f.raw.accountId = 'another-account';
    if (kind === 'phase') f.raw.setupContinuation.phase = 'installed';
    if (kind === 'duplicate-url') f.raw.approvalUrl += '&code=' + f.raw.claimCode;
    if (kind === 'foreign-url') f.raw.approvalUrl = f.raw.approvalUrl.replace(claim.audience, 'https://another.test');
    if (kind === 'name') f.raw.approvalUrl = f.raw.approvalUrl.replace('Vector+server', 'Unsigned+name');
    if (kind === 'code') f.raw.claimCode = 'different';
    await assert.rejects(f.start()); assert.equal(f.bound.length, 0);
  });

test('a replacement while the HTTP response is in flight preserves the successor', async t => {
  const f = await fixture(t);
  f.beforeAnswer(async () => {f.replace();});
  await assert.rejects(f.start(), /pending changed/); assert.equal(f.sent.length, 1); assert.equal(f.bound.length, 0);
});
test('a failed atomic protected commit returns no URL and does not clear another attempt', async t => {
  const f = await fixture(t);
  f.beforeBind(async () => {f.replace();});
  await assert.rejects(f.start(), /pending changed/); assert.equal(f.bound.length, 0);
});
test('expiry during protected commit prevents handoff', async t => {
  const f = await fixture(t);
  f.beforeBind(async () => {f.expire();});
  await assert.rejects(f.start(), {code: 'invalid_return'}); assert.equal(f.bound.length, 1);
});
test('an abort after a valid producer response prevents protected binding', async t => {
  const f = await fixture(t), controller = new AbortController();
  f.beforeAnswer(async () => {controller.abort();});
  await assert.rejects(f.start(controller.signal)); assert.equal(f.bound.length, 0);
});
test('caller mutation after capture cannot retarget the original signed request', async t => {
  const f = await fixture(t);
  f.beforeAnswer(async () => {f.expected.operationId = 'successor'; f.binding.state = 'A'.repeat(43);});
  assert.equal(await f.start(), f.approvalUrl);
  assert.equal(f.bound[0].expected.operationId, claim.operationId); assert.equal(f.bound[0].binding.state, start.state);
});

// A slow certificate outlived the start: the same setup renews it once its claim installed.
test('a renewal accepts only the installed operation of a bound account', async t => {
  const installed = async () => {
    const f = await fixture(t);
    Object.assign(f.raw, {state: 'installed', installationAcknowledged: true, accountId: 'account-approved', approvalRequired: false, actions: ['cancel']});
    return f;
  };
  const renew = (f: Awaited<ReturnType<typeof installed>>) => prepareSetupClaimHandoff(f.api, f.expected, f.binding, f.ports, {now: () => Date.parse('2030-01-02T03:04:07Z'), renewal: true});
  const f = await installed();
  assert.equal(await renew(f), f.approvalUrl); assert.equal(f.bound.length, 1);
  // The ordinary path never takes an installed answer, and a renewal never takes anything else.
  const ordinary = await installed();
  await assert.rejects(ordinary.start()); assert.equal(ordinary.bound.length, 0);
  for (const change of [{accountId: 'account-unbound'}, {installationAcknowledged: false}, {state: 'prepared'}, {accountId: ''}]) {
    const g = await installed(); Object.assign(g.raw, change);
    await assert.rejects(renew(g)); assert.equal(g.bound.length, 0);
  }
});
