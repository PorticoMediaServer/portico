import assert from 'node:assert/strict';
import test from 'node:test';
import {createSetupAttempt, preparedSetupOperation, setupApprovalCode, setupAttemptLive, setupContinuation, setupGrantSessionBound,
  setupHandoffBound, setupOwnerGrantValid, type SetupAttempt} from '../src/setup-claim-attempt.ts';
import type {ClaimStatus} from '../src/claim-onboarding.ts';
import type {SetupDraft} from '../src/setup-onboarding.ts';
import type {DirectSignIn} from '../src/profile-management.ts';
import {routeBase64} from '../src/route-identity.ts';

const pin = {serverId: 'server', publicKey: 'key', fingerprint: 'print'};
const now = 1_800_000_000_000;
const draft: SetupDraft = {version: 1, serverId: 'server', requestId: 'request', setupToken: 'token', name: 'Home', authMode: 'hosted'};
const crypto = {random: (n: number) => new Uint8Array(n).fill(7), sha256: async (value: Uint8Array) => new Uint8Array(32).fill(value.length % 251)};
const facts = {draft, selectionRevision: 4, origin: 'https://home.example', installationId: 'install', now};
const expected = {operationId: 'op', localGeneration: '3', revision: '5'};
const transactionId = 'txn', handoffExpires = new Date(now + 200_000).toISOString();

async function attempt(): Promise<SetupAttempt> {
  return createSetupAttempt({origin: facts.origin, hostedOrigin: 'https://account.example', pin, selectionRevision: 4, draft, setupRevision: '9',
    installationId: 'install', now, crypto});
}
function bound(record: SetupAttempt): SetupAttempt {
  const binding = {pin, origin: record.origin, hostedOrigin: record.hostedOrigin, operationId: 'op', localGeneration: '3', setupRequestId: 'request',
    setupRevision: '9', state: record.state, codeChallenge: record.codeChallenge, installationId: 'install'};
  return {...record, expected, expiresAt: new Date(now + 100_000).toISOString(), handoff: {expected, binding, transactionId, expiresAt: handoffExpires,
    claim: {payload: '', signature: ''}, start: {payload: '', signature: ''}}};
}
const status = (state: string, revision = '5'): ClaimStatus => ({state, identity: {serverId: 'server', localGeneration: '3'},
  operation: {operationId: 'op', localGeneration: '3', revision}, installationAcknowledged: true, approvalRequired: false, actions: []});
const installed = (extra: Record<string, unknown> = {}) => ({setupContinuation: {phase: 'installed', transactionId, expiresAt: handoffExpires, ...extra}});

test('a new attempt carries a 32-byte verifier, its challenge and a five-minute window', async () => {
  const record = await attempt();
  assert.equal(record.verifier, routeBase64(new Uint8Array(32).fill(7)));
  assert.equal(record.codeChallenge, routeBase64(new Uint8Array(32).fill(43)));
  assert.equal(Date.parse(record.expiresAt) - record.createdAt, 300_000);
  assert.equal(setupAttemptLive(record, facts), true);
});

test('drift in page, installation, selection, draft or time ends the attempt', async () => {
  const record = await attempt();
  for (const changed of [{origin: 'https://other.example'}, {installationId: 'other'}, {selectionRevision: 5}, {now: now + 300_000}, {now: now - 61_000},
    {draft: undefined}, {draft: {...draft, setupToken: 'other'}}, {draft: {...draft, requestId: 'other'}}, {draft: {...draft, authMode: 'local' as const}}])
    assert.equal(setupAttemptLive(record, {...facts, ...changed}), false, JSON.stringify(changed));
  assert.equal(setupAttemptLive({...record, version: 2 as 1}, facts), false);
  assert.equal(setupAttemptLive({...record, expiresAt: new Date(record.createdAt + 300_001).toISOString()}, facts), false, 'no window past five minutes');
  assert.equal(setupAttemptLive({...record, retired: true}, {...facts, draft: undefined}), true, 'only a retired tombstone outlives its draft');
});

test('a prepare answer is admitted only as a fresh setup approval', () => {
  const approval = {operationId: 'op', serverId: 'server', publicKey: 'key', localGeneration: '3', name: 'Home', expectedRevision: '5', purpose: 'setup' as const,
    currentOwnerAccountId: '', previousOwnerRole: '', returnRegistrationId: ''};
  assert.deepEqual(preparedSetupOperation({...status('prepared'), approvalRequest: approval}), expected);
  for (const change of [{purpose: 'switch' as const}, {currentOwnerAccountId: 'a'}, {previousOwnerRole: 'owner'}, {returnRegistrationId: 'ret'}])
    assert.equal(preparedSetupOperation({...status('prepared'), approvalRequest: {...approval, ...change}}), undefined);
  assert.equal(preparedSetupOperation({...status('installed'), approvalRequest: approval}), undefined);
});

