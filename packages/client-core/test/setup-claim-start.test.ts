import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, sign} from 'node:crypto';
import {transportJSON} from '../src/index.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '../src/route-identity.ts';
import {verifyOwnerClaimStart} from '../src/owner-claim-start.ts';
import {verifySetupClaimStart, type SetupStartExpectation} from '../src/setup-claim-start.ts';

// Public interoperability seed from the existing canonical Go fixture. New
// setup proofs are signed here; this is not an installed claim or a grant.
const vector = JSON.parse(readFileSync(new URL('../../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const parent = JSON.parse(vector.claimPayload), owner = JSON.parse(vector.startPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const now = Date.parse('2030-01-02T03:04:07Z');
const signedRaw = (raw: string) => ({payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), key).toString('base64url')});
const signed = (value: unknown) => signedRaw(transportJSON(value));
async function fixture() {
  const claim = {...parent, purpose: 'setup'};
  const start = {kind: 'portico.setup-start.v1', audience: claim.audience, claimHash: routeBase64(await webRouteCrypto.sha256(Buffer.from(transportJSON(claim)))), operationId: claim.operationId,
    serverId: claim.serverId, publicKey: claim.publicKey, localGeneration: claim.localGeneration, address: claim.address, purpose: 'setup',
    setupRequestId: '11111111-2222-4333-8444-555555555555', setupRevision: '2', state: owner.state, codeChallenge: owner.codeChallenge,
    installationId: owner.installationId, serverChallenge: owner.serverChallenge, issuedAt: owner.issuedAt, expiresAt: owner.expiresAt};
  const expected: SetupStartExpectation = {pin: {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))},
    origin: claim.address, hostedOrigin: claim.audience, operationId: claim.operationId, localGeneration: claim.localGeneration,
    setupRequestId: start.setupRequestId, setupRevision: start.setupRevision, state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId};
  return {claim, start, expected};
}

test('setup verifies distinct signed request/revision binding with no setup token or owner grant', async () => {
  const f = await fixture();
  const result = await verifySetupClaimStart(signed(f.claim), signed(f.start), f.expected, now);
  assert.deepEqual(result.start, f.start);
  assert.equal(Object.keys(result.start).length, 17);
  assert.equal(Object.isFrozen(result.start), true);
  assert.equal('setupToken' in result.start, false);
  assert.equal('session' in result, false);
});

test('setup and switch proof purposes never substitute for one another', async () => {
  const f = await fixture();
  await assert.rejects(verifySetupClaimStart(vector.claim, vector.start, f.expected, now));
  await assert.rejects(verifyOwnerClaimStart(signed(f.claim), signed(f.start), f.expected, now));
  await assert.rejects(verifySetupClaimStart(signed({...f.claim, purpose: 'switch'}), signed(f.start), f.expected, now));
});

test('setup checks the captured request, revision, pin, origin, state and installation', async () => {
  const f = await fixture();
  for (const changed of [{setupRequestId: '11111111-2222-4333-8444-555555555556'}, {setupRevision: '3'}, {setupRevision: '0'}, {setupRevision: '02'}, {state: 'x'.repeat(43)},
    {installationId: 'another-browser-installation-id-123456789'}, {origin: 'https://elsewhere.test'}, {hostedOrigin: 'https://another-hosted.test'}, {localGeneration: '9'}, {pin: {...f.expected.pin, fingerprint: 'x'.repeat(43)}}]) {
    await assert.rejects(verifySetupClaimStart(signed(f.claim), signed(f.start), {...f.expected, ...changed}, now));
  }
});

test('setup refuses noncanonical proof JSON, extra fields and base64 transport changes', async () => {
  const f = await fixture(), raw = transportJSON(f.start);
  for (const bad of [signedRaw(raw.replace('"kind":', '"kind":null,"kind":')), signedRaw(raw.replace('"purpose":"setup"', '"purpose":null')), signed({...f.start, setupToken: 'never permitted'}),
    signedRaw(raw.replace('{', '{ ')), {...signed(f.start), payload: signed(f.start).payload + '\n'}, {...signed(f.start), signature: signed(f.start).signature + '='}]) {
    await assert.rejects(verifySetupClaimStart(signed(f.claim), bad, f.expected, now));
  }
});

test('setup rejects expired, extended, future and old-parent signed attempts', async () => {
  const f = await fixture();
  for (const changed of [{expiresAt: '2030-01-02T03:04:06Z'}, {expiresAt: '2030-01-02T03:20:00Z'}, {issuedAt: '2030-01-02T03:06:00Z'}, {issuedAt: '2030-01-02T03:00:00Z'}]) {
    await assert.rejects(verifySetupClaimStart(signed(f.claim), signed({...f.start, ...changed}), f.expected, now));
  }
});
