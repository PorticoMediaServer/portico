import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createHash, createPrivateKey, sign} from 'node:crypto';
import {HttpLocalApi, transportJSON, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {routeBase64, routeBytes} from '@core/route-identity.ts';
import type {SetupAttempt, SetupRecord} from '@core/setup-store.ts';
import {BrowserSetupStore} from '../src/platform/setup-store.ts';
import {BrowserSetupReturn} from '../src/platform/setup-return.ts';

function slot(initial?: unknown, options: {damaged?: boolean} = {}) {
  let value = initial, damaged = options.damaged === true;
  return {
    assertAvailable: () => {},
    read: async () => {if (damaged) throw Object.assign(new Error('damaged'), {code: 'invalid_saved_connection'}); return value;},
    // The protected store reads damaged bytes as absent inside a change.
    change: async (update: (old: unknown) => unknown) => {const next = update(damaged ? undefined : value); damaged = false; value = next;},
    peek: () => value,
  };
}
const ORIGIN = 'http://192.168.1.20:32400';
const record: SetupRecord = {version: 2, serverId: 'srv', requestId: 'req', setupToken: 'tok', tokenAt: 1, name: 'Den', authMode: 'hosted'};
const handoff = {expected: {operationId: 'op', localGeneration: '1', revision: '2'}, binding: {pin: {serverId: 'srv', publicKey: 'k', fingerprint: 'f'}, origin: ORIGIN,
  hostedOrigin: 'https://hosted.example', operationId: 'op', localGeneration: '1', setupRequestId: 'req', setupRevision: '2', state: 's', codeChallenge: 'c', installationId: 'inst'},
  transactionId: 'setup_tx', expiresAt: '2099-01-01T00:00:00Z', claim: {payload: 'p', signature: 's'}, start: {payload: 'p', signature: 's'}};
const attempt: SetupAttempt = {version: 2, serverId: 'srv', requestId: 'req', attemptId: 'att', origin: ORIGIN, hostedOrigin: 'https://hosted.example', installationId: 'inst',
  state: 's', verifier: 'v', codeChallenge: 'c', createdAt: 1, expiresAt: '2098-01-01T00:00:00Z', handoff};

test('each slot is compare-and-swapped on its own id, and a successor stays', async () => {
  const r = slot(record), a = slot(attempt), store = new BrowserSetupStore('srv', {record: r, attempt: a});
  assert.deepEqual(await store.read(), {record, attempt});
  assert.equal(await store.change({requestId: 'other'}, {record: null}), false);
  assert.deepEqual(r.peek(), record, 'the successor is kept');
  assert.equal(await store.change({attemptId: 'att'}, {attempt: null}), true);
  assert.equal(a.peek(), undefined);
  assert.equal(await store.change({requestId: 'req'}, {record: null}), true);
  assert.deepEqual(await store.read(), {});
  // Another server's record is never written here.
  assert.equal(await store.change({}, {record: {...record, serverId: 'elsewhere'}}), false);
});

test('this tab\'s attempt for another server reads as none here and stays; damaged bytes read as unreadable and retire', async () => {
  const a = slot({...attempt, serverId: 'elsewhere'}), store = new BrowserSetupStore('srv', {record: slot(), attempt: a});
  assert.deepEqual(await store.read(), {});
  assert.equal((a.peek() as SetupAttempt).serverId, 'elsewhere');
  const damaged = new BrowserSetupStore('srv', {record: slot(undefined, {damaged: true}), attempt: slot()});
  assert.deepEqual(await damaged.read(), {unreadable: true});
  assert.equal(await damaged.change({}, {record: null}), true);
  assert.deepEqual(await damaged.read(), {});
});

function returning(stored: unknown = attempt) {
  const a = slot(stored), r = slot(record), connects: string[] = [];
  const setup = new BrowserSetupReturn({attempt: a, store: () => new BrowserSetupStore('srv', {record: r, attempt: a}),
    sessions: new AppSessionStore({read: async () => ({version: 1, revision: 0, servers: []}), change: async () => {}}), environment: () => ({}) as never,
    installationId: () => 'inst', pageOrigin: () => ORIGIN, now: () => Date.parse('2030-01-01T00:00:00Z'),
    connect: async () => {connects.push('connect'); throw new Error('no network in this test');}});
  return {setup, a, connects};
}
const callback = (transaction: string) => ({href: ORIGIN + '/auth/portico-return', fragment: '#transaction=' + transaction});

test('a Return is offered only for this tab\'s bound attempt on this page', async () => {
  assert.deepEqual(await returning().setup.pendingReturn(), {kind: 'setup', transactionId: 'setup_tx'});
  assert.equal(await returning({...attempt, handoff: undefined}).setup.pendingReturn(), undefined);
  assert.equal(await returning({...attempt, origin: 'http://192.168.1.21:32400', handoff: {...handoff, binding: {...handoff.binding, origin: 'http://192.168.1.21:32400'}}}).setup.pendingReturn(), undefined);
  assert.equal(await returning(null).setup.pendingReturn(), undefined);
});

test('a callback for another transaction is refused and leaves the attempt; a valid one spends the attempt before any network', async () => {
  const other = returning();
  await assert.rejects(other.setup.finish(callback('another_tx')), /no longer matches/);
  assert.deepEqual(other.a.peek(), attempt);
  assert.deepEqual(other.connects, []);
  const mine = returning();
  // The fixture's signed pair is not real, so verification fails after the spend: the attempt is gone and nothing was sent.
  await assert.rejects(mine.setup.finish(callback('setup_tx')));
  assert.equal(mine.a.peek(), undefined);
  assert.deepEqual(mine.connects, []);
});

// --- a real signed owner grant, so the path after the exchange is reachable ---

// The public claim vector's seed key signs the setup start, exactly as setup-adapter.test.ts does.
const vector = JSON.parse(readFileSync(new URL('../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const seed = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const signed = (value: unknown) => {const raw = transportJSON(value); return {payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), seed).toString('base64url')};};
const digest = (value: string | Uint8Array) => createHash('sha256').update(value).digest();
const vectorClaim = JSON.parse(vector.claimPayload), vectorStart = JSON.parse(vector.startPayload);
const claim = {...vectorClaim, purpose: 'setup'};
const SERVER = claim.serverId as string, OWNER_ORIGIN = claim.address as string, HOSTED = claim.audience as string;
const PIN = {serverId: SERVER, publicKey: claim.publicKey as string, fingerprint: routeBase64(digest(routeBytes(claim.publicKey, 32)))};
const REQUEST = '11111111-2222-4333-8444-555555555555', NOW = Date.parse('2030-01-02T03:04:07Z');
const INSTALL = vectorStart.installationId as string, STATE = 's'.repeat(43), VERIFIER = routeBase64(new Uint8Array(32).fill(7));
const CODE_CHALLENGE = routeBase64(digest(VERIFIER)), EXPIRES = vectorStart.expiresAt as string, TOKEN = 'a'.repeat(43);
// The signed start names this attempt's own state, challenge and installation; its window is the attempt's.
const ownerStart = {kind: 'portico.setup-start.v1', audience: HOSTED, claimHash: routeBase64(digest(transportJSON(claim))),
  operationId: claim.operationId as string, serverId: SERVER, publicKey: PIN.publicKey, localGeneration: claim.localGeneration as string,
  address: OWNER_ORIGIN, purpose: 'setup', setupRequestId: REQUEST, setupRevision: '2', state: STATE, codeChallenge: CODE_CHALLENGE,
  installationId: INSTALL, serverChallenge: vectorStart.serverChallenge as string, issuedAt: vectorStart.issuedAt as string, expiresAt: EXPIRES};
const ownerRecord: SetupRecord = {version: 2, serverId: SERVER, requestId: REQUEST, setupToken: 'saved-setup-token', tokenAt: 1, name: 'Den', authMode: 'hosted'};
const ownerAttempt: SetupAttempt = {version: 2, serverId: SERVER, requestId: REQUEST, attemptId: 'att_owner', origin: OWNER_ORIGIN, hostedOrigin: HOSTED,
  installationId: INSTALL, state: STATE, verifier: VERIFIER, codeChallenge: CODE_CHALLENGE, createdAt: 1, expiresAt: EXPIRES,
  handoff: {expected: {operationId: claim.operationId as string, localGeneration: claim.localGeneration as string, revision: '2'},
    binding: {pin: PIN, origin: OWNER_ORIGIN, hostedOrigin: HOSTED, operationId: claim.operationId as string, localGeneration: claim.localGeneration as string,
      setupRequestId: REQUEST, setupRevision: '2', state: STATE, codeChallenge: CODE_CHALLENGE, installationId: INSTALL},
    transactionId: 'setup_tx', expiresAt: EXPIRES, claim: signed(claim), start: signed(ownerStart)}};
const HOSTED_ACCOUNT = 'hosted-account-1';
// A raw direct owner sign-in the exchange could answer: one unprotected primary profile, this installation, this server's key.
const grant = {authority: 'local', serverId: SERVER, canManage: true,
  account: {id: 'acct', username: 'owner', primaryProfileId: 'p1', role: 'owner', revision: 1, disabled: false, allowedLibraries: [], hostedAccountId: HOSTED_ACCOUNT},
  profiles: [{id: 'p1', name: 'Owner', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false, allowedLibraries: []}],
  capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
  session: {accessToken: TOKEN, refreshToken: 'r'.repeat(43), deviceId: 'dev', installationId: INSTALL, expiresAt: '2099-01-01T00:00:00Z',
    viewer: {accountId: 'acct', profileId: 'p1', serverId: SERVER, authority: 'local', role: 'owner'}, sessionFamilyId: 'family-1', tokenGeneration: '1',
    authorizationHorizon: '2099-03-01T00:00:00Z', serverIdentity: {publicKey: PIN.publicKey, fingerprint: PIN.fingerprint}}};

type Failure = 'connectPortico' | 'remember' | 'selection' | 'none';
/** A Return whose exchange answers the owner grant and whose ports fail at one chosen step after it. */
function ownerReturn(failure: Failure = 'none') {
  const a = slot(ownerAttempt), r = slot(ownerRecord), retires: string[] = [], calls: string[] = [];
  let pageOrigin = OWNER_ORIGIN, connectedDisposed = 0, boundDisposed = 0, exchange: Record<string, unknown> | undefined;
  const remembered: string[] = [];
  let stored: AppSessionRecord = {version: 1, revision: 0, servers: []};
  const sessions = new AppSessionStore({read: async () => stored, change: async update => {stored = update(stored)!;}});
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
  const fetcher: typeof fetch = async (url, init) => {
    const path = new URL(String(url)).pathname, method = init?.method ?? 'GET';
    calls.push(method + ' ' + path);
    if (method === 'GET' && path === '/v1/setup/state') return json({status: 'in_progress', setup: {variant: 'installed', requestId: REQUEST, revision: '2'}});
    if (method === 'POST' && path === '/v1/auth/portico-return/exchange') {exchange = JSON.parse(String(init?.body)); return json(grant);}
    return json({error: {code: 'not_found', message: 'not found'}}, 404);
  };
  const route = {logicalOrigin: OWNER_ORIGIN, pin: PIN, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const pair = (dispose: () => void) => {const api = new HttpLocalApi(OWNER_ORIGIN, '', fetcher); api.useRoutes(route as never); return {api, connection: {dispose}};};
  const link = (session: LocalSession) => ({version: '1' as const, selectionId: 'A'.repeat(32), logicalOrigin: OWNER_ORIGIN, pin: PIN, session,
    routes: {paired: [] as string[]}, hosted: {origin: HOSTED, accountId: HOSTED_ACCOUNT, key: {keyId: 'key-1', publicKey: 'k'.repeat(43)}}});
  const setup = new BrowserSetupReturn({attempt: a, store: () => new BrowserSetupStore(SERVER, {record: r, attempt: a}), sessions, environment: () => ({}) as never,
    installationId: () => INSTALL, pageOrigin: () => pageOrigin, now: () => NOW,
    connect: async () => pair(() => {++connectedDisposed;}) as never,
    connectPortico: async () => {
      if (failure === 'connectPortico') throw new Error('the owner grant could not be paired');
      return {...pair(() => {++boundDisposed;}), hosted: {origin: HOSTED, accountId: HOSTED_ACCOUNT, key: {keyId: 'key-1', publicKey: 'k'.repeat(43)}}} as never;
    },
    remember: async (_api, session, env) => {
      if (failure === 'remember') throw new Error('admission storage failed');
      await env.storage.change(() => link(session));
      remembered.push((await env.storage.read())?.session.sessionFamilyId ?? '');
      // The write landed and selected the server; this page then stops being the attempt's page.
      if (failure === 'selection') pageOrigin = 'https://elsewhere.example.test';
    },
    retire: async (_api, session) => {retires.push(session.accessToken);}});
  return {setup, a, r, sessions, retires, calls, exchange: () => exchange, remembered: () => remembered,
    connectedDisposed: () => connectedDisposed, boundDisposed: () => boundDisposed};
}
const ownerFinish = (f: ReturnType<typeof ownerReturn>) => f.setup.finish({href: OWNER_ORIGIN + '/auth/portico-return', fragment: '#transaction=setup_tx'});

test('a connectPortico failure after the owner grant retires the session, keeps the record and spends the attempt', async () => {
  const f = ownerReturn('connectPortico');
  await assert.rejects(ownerFinish(f), /could not be paired/);
  assert.deepEqual(f.retires, [TOKEN], 'the grant session is retired once');
  assert.deepEqual(f.r.peek(), ownerRecord, 'the setup record is still there for the setup page');
  assert.equal(f.a.peek(), undefined, 'the attempt was spent before the network');
  assert.equal(f.connectedDisposed(), 1);
  assert.equal(f.boundDisposed(), 0, 'nothing was bound');
  assert.deepEqual((await f.sessions.read()).servers, [], 'no server was admitted');
  assert.ok(f.calls.includes('POST /v1/auth/portico-return/exchange'), 'the exchange had answered the grant');
});

test('a remember failure after the owner grant retires the session, disposes the bound connection and keeps the record', async () => {
  const f = ownerReturn('remember');
  await assert.rejects(ownerFinish(f), /admission storage failed/);
  assert.deepEqual(f.retires, [TOKEN]);
  assert.deepEqual(f.r.peek(), ownerRecord);
  assert.equal(f.a.peek(), undefined);
  assert.equal(f.connectedDisposed(), 1);
  assert.equal(f.boundDisposed(), 1, 'the bound connection did not survive');
  assert.deepEqual((await f.sessions.read()).servers, []);
});

test('a remember whose admission is no longer current rolls the connection back and reports cancelled', async () => {
  const f = ownerReturn('selection');
  await assert.rejects(ownerFinish(f), {code: 'cancelled'});
  assert.deepEqual(f.remembered(), ['family-1'], 'remember stored the connection before admission failed');
  const stored = await f.sessions.read();
  assert.equal(stored.servers.filter(value => value.connection.session.sessionFamilyId === 'family-1').length, 0, 'the remembered family was removed');
  assert.equal(stored.selectedServerId, undefined, 'its selection went with it');
  assert.deepEqual(f.retires, [TOKEN]);
  assert.deepEqual(f.r.peek(), ownerRecord);
  assert.equal(f.a.peek(), undefined);
  assert.equal(f.connectedDisposed(), 1);
  assert.equal(f.boundDisposed(), 1);
});

test('an admitted owner grant clears the record and keeps the bound connection', async () => {
  const f = ownerReturn();
  const done = await ownerFinish(f);
  assert.equal(done.result.kind, 'signed-in');
  assert.equal(f.r.peek(), undefined, 'the record has done its work');
  assert.equal(f.a.peek(), undefined);
  assert.deepEqual(f.retires, [], 'nothing is retired');
  assert.equal(f.connectedDisposed(), 1);
  assert.equal(f.boundDisposed(), 0, 'the admitted connection stays');
  const stored = await f.sessions.read();
  assert.equal(stored.selectedServerId, SERVER);
  assert.equal(stored.servers[0]?.signIn, 'portico');
  assert.equal(stored.servers[0]?.connection.session.sessionFamilyId, 'family-1');
  assert.deepEqual(f.exchange(), {transactionId: 'setup_tx', state: STATE, verifier: VERIFIER, installationId: INSTALL,
    serverChallenge: vectorStart.serverChallenge, purpose: 'setup'});
});
