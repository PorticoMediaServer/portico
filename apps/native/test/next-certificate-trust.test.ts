/** Synthetic signed proofs only. Final69 expectations replace retired Trust behavior.
 * No native authentication storage is used. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash, generateKeyPairSync, sign, verify} from 'node:crypto';
import {
  certificateOrigin, clearPendingCertificate, confirmIdentityCode, pendingCertificate,
  resetCertificateDecisions, settleCertificate, type CertificateIdentityContext,
  type CertificatePlatform, type PinnedCertificate,
} from '../src-next/platform/certificate-trust.ts';
import {bonjourProvesHome, type DiscoveryRoute} from '../../../packages/client-core/src/route-connection.ts';
import {identityCode, RouteError, type RouteCrypto} from '../../../packages/client-core/src/route-identity.ts';

const ORIGIN = 'https://media.example:32500';
const LEAF = Buffer.alloc(32, 1).toString('base64url');
const OTHER_LEAF = Buffer.alloc(32, 2).toString('base64url');
const away: CertificateIdentityContext = {home: false, knownServerIds: []};
const home: CertificateIdentityContext = {home: true, knownServerIds: []};
const bytes = (value: Uint8Array) => Buffer.from(value).toString('base64url');
const crypto: RouteCrypto = {
  random: n => new Uint8Array(n),
  sha256: async value => new Uint8Array(createHash('sha256').update(value).digest()),
  verify: async (key, signature, payload) => verify(null, payload, {
    key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), key]), format: 'der', type: 'spki',
  }, signature),
};
function identity() {
  const pair = generateKeyPairSync('ed25519');
  const publicKey = pair.publicKey.export({format: 'der', type: 'spki'}).subarray(-32);
  return {publicKey: bytes(publicKey), privateKey: pair.privateKey,
    serverId: 'srv_' + createHash('sha256').update('portico.server.identity.v1\0').update(publicKey).digest('base64url'),
    fingerprint: createHash('sha256').update(publicKey).digest('base64url')};
}
const SERVER = identity(), DIFFERENT = identity();
function fixture(options: {
  refused?: string; previous?: PinnedCertificate; server?: typeof SERVER; bindings?: readonly string[];
  version?: '1' | '2'; invalidSignature?: boolean; differentNonce?: boolean; observed?: string | null;
} = {}) {
  const pins: {origin: string; pin: PinnedCertificate}[] = [];
  const requests: {url: string; method: string; body: string}[] = [];
  const observations = new WeakMap<Response, string>();
  const platform: CertificatePlatform = {
    observedTLSLeaf: response => observations.get(response),
    refused: async () => options.refused ?? LEAF,
    pinned: async () => options.previous,
    pin: async (origin, pin) => {pins.push({origin, pin});},
    proofFetcher: (origin, observedLeaf) => async (input, init = {}) => {
      assert.equal(observedLeaf, options.refused ?? LEAF);
      assert.equal(input, origin + '/v1/networking/identity-proof');
      assert.equal(init.method, 'POST');
      assert.equal(init.credentials, 'omit');
      assert.equal(init.redirect, 'error');
      assert.equal(init.referrerPolicy, 'no-referrer');
      const headers = new Headers(init.headers);
      assert.equal(headers.get('Authorization'), null);
      assert.equal(headers.get('Cookie'), null);
      assert.equal(headers.get('Portico-Installation'), null);
      assert.equal(headers.get('Accept'), 'application/json; portico-route-proof=2');
      const body = String(init.body), asked = JSON.parse(body);
      assert.deepEqual(Object.keys(asked).sort(), ['baseUrl', 'nonce']);
      requests.push({url: String(input), method: init.method!, body});
      const who = options.server ?? SERVER, version = options.version ?? '2', now = Date.now();
      const payload = Buffer.from(JSON.stringify({kind: 'portico.route-proof', version, baseUrl: asked.baseUrl,
        nonce: options.differentNonce ? 'wrong' : asked.nonce, serverId: who.serverId, publicKey: who.publicKey,
        fingerprint: who.fingerprint, issuedAt: new Date(now).toISOString(), expiresAt: new Date(now + 15000).toISOString(),
        ...(version === '2' && options.bindings !== undefined ? {tlsCertificateFingerprints: options.bindings} : {})}));
      const signature = sign(null, payload, (options.invalidSignature ? DIFFERENT : who).privateKey);
      const response = new Response(JSON.stringify({payload: bytes(payload), signature: bytes(signature)}), {headers: {'Content-Type': 'application/json'}});
      if (options.observed !== null) observations.set(response, options.observed ?? observedLeaf);
      return response;
    },
  };
  return {platform, pins, requests};
}
const codeIs = (code: string) => (error: unknown) => error instanceof RouteError && error.code === code;

test('unknown own-certificate away is proof-only and waits for a separately supplied code', async () => {
  resetCertificateDecisions();
  const f = fixture({bindings: [LEAF]});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, away), codeIs('identity_code_needed'));
  const captured = pendingCertificate(ORIGIN)!;
  assert.equal(captured.serverId, SERVER.serverId);
  assert.equal(captured.fingerprint, LEAF);
  assert.deepEqual(f.pins, []);
  assert.equal(f.requests.length, 1);
  // This helper prepares the correct input, not an expected parser/code golden.
  // The independent published vector is asserted separately below.
  const correct = identityCode(SERVER.serverId);
  assert.equal(f.requests[0]!.body.includes(correct), false);
  assert.equal(await confirmIdentityCode(ORIGIN, 'IIII-IIII-IIII', f.platform, undefined, captured), false);
  assert.equal(pendingCertificate(ORIGIN), captured);
  assert.deepEqual(f.pins, []);
  assert.equal(await confirmIdentityCode(ORIGIN, correct.toLowerCase().replace(/-/g, ' '), f.platform, undefined, captured), true);
  assert.deepEqual(f.pins, [{origin: ORIGIN, pin: {fingerprint: LEAF, serverId: SERVER.serverId}}]);
  assert.equal(pendingCertificate(ORIGIN), undefined);
  assert.equal(f.requests.length, 1); // code comparison adds no request, including no system/name read
});

test('published independent code vector is unchanged', () => {
  assert.equal(identityCode('srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY'), 'JJWY-C8J8-J26F');
});

for (const version of ['1', '2'] as const) test(`an obsolete home flag cannot admit an unbound v${version} proof`, async () => {
  resetCertificateDecisions(); const f = fixture({version});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, home), codeIs('certificate_unverified'));
  assert.deepEqual(f.pins, []); assert.equal(pendingCertificate(ORIGIN), undefined);
});

for (const mode of ['matched', 'wrong TXT key hash', 'wrong TXT id', 'stale discovery', 'missing observed leaf', 'CGNAT'] as const) test(`canonical same-proof home conjunction: ${mode}`, async () => {
  resetCertificateDecisions();
  const origin = mode === 'CGNAT' ? 'https://100.120.50.24:32500' : 'https://192.168.1.25:32500';
  const f = fixture({bindings: [LEAF], ...(mode === 'missing observed leaf' ? {observed: null} : {})});
  const records: readonly DiscoveryRoute[] = [{baseUrl: origin, serverId: mode === 'wrong TXT id' ? DIFFERENT.serverId : SERVER.serverId,
    fingerprint: mode === 'wrong TXT key hash' ? DIFFERENT.fingerprint : SERVER.fingerprint, port: 32500, path: '/',
    expiresAt: Date.now() + (mode === 'stale discovery' ? -1 : 30000)}];
  const action = settleCertificate(origin, f.platform, crypto, async proof => ({home: bonjourProvesHome(origin, proof, LEAF, records), knownServerIds: []}));
  if (mode === 'matched') {await action; assert.equal(f.pins.length, 1);}
  else {
    await assert.rejects(action, codeIs(mode === 'missing observed leaf' ? 'certificate_unverified' : 'identity_code_needed'));
    assert.deepEqual(f.pins, []);
  }
});

for (const authority of ['expected', 'known elsewhere', 'previous pin'] as const) test(`away bound certificate uses ${authority} without a code`, async () => {
  resetCertificateDecisions();
  const f = fixture({bindings: [LEAF], ...(authority === 'previous pin' ? {previous: {serverId: SERVER.serverId, fingerprint: OTHER_LEAF}} : {})});
  const context = {...away, ...(authority === 'expected' ? {expected: SERVER.serverId} : {}),
    knownServerIds: authority === 'known elsewhere' ? [SERVER.serverId] : []};
  await settleCertificate(ORIGIN, f.platform, crypto, context);
  assert.equal(f.pins.length, 1); assert.equal(pendingCertificate(ORIGIN), undefined);
});

for (const bindings of [undefined, [OTHER_LEAF]]) test(`away unbound certificate is refused even for a known server: ${String(bindings)}`, async () => {
  resetCertificateDecisions(); const f = fixture({bindings});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, {...away, expected: SERVER.serverId}), codeIs('certificate_unverified'));
  assert.deepEqual(f.pins, []); assert.equal(pendingCertificate(ORIGIN), undefined);
});

for (const previous of [false, true]) test(`a different identity is blocked even at home, previous origin pin ${previous}`, async () => {
  resetCertificateDecisions(); const f = fixture({bindings: [LEAF], server: DIFFERENT,
    ...(previous ? {previous: {serverId: SERVER.serverId, fingerprint: LEAF}} : {})});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, {...home, expected: SERVER.serverId}), codeIs('identity_mismatch'));
  assert.deepEqual(f.pins, []); assert.equal(pendingCertificate(ORIGIN), undefined);
});

for (const bad of ['invalidSignature', 'differentNonce'] as const) test(`${bad} cannot grant home or pending-code authority`, async () => {
  resetCertificateDecisions(); const f = fixture({bindings: [LEAF], [bad]: true});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, home), codeIs('certificate_unverified'));
  assert.deepEqual(f.pins, []); assert.equal(pendingCertificate(ORIGIN), undefined);
});

test('no observed refused leaf cannot grant certificate authority', async () => {
  resetCertificateDecisions(); const f = fixture({bindings: [LEAF]}); f.platform.refused = async () => undefined;
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, away), codeIs('certificate_untrusted'));
  assert.equal(f.requests.length, 0); assert.deepEqual(f.pins, []);
});

test('a retired code screen cannot clear or confirm a newer pending proof at the same origin', async () => {
  resetCertificateDecisions(); const first = fixture({bindings: [LEAF]}), next = fixture({bindings: [LEAF], server: DIFFERENT});
  await assert.rejects(settleCertificate(ORIGIN, first.platform, crypto, away), codeIs('identity_code_needed'));
  const old = pendingCertificate(ORIGIN)!;
  await assert.rejects(settleCertificate(ORIGIN, next.platform, crypto, away), codeIs('identity_code_needed'));
  const current = pendingCertificate(ORIGIN)!;
  clearPendingCertificate(ORIGIN, old); assert.equal(pendingCertificate(ORIGIN), current);
  await assert.rejects(confirmIdentityCode(ORIGIN, identityCode(SERVER.serverId), first.platform, undefined, old), codeIs('cancelled'));
  assert.deepEqual(first.pins, []); assert.deepEqual(next.pins, []);
  clearPendingCertificate(ORIGIN, current); assert.equal(pendingCertificate(ORIGIN), undefined);
});

test('cancelled code input pins nothing and cannot revive a cleared decision', async () => {
  resetCertificateDecisions(); const f = fixture({bindings: [LEAF]});
  await assert.rejects(settleCertificate(ORIGIN, f.platform, crypto, away), codeIs('identity_code_needed'));
  const captured = pendingCertificate(ORIGIN)!, abort = new AbortController(); abort.abort();
  await assert.rejects(confirmIdentityCode(ORIGIN, identityCode(SERVER.serverId), f.platform, abort.signal, captured), codeIs('cancelled'));
  clearPendingCertificate(ORIGIN, captured);
  assert.equal(await confirmIdentityCode(ORIGIN, identityCode(SERVER.serverId), f.platform), false);
  assert.deepEqual(f.pins, []);
});

test('concurrent callers retain separate expected identities', async () => {
  resetCertificateDecisions(); const f = fixture({bindings: [LEAF]});
  const result = await Promise.allSettled([
    settleCertificate(ORIGIN, f.platform, crypto, {...away, expected: SERVER.serverId}),
    settleCertificate(ORIGIN, f.platform, crypto, {...away, expected: DIFFERENT.serverId}),
  ]);
  assert.equal(result[0]!.status, 'fulfilled'); assert.equal(result[1]!.status, 'rejected');
  assert.equal(f.requests.length, 2); assert.equal(f.pins.length, 1);
});

test('only HTTPS origins participate in certificate decisions', () => {
  assert.equal(certificateOrigin('https://Media.Example:32500/v1/system'), ORIGIN);
  assert.equal(certificateOrigin('http://192.168.1.20:32500/v1/system'), undefined);
  assert.equal(certificateOrigin('not a URL'), undefined);
});