test('a stored handoff must bind exactly this attempt and operation', async () => {
  const record = bound(await attempt());
  assert.equal(setupHandoffBound(record), true);
  const binding = record.handoff!.binding;
  for (const change of [{state: 'x'}, {codeChallenge: 'x'}, {installationId: 'x'}, {operationId: 'x'}, {localGeneration: '4'}, {setupRevision: '10'},
    {origin: 'https://x.example'}, {pin: {...pin, publicKey: 'other'}}])
    assert.equal(setupHandoffBound({...record, handoff: {...record.handoff!, binding: {...binding, ...change}}}), false, JSON.stringify(change));
  assert.equal(setupHandoffBound({...record, expiresAt: new Date(now + 300_000).toISOString()}), false, 'the attempt cannot outlive its handoff');
  assert.equal(setupHandoffBound({...record, handoff: undefined}), false);
});

test('only an exact installed continuation (no registration) completes admission; prepared and cancelled grant nothing', async () => {
  const record = bound(await attempt()) as Parameters<typeof setupContinuation>[2];
  assert.deepEqual(setupContinuation(installed(), status('installed'), record), {phase: 'installed'});
  assert.deepEqual(setupContinuation({setupContinuation: {phase: 'prepared', transactionId, expiresAt: handoffExpires}}, status('installed'), record), {phase: 'pending'});
  assert.deepEqual(setupContinuation({setupContinuation: {phase: 'cancelled', transactionId, expiresAt: handoffExpires}}, status('cancelled'), record), {phase: 'cancelled'});
  assert.deepEqual(setupContinuation(installed(), status('prepared'), record), {phase: 'pending'});
  for (const raw of [installed({extra: 1}), installed({transactionId: 'other'}), installed({expiresAt: new Date(now).toISOString()}),
    installed({returnRegistration: {id: 'ret_' + 'a'.repeat(43), origin: 'https://home.example'}})])
    assert.deepEqual(setupContinuation(raw, status('installed'), record), {phase: 'invalid'}, JSON.stringify(raw));
  assert.deepEqual(setupContinuation(installed(), status('installed', '4'), record), {phase: 'invalid'}, 'an older operation revision');
  assert.deepEqual(setupContinuation(installed(), {...status('installed'), identity: {serverId: 'server', localGeneration: '4'}}, record), {phase: 'invalid'});
});

test('the final grant must be this server owner on its primary profile, with nothing left to set up', async () => {
  const record = await attempt();
  const grant = {kind: 'signed-in', accountScoped: false, passwordChangeRequired: false,
    snapshot: {serverId: 'server', twoStepSetupRequired: false, account: {role: 'owner', disabled: false, hostedAccountId: 'hosted', primaryProfileId: 'main'}},
    session: {installationId: 'install', expiresAt: new Date(now + 60_000).toISOString(), serverIdentity: {publicKey: 'key', fingerprint: 'print'},
      viewer: {role: 'owner', serverId: 'server', profileId: 'main'}}} as unknown as Extract<DirectSignIn, {kind: 'signed-in'}>;
  assert.equal(setupOwnerGrantValid(grant, record), true);
  assert.equal(setupGrantSessionBound(grant, record, now), true);
  const account = grant.snapshot.account;
  for (const changed of [{snapshot: {...grant.snapshot, serverId: 'other'}}, {snapshot: {...grant.snapshot, account: {...account, role: 'member'}}},
    {snapshot: {...grant.snapshot, account: {...account, disabled: true}}}, {snapshot: {...grant.snapshot, account: {...account, hostedAccountId: ''}}},
    {snapshot: {...grant.snapshot, twoStepSetupRequired: true}}, {passwordChangeRequired: true}, {resetFileOwnerSession: true},
    {session: {...grant.session, viewer: {...grant.session.viewer, profileId: 'child'}}}])
    assert.equal(setupOwnerGrantValid({...grant, ...changed} as typeof grant, record), false, JSON.stringify(changed));
  for (const session of [{installationId: 'other'}, {expiresAt: new Date(now).toISOString()}, {serverIdentity: {publicKey: 'other', fingerprint: 'print'}},
    {viewer: {...grant.session.viewer, role: 'member'}}])
    assert.equal(setupGrantSessionBound({...grant, session: {...grant.session, ...session}} as typeof grant, record, now), false, JSON.stringify(session));
});

test('an approval URL must be the hosted claim page for this operation and server', () => {
  const code = 'claim_' + 'A'.repeat(43), good = `https://account.example/claim#code=${code}&operation=op&server=Home`;
  const expect = {hostedOrigin: 'https://account.example', operationId: 'op', serverName: 'Home'};
  assert.equal(setupApprovalCode(good, expect), code);
  for (const url of [good.replace('/claim', '/other'), good.replace('#', '?x=1#'), good.replace('operation=op', 'operation=other'),
    good.replace('server=Home', 'server=Away'), good + '&extra=1', good.replace('code=claim_', 'code=other_'), 'https://evil.example/claim#' + good.split('#')[1]])
    assert.equal(setupApprovalCode(url, expect), undefined, url);
  assert.throws(() => setupApprovalCode(good.replace(code, 'claim_short'), expect));
});
