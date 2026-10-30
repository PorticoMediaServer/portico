import assert from 'node:assert/strict';
import test from 'node:test';
import {createHash, generateKeyPairSync, sign} from 'node:crypto';
import {restoreExactNativeSession, type ServerConnectionStorage} from '../src/server-connections.ts';
import {parseRememberedServer} from '../src/remembered-server.ts';

// ACC-LEAF-21: the exact saved-session restore boundaries, authored UNRUN.
// Every vector drives the real restore: refusals prove no IO or write, and the
// success and 401 vectors use a real Ed25519 pin and a genuinely signed route
// proof. One fixed capture time is used throughout, and every outgoing request is
// recorded so a renewal attempt cannot hide behind an unexpected throw.

// One real capture at load time: every timestamp below is derived from it, so
// nothing here is already expired when the file runs later.
const captured = Date.now();
const at = (offset: number) => new Date(captured + offset).toISOString().replace(/\.\d{3}Z$/, 'Z');
const token = 't'.repeat(43);
const state = {
  accessToken: token,
  expiresAt: at(3600000),
  viewer: {accountId: 'acct', profileId: 'profile', serverId: 'srv-1', authority: 'local', role: 'owner'},
  sessionFamilyId: 'family-1',
  tokenGeneration: '2',
  authorizationHorizon: at(86400000),
};
const record = (over: Record<string, unknown> = {}) => parseRememberedServer({
  version: '1', logicalOrigin: 'https://server.example',
  session: {...state, ...(over.session as object ?? {})},
  pin: {serverId: 'srv-1', publicKey: 'A'.repeat(43), fingerprint: 'B'.repeat(43)},
  routes: {paired: ['https://server.example']},
  ...Object.fromEntries(Object.entries(over).filter(([key]) => key !== 'session')),
})!;

function harness(saved: ReturnType<typeof record> | undefined) {
  const writes: unknown[] = [];
  const storage: ServerConnectionStorage = {
    read: async () => saved,
    change: async update => {writes.push(update(saved));},
  };
  let fetched = 0;
  const fetcher = (async () => {fetched++; throw new Error('network must not be used');}) as unknown as typeof fetch;
  return {env: {storage, fetcher}, writes, get fetched() {return fetched;}};
}

// The caller's copy starts from the plain record: only the explicit success and
// 401 vectors pass liveSession, so a negative cannot pass for the wrong reason.
const expected = (over: Partial<typeof state> = {}) => ({...state, ...over}) as typeof state;

test('a newer same-family generation or a different token for the same generation is refused before IO', async () => {
  for (const original of [
    expected({tokenGeneration: '1'}),
    expected({accessToken: 'u'.repeat(43)}),
  ]) {
    const h = harness(record());
    assert.equal(await restoreExactNativeSession(h.env, original), undefined);
    assert.equal(h.fetched, 0);
    assert.equal(h.writes.length, 0);
  }
});

test('pendingRefresh, an expired copy and a different family or viewer are refused before IO', async () => {
  const variants: [ReturnType<typeof record>, ReturnType<typeof expected>][] = [
    [{...record(), pendingRefresh: 'n'.repeat(32)} as ReturnType<typeof record>, expected()],
    [record({session: {expiresAt: '2000-01-01T00:00:00Z'}}), expected()],
    [record(), expected({sessionFamilyId: 'family-2'})],
    [record(), expected({viewer: {...state.viewer, serverId: 'srv-2'}})],
    [record(), expected({viewer: {...state.viewer, profileId: 'another'}})],
    [record(), expected({viewer: {...state.viewer, authority: 'hosted'}})],
    [record(), expected({viewer: {...state.viewer, role: 'member'}})],
  ];
  for (const [saved, original] of variants) {
    const h = harness(saved);
    assert.equal(await restoreExactNativeSession(h.env, original), undefined);
    assert.equal(h.fetched, 0);
    assert.equal(h.writes.length, 0);
  }
});

test('an unchanged unexpired copy is admitted to the identity check without refresh or a write', async () => {
  const h = harness(record());
  // The saved pin is not a real Ed25519 identity, so admission ends at that check
  // rather than at the exact-copy gate. Reaching it proves the copy passed.
  await assert.rejects(restoreExactNativeSession(h.env, expected()), /Invalid identity encoding|identity/i);
  assert.equal(h.writes.length, 0);
  assert.equal(h.fetched, 0);
});

