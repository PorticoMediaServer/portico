import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createHash, generateKeyPairSync, sign} from 'node:crypto';
import {buildHostedSignInStartURL, parseHostedSignInStartURL, verifyHostedSignInStart} from '../src/hosted-sign-in-start.ts';
import {routeBase64} from '../src/route-identity.ts';
import type {RegisteredSignInWithContext} from '../src/same-tab-return.ts';

const now = Date.UTC(2030, 0, 1), hosted = 'https://web.example.test';
const token = (byte: number) => routeBase64(new Uint8Array(32).fill(byte));
const keys = generateKeyPairSync('ed25519'), key = keys.publicKey.export({format: 'der', type: 'spki'}).subarray(-32);
const serverId = 'srv_' + createHash('sha256').update('portico.server.identity.v1\0').update(key).digest('base64url');
const stamp = (at: number) => new Date(at).toISOString().replace('.000Z', 'Z');
// A prepare as the signing Server issues it: the Server stamp and the challenge
// it answers own the window, while this browser's clock may still trail it.
const value = (serverNow = now, life = 60_000): RegisteredSignInWithContext => {
  const facts = {kind: 'portico.registered-sign-in-prepare', version: '1', serverId, publicKey: key.toString('base64url'),
    fingerprint: createHash('sha256').update(key).digest('base64url'), serverName: 'Actual server', registrationId: 'ret_' + token(2),
    registrationOrigin: 'https://server.example.test', hostedOrigin: hosted, claimOperationId: 'installed_operation-1', challenge: token(5),
    nonce: token(6), installationId: '10000000-0000-4000-8000-000000000001', issuedAt: stamp(serverNow),
    expiresAt: stamp(serverNow + life)};
  const raw = Buffer.from(JSON.stringify(facts));
  return {start: {serverId, purpose: 'sign-in', claimOperationId: facts.claimOperationId, registrationId: facts.registrationId,
    state: token(3), codeChallenge: token(4), serverChallenge: facts.challenge, installationId: facts.installationId},
    registration: {id: facts.registrationId, origin: facts.registrationOrigin}, hostedOrigin: hosted, expiresAt: facts.expiresAt,
    context: {payload: raw.toString('base64url'), signature: sign(null, raw, keys.privateKey).toString('base64url')}};
};
const encode = (json: string) => hosted + '/#signIn=v1.' + routeBase64(new TextEncoder().encode(json));

test('the genuine installed operation survives a canonical ordinary HTTPS-root handoff', () => {
  const input = value(), href = buildHostedSignInStartURL(input, now), parsed = parseHostedSignInStartURL(href, hosted, now);
  assert.deepEqual(parsed, input); assert.equal(parsed?.start.claimOperationId, 'installed_operation-1');
  assert(Object.isFrozen(parsed) && Object.isFrozen(parsed.start) && Object.isFrozen(parsed.registration));
  assert.equal(parseHostedSignInStartURL(href, 'https://other.example.test', now), undefined);
});
test('the builder rejects every protected or unknown field at all three object boundaries', () => {
  for (const key of ['verifier', 'moveTicket', 'moveCode', 'credentials', 'ownerTransactionId', 'pinReset', 'expectedRevision']) {
    for (const part of ['root', 'start', 'registration']) {
      const input = value() as unknown as Record<string, any>;
      (part === 'root' ? input : input[part])[key] = 'must-never-travel';
      assert.throws(() => buildHostedSignInStartURL(input as unknown as RegisteredSignInWithContext, now));
    }
  }
  const input = value(); let called = false;
  Object.defineProperty(input.start, 'state', {enumerable: true, get() { called = true; return token(3); }});
  assert.throws(() => buildHostedSignInStartURL(input, now)); assert.equal(called, false);
});
test('ordinary purpose, actual operation, registration binding, token encodings and original lifetime are mandatory', () => {
  const changes = [
    (v: any) => { v.start.purpose = 'switch'; }, (v: any) => { v.start.claimOperationId = ''; },
    (v: any) => { v.registration.id = 'ret_' + token(9); }, (v: any) => { v.start.state += '='; },
    (v: any) => { v.start.codeChallenge = 'a'.repeat(42); }, (v: any) => { v.start.installationId = 'short'; },
    (v: any) => { v.hostedOrigin = 'http://127.0.0.1:19410'; }, (v: any) => { v.registration.origin += '/'; },
    (v: any) => { v.registration.origin = 'https://user:pass@server.example.test'; },
    (v: any) => { v.expiresAt = new Date(now).toISOString(); },
    // The handoff envelope carries only the producer's expiry, so its ceiling is
    // the widest an authentic issuance can reach; the signed facts (below) still
    // refuse any real lifetime beyond one producer window.
    (v: any) => { v.expiresAt = new Date(now + 300_000 + 60_000 + 1).toISOString(); },
  ];
  for (const change of changes) { const input = value(); change(input); assert.throws(() => buildHostedSignInStartURL(input, now)); }
  const href = buildHostedSignInStartURL(value(), now);
  assert.equal(parseHostedSignInStartURL(href, hosted, now + 60_000), undefined);
});
test('the parser rejects duplicate/reordered/extra JSON, noncanonical base64, UTF8 and URL spellings', () => {
  const json = JSON.stringify(value()), href = encode(json);
  const invalid = [
    encode(' ' + json), encode(json.replace('"start":', '"unexpected":0,"start":')),
    encode(json.replace('"purpose":"sign-in"', '"purpose":"switch","purpose":"sign-in"')),
    encode(JSON.stringify({...value(), start: {...value().start, verifier: 'never'}})),
    encode(JSON.stringify({expiresAt: value().expiresAt, hostedOrigin: hosted, registration: value().registration, start: value().start})),
    href + '=', href + '&other=1', href.replace('/#', '/account#'), href.replace('/#', '/?query=1#'),
    href.replace('https://web.', 'https://WEB.'), hosted + '/#signIn=v1.' + routeBase64(new Uint8Array([0xc3, 0x28])),
    hosted + '/#signIn=v1.' + 'A'.repeat(8192),
  ];
  for (const candidate of invalid) assert.equal(parseHostedSignInStartURL(candidate, hosted, now), undefined);
});

