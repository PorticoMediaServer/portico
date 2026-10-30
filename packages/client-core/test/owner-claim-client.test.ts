import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi} from '../src/index.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {prepareOwnerClaimHandoff, type OwnerClaimHandoff} from '../src/owner-claim-client.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '../src/route-identity.ts';

// Actual canonical Go producer vector, not a new hand-written signed protocol.
const vector = JSON.parse(readFileSync(new URL('../../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const claim = JSON.parse(vector.claimPayload), start = JSON.parse(vector.startPayload);
async function fixture(t: TestContext) {
  setInstallationIdentity({installationId: start.installationId, name: 'Owner browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const expected = {operationId: claim.operationId, localGeneration: claim.localGeneration, revision: '2'};
  const binding = {pin: {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))},
    origin: claim.address, hostedOrigin: claim.audience, operationId: claim.operationId, localGeneration: claim.localGeneration,
    state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId};
  const code = 'claim_' + 'K'.repeat(43), transactionId = 'actual_owner_transaction';
  const approvalUrl = claim.audience + '/claim#' + new URLSearchParams({code, operation: claim.operationId, server: claim.name});
  const raw = {state: 'prepared', identity: {serverId: claim.serverId, localGeneration: claim.localGeneration}, operation: {...expected}, accountId: 'account-unbound',
    installationAcknowledged: false, approvalRequired: true, actions: ['cancel', 'approve'], approvalUrl, claimCode: code,
    ownerContinuation: {transactionId, phase: 'prepared', expiresAt: start.expiresAt, claim: {...vector.claim}, start: {...vector.start}}};
  const sent: {url: string; init?: RequestInit; body: unknown}[] = [], bound: OwnerClaimHandoff[] = [];
  let current = true, time = Date.parse('2030-01-02T03:04:07Z'), beforeAnswer = async () => {}, beforeBind = async () => {}, afterCheck = async () => {};
  const fetcher: typeof fetch = async (url, init) => {
    sent.push({url: String(url), init, body: JSON.parse(String(init?.body))});
    await beforeAnswer();
    return new Response(JSON.stringify(raw), {headers: {'Content-Type': 'application/json'}});
  };
  const api = new HttpLocalApi(binding.origin, 'captured-owner-bearer', fetcher);
  const route = {logicalOrigin: binding.origin, pin: binding.pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'})};
  api.useRoutes(route as never);
  const ports = {assertPendingCurrent: async () => {if (!current) throw new Error('pending changed'); await afterCheck();},
    bindPending: async (value: OwnerClaimHandoff) => {await beforeBind(); if (!current) throw new Error('pending changed'); bound.push(value);}};
  return {api, route, expected, binding, ports, raw, sent, bound, approvalUrl, transactionId,
    start: (signal?: AbortSignal) => prepareOwnerClaimHandoff(api, expected, binding, ports, {signal, now: () => time}),
    replace: () => {current = false;}, expire: () => {time = Date.parse(start.expiresAt);},
    beforeAnswer: (fn: () => Promise<void>) => {beforeAnswer = fn;}, beforeBind: (fn: () => Promise<void>) => {beforeBind = fn;},
    afterCheck: (fn: () => Promise<void>) => {afterCheck = fn;}};
}

test('existing owner web-approval carries the exact installation and commits verified full proofs before URL delivery', async t => {
  const f = await fixture(t);
  const url = await f.start();
  assert.equal(url, f.approvalUrl); assert.equal(f.sent.length, 1); assert.equal(f.bound.length, 1);
  assert.equal(f.sent[0].url, claim.address + '/v1/networking/claim/web-approval');
  assert.deepEqual(f.sent[0].body, {expected: f.expected, ownerStart: {state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId}});
  const headers = new Headers(f.sent[0].init?.headers);
  assert.equal(headers.get('Authorization'), 'Bearer captured-owner-bearer');
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
  await assert.rejects(prepareOwnerClaimHandoff(f.api, f.expected, f.binding, unavailable), /secure storage unavailable/);
  for (const origin of ['http://server.example.test', 'http://127.0.0.1:32500', 'http://localhost.attacker.test'])
    await assert.rejects(prepareOwnerClaimHandoff(f.api, f.expected, {...f.binding, origin}, f.ports));
  assert.equal(f.sent.length, 0); assert.equal(f.bound.length, 0);
});

for (const kind of ['installation', 'route', 'counter', 'state', 'origin'] as const) test('captured ' + kind + ' mismatch never starts a second operation', async t => {
  const f = await fixture(t);
  if (kind === 'installation') setInstallationIdentity(undefined);
  if (kind === 'route') f.route.pin = {...f.binding.pin, fingerprint: 'A'.repeat(43)};
  if (kind === 'counter') f.expected.revision = '9223372036854775808';
  if (kind === 'state') f.binding.state = 'not-a-canonical-state';
  if (kind === 'origin') f.binding.origin = 'https://another-route.example.test';
  await assert.rejects(f.start()); assert.equal(f.sent.length, 0); assert.equal(f.bound.length, 0);
});

for (const kind of ['transaction', 'expiry', 'proof', 'registration', 'operation', 'account', 'phase', 'duplicate-url', 'foreign-url', 'name', 'code'] as const)
  test('untrusted ' + kind + ' response cannot bind protected state or navigate', async t => {
    const f = await fixture(t);
    if (kind === 'transaction') f.raw.ownerContinuation.transactionId = '';
    if (kind === 'expiry') f.raw.ownerContinuation.expiresAt = '2030-01-02T03:09:06Z';
    if (kind === 'proof') f.raw.ownerContinuation.start.signature = f.raw.ownerContinuation.claim.signature;
    if (kind === 'registration') Object.assign(f.raw.ownerContinuation, {returnRegistration: {id: 'invented', origin: claim.address}});
    if (kind === 'operation') f.raw.operation.operationId = 'other-operation';
    if (kind === 'account') f.raw.accountId = 'another-account';
    if (kind === 'phase') f.raw.ownerContinuation.phase = 'installed';
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