// The remaining vectors need a real pinned identity and a real signed probe.
// Both are produced here with Node's Ed25519 exactly as a server would; nothing
// is stubbed to always verify.
const keys = generateKeyPairSync('ed25519');
const rawKey = keys.publicKey.export({format: 'der', type: 'spki'}).subarray(-32);
const b64 = (value: Buffer | Uint8Array) => Buffer.from(value).toString('base64url');
const publicKey = b64(rawKey), fingerprint = b64(createHash('sha256').update(rawKey).digest());
const serverId = 'srv_' + b64(createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest());
const stamp = at;
const pin = {serverId, publicKey, fingerprint};
// The saved viewer and the caller's copy must both name the pinned server.
const liveSession = {...state, expiresAt: at(3600000), viewer: {...state.viewer, serverId}};
const live = () => parseRememberedServer({
  version: '1', logicalOrigin: 'https://server.example',
  session: liveSession,
  pin, routes: {paired: ['https://server.example']},
})!;
function liveEnv() {
  const writes: unknown[] = [];
  const calls: string[] = [];
  const storage: ServerConnectionStorage = {
    read: async () => live(),
    change: async update => {writes.push(update(live()));},
  };
  const fetcher = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input); const method = init?.method ?? 'GET';
    calls.push(method + ' ' + new URL(url).pathname);
    if (url.endsWith('/v1/networking/identity-proof') && method === 'POST') {
      const raw = Buffer.from(JSON.stringify({kind: 'portico.route-proof', version: '1', baseUrl: 'https://server.example',
        nonce: JSON.parse(String(init?.body ?? '{}')).nonce, serverId, publicKey, fingerprint,
        issuedAt: at(-1000), expiresAt: at(20000)}));
      return new Response(JSON.stringify({payload: b64(raw), signature: b64(sign(null, raw, keys.privateKey))}), {status: 200, headers: {'Content-Type': 'application/json'}});
    }
    if (url.endsWith('/v1/me') && method === 'GET') return new Response(JSON.stringify({viewer: {...liveSession.viewer}}), {status: 200, headers: {'Content-Type': 'application/json'}});
    throw new Error('unexpected ' + method + ' ' + url);
  }) as typeof fetch;
  return {env: {storage, fetcher}, writes, calls};
}

test('an unchanged unexpired copy restores with a signed probe and never refreshes or writes', async () => {
  const h = liveEnv();
  const restored = await restoreExactNativeSession(h.env, liveSession);
  assert.ok(restored);
  assert.equal(restored!.session.accessToken, token);
  assert.equal(h.writes.length, 0);
  assert.equal(h.calls.some(call => call.includes('/auth/refresh')), false);
  assert.equal(h.calls.includes('POST /v1/networking/identity-proof'), true);
  assert.equal(h.calls.includes('GET /v1/me'), true);
});

test('an actual 401 on the exact copy throws as session-ended, with no renewal and no Hosted route', async () => {
  const h = liveEnv();
  const calls: string[] = [];
  const refusing = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input), method = init?.method ?? 'GET', path = new URL(url).pathname;
    calls.push(method + ' ' + path);
    if (path === '/v1/networking/identity-proof') return h.env.fetcher!(input, init);
    if (path === '/v1/me') return new Response(JSON.stringify({error: {code: 'session_ended'}}), {status: 401, headers: {'Content-Type': 'application/json'}});
    return new Response(JSON.stringify({error: {code: 'unexpected'}}), {status: 500, headers: {'Content-Type': 'application/json'}});
  }) as typeof fetch;
  const failure = await restoreExactNativeSession({...h.env, fetcher: refusing}, liveSession).then(
    () => {throw new Error('a 401 restored a session');},
    (error: unknown) => error as {code?: string; status?: number});
  // The exact path reports the server's own refusal shape (ApiError) and neither
  // renews nor retries.
  assert.equal(failure.status, 401);
  assert.equal(failure.code, 'session_ended');
  assert.equal(calls.includes('GET /v1/me'), true);
  assert.equal(calls.some(call => call.includes('/auth/refresh')), false);
  assert.equal(calls.some(call => call.includes('/v1/networking/keys') || call.includes('/hosted')), false);
  assert.equal(h.writes.length, 0);
});