test('Hosted verifies the original signed name and every wrapper binding before either auth5 state', async () => {
  assert.equal((await verifyHostedSignInStart(value(), now)).serverName, 'Actual server');
  for (const mutate of [
    (v: any) => {v.start.serverId = 'srv_' + token(9);},
    (v: any) => {v.start.claimOperationId = 'another_operation';},
    (v: any) => {v.start.serverChallenge = token(9);},
    (v: any) => {v.start.installationId = '20000000-0000-4000-8000-000000000001';},
    (v: any) => {v.registration.id = v.start.registrationId = 'ret_' + token(9);},
    (v: any) => {v.registration.origin = 'https://different.example';},
    (v: any) => {v.hostedOrigin = 'https://different.example';},
    (v: any) => {v.expiresAt = new Date(now + 59_000).toISOString();},
    (v: any) => {v.context.signature = routeBase64(new Uint8Array(64).fill(1));},
    (v: any) => {const facts = JSON.parse(Buffer.from(v.context.payload, 'base64url').toString()); facts.serverName = 'Forged label'; v.context.payload = Buffer.from(JSON.stringify(facts)).toString('base64url');},
  ]) {const input = value(); mutate(input); await assert.rejects(verifyHostedSignInStart(input, now));}
  await assert.rejects(verifyHostedSignInStart(value(), now + 60_000));
  const missing = value() as any; delete missing.context;
  assert.throws(() => buildHostedSignInStartURL(missing, now));
});

/** QA7ea: a real Chromium prepare answered while this browser's clock still
 * trailed the signing Server. `checked` compared the producer's expiry with the
 * browser clock and threw TypeError even though the signed context was genuine. */
test('a browser clock behind the signing Server keeps the genuine handoff and signed proof', async () => {
  for (const behind of [1, 1_000, 59_999, 60_000]) {
    const serverNow = now + behind, input = value(serverNow, 300_000);
    const href = buildHostedSignInStartURL(input, now);
    assert.deepEqual(parseHostedSignInStartURL(href, hosted, now), input, String(behind));
    assert.equal((await verifyHostedSignInStart(input, now)).handoff.expiresAt, input.expiresAt, String(behind));
  }
});

test('one millisecond past the skew, a reached expiry and an overlong issuance are still refused', async () => {
  const beyond = value(now + 60_000 + 1, 300_000);
  assert.throws(() => buildHostedSignInStartURL(beyond, now), TypeError);
  await assert.rejects(verifyHostedSignInStart(beyond, now));
  assert.equal(parseHostedSignInStartURL(encode(JSON.stringify(beyond)), hosted, now), undefined);
  for (const expired of [value(now - 300_000), value(now - 600_000, 300_000)]) {
    assert.throws(() => buildHostedSignInStartURL(expired, now), TypeError);
    await assert.rejects(verifyHostedSignInStart(expired, now));
  }
  // A genuine but short issuance is refused once this browser reaches its expiry.
  const short = value(now + 5_000, 1_000);
  await verifyHostedSignInStart(short, now);
  await assert.rejects(verifyHostedSignInStart(short, now + 6_000));
  // The signature, not the envelope, owns the producer lifetime: an issuance
  // signed for longer than one lifetime never verifies at any skew.
  for (const serverNow of [now, now + 60_000]) {
    await assert.rejects(verifyHostedSignInStart(value(serverNow, 300_000 + 1), now));
  }
});

test('the bounded checkpoint ceiling is never a grant: an unsigned stretched envelope expiry is refused', async () => {
  const input = value(now + 5_000, 300_000);
  // Shape and signature-free checkpoints may carry the widest expiry an
  // authentic issuance can reach; Hosted still refuses it before any auth state.
  const stretched = {...input, expiresAt: stamp(now + 300_000 + 60_000)};
  assert.equal(parseHostedSignInStartURL(buildHostedSignInStartURL(stretched, now), hosted, now)?.expiresAt, stretched.expiresAt);
  await assert.rejects(verifyHostedSignInStart(stretched, now));
});
