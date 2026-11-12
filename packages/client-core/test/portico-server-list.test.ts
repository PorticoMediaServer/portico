import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {generateKeyPairSync, sign, createHash, type KeyObject} from 'node:crypto';
import {PORTICO_ROOT_PUBLIC_KEY, openEnvelope, openServerList, readPorticoServerList, ServerListRefused, type Certificate} from '../src/portico-server-list.ts';
import {webRouteCrypto} from '../src/route-identity.ts';

// A Portico Account's server list is checked the way server-next/internal/hosted/trust checks it.
const b64url = (raw: Uint8Array | Buffer) => Buffer.from(raw).toString('base64url');
const raw = (key: KeyObject) => Buffer.from(key.export({format: 'jwk'}).x!, 'base64url');
const pair = () => { const {publicKey, privateKey} = generateKeyPairSync('ed25519'); return {publicKey, privateKey, raw: raw(publicKey)}; };
const now = Date.parse('2026-11-12T03:00:00Z');

test('the root built into the apps is trust/hosted-root-public-key.pem', () => {
  const pem = readFileSync(new URL('../../../trust/hosted-root-public-key.pem', import.meta.url), 'utf8');
  assert.equal(pem.replace(/-----[A-Z ]+-----/g, '').replace(/\s/g, ''), PORTICO_ROOT_PUBLIC_KEY);
});

test('an envelope Go signed for released servers opens here too', async () => {
  const vector = JSON.parse(readFileSync(new URL('../../../server-next/internal/hosted/trust/testdata/shared_vector.json', import.meta.url), 'utf8'));
  const root = Buffer.from(vector.rootPublic, 'base64url');
  const payload = await openEnvelope(vector.envelope, webRouteCrypto, Date.parse('2026-03-01T00:00:00Z'), [], root);
  assert.equal(new TextDecoder().decode(payload), '{"kind":"portico.trust.test-vector","value":1}');
  await assert.rejects(openEnvelope({...vector.envelope, payload: b64url(Buffer.from('{"kind":"portico.trust.test-vector","value":2}'))}, webRouteCrypto, Date.parse('2026-03-01T00:00:00Z'), [], root), ServerListRefused);
  await assert.rejects(openEnvelope(vector.envelope, webRouteCrypto, Date.parse('2026-07-01T00:00:00Z'), [], root), ServerListRefused, 'an expired certificate');
});

/** Services' side, as trust.Certify and trust.Sign write it. */
function signer(withdraws: (own: string) => string[] = () => []) {
  const root = pair(), working = pair();
  const keyId = createHash('sha256').update(working.raw).digest('hex').slice(0, 32);
  const unsigned = {keyId, publicKey: b64url(working.raw), purpose: 'documents', notBefore: '2026-11-01T00:00:00Z', notAfter: '2027-11-01T00:00:00Z', revision: 1, revoked: withdraws(keyId), signature: ''};
  const certificate: Certificate = {...unsigned, signature: b64url(sign(null, Buffer.from('portico.hosted.signing-certificate.v1\x00' + JSON.stringify(unsigned)), root.privateKey))};
  const envelope = (document: unknown) => { const payload = Buffer.from(JSON.stringify(document)); return {payload: b64url(payload), signature: b64url(sign(null, payload, working.privateKey)), keyId, certificate}; };
  return {root: root.raw, envelope, keyId};
}
const server = () => {
  const key = pair();
  const serverId = 'srv_' + b64url(createHash('sha256').update(Buffer.concat([Buffer.from('portico.server.identity.v1\x00'), key.raw])).digest());
  return {serverId, publicKey: b64url(key.raw), name: 'Living room', role: 'owner', owner: true, addresses: ['https://a1.ns.portico.direct:32400']};
};
const list = (servers: unknown[], version = 3, accountId = 'acct_1') => ({kind: 'portico.server-list', accountId, version, servers, issuedAt: '2026-11-12T02:00:00Z'});

test('a true list opens; another account\'s, an older one, a server whose id is not its key, and a withdrawn key are refused', async () => {
  const services = signer(), living = server();
  const opened = await openServerList(services.envelope(list([living])), 'acct_1', 2, webRouteCrypto, now, services.root);
  assert.equal(opened.servers[0].serverId, living.serverId);
  await assert.rejects(openServerList(services.envelope(list([living], 3, 'acct_2')), 'acct_1', 0, webRouteCrypto, now, services.root), ServerListRefused);
  await assert.rejects(openServerList(services.envelope(list([living], 3)), 'acct_1', 4, webRouteCrypto, now, services.root), ServerListRefused, 'older than the list held');
  await assert.rejects(openServerList(services.envelope(list([{...living, serverId: server().serverId}])), 'acct_1', 0, webRouteCrypto, now, services.root), ServerListRefused);
  await assert.rejects(openServerList(services.envelope(list([living])), 'acct_1', 0, webRouteCrypto, now, signer().root), ServerListRefused, 'signed under another root');
  // A certificate the root signed that withdraws its own key.
  const revokedOwn = signer(own => [own]);
  await assert.rejects(openServerList(revokedOwn.envelope(list([living])), 'acct_1', 0, webRouteCrypto, now, revokedOwn.root), ServerListRefused);
});

test('the apps read their list from GET /v1/servers with the access token, and get the servers to show', async () => {
  const services = signer(), living = server(), calls: string[] = [];
  const fetcher = (async (url: string, init: RequestInit) => {
    calls.push(`${init.method} ${new URL(url).pathname} ${(init.headers as Record<string, string>).Authorization}`);
    return {ok: true, status: 200, headers: {get: () => null}, text: async () => JSON.stringify({list: services.envelope(list([living])), listToken: 'lt_1', listVersion: 3})};
  }) as unknown as typeof fetch;
  const crypto = {...webRouteCrypto};
  // The apps' root is the production one; the stand-in's is the test's, so the check uses it.
  const verify = crypto.verify; crypto.verify = (key, signature, payload) => verify(Buffer.from(key).equals(Buffer.from(PORTICO_ROOT_PUBLIC_KEY, 'base64').subarray(-32)) ? services.root : key, signature, payload);
  const answer = await readPorticoServerList('https://services.test', 'pat_1', 'acct_1', crypto, {fetcher, now});
  assert.deepEqual(calls, ['GET /v1/servers Bearer pat_1']);
  assert.deepEqual(answer.items, [{id: living.serverId, name: 'Living room', baseUrl: 'https://a1.ns.portico.direct:32400', publicKey: living.publicKey, policyRevision: 0, role: 'owner', ownedByMe: true}]);
  assert.equal(answer.listToken, 'lt_1'); assert.equal(answer.version, 3);
});
