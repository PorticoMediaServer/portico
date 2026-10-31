import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, sign} from 'node:crypto';
import {HttpLocalApi, transportJSON, type LocalSession} from '../src/index.ts';
import {AppSessionStore, type AppSessionRecord} from '../src/app-sessions.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {routeBase64, routeBytes, webRouteCrypto, type ServerPin} from '../src/route-identity.ts';
import {createMemorySetupStore, type SetupAttempt, type SetupLocal, type SetupRecord, type SetupStore} from '../src/setup-store.ts';
import type {SetupPortContext} from '../src/setup-controller.ts';
import {createSetupAdapter, type SetupAdapterDeps} from '../src/setup-adapter.ts';
import {routePath} from '../../contracts/src/routes.ts';

// The shared adapter's app half: the app token route, and a Portico Account setup finished here (`finish: here`) once
// the server reads installed. The public claim vector's seed key signs each setup start as the server would.

const vector = JSON.parse(readFileSync(new URL('../../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const claim = {...JSON.parse(vector.claimPayload), purpose: 'setup'}, parent = JSON.parse(vector.startPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
const signed = (value: unknown) => {const raw = transportJSON(value); return {payload: Buffer.from(raw).toString('base64url'), signature: sign(null, Buffer.from(raw), key).toString('base64url')};};
const REQUEST = '11111111-2222-4333-8444-555555555555', NOW = Date.parse('2030-01-02T03:04:07Z');
const installationId = parent.installationId as string, hostedOrigin = claim.audience as string, ORIGIN = claim.address as string;
const SERVER_NAME = claim.name as string, SETUP_TOKEN = 'issued-setup-token';
const ACCOUNT = 'acct_owner', HOSTED_ACCOUNT = 'hosted_acct_1', FAMILY = 'setup_family';
const PROFILE = {id: 'p_main', name: 'Owner', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false, allowedLibraries: []};

type Call = {path: string; method: string; token?: string; body?: any};
type Deferred<T = void> = Readonly<{promise: Promise<T>; resolve: (value: T) => void}>;
function deferred<T = void>(): Deferred<T> {let resolve!: (value: T) => void; const promise = new Promise<T>(next => {resolve = next;}); return {promise, resolve};}

async function fixture(t: TestContext, options: Readonly<{
  origin?: string;
  local?: SetupLocal;
  approveInApp?: SetupAdapterDeps['approveInApp'];
  /** When set, `remember` writes the session and then waits here, so a test can dispose mid-exchange. */
  rememberGate?: Promise<void>;
}> = {}) {
  setInstallationIdentity({installationId, name: 'Phone', platform: 'ios', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const origin = options.origin ?? ORIGIN;
  const pin: ServerPin = {serverId: claim.serverId, publicKey: claim.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(claim.publicKey, 32)))};
  const calls: Call[] = [], navigated: string[] = [];
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
  const failure = (status: number, code: string) => json({error: {code, message: code, retryable: false}}, status);
  let state: unknown = {status: 'in_progress', setup: {variant: 'awaiting_approval', requestId: REQUEST, revision: '2'}};
  // The owner grant this server answers the setup exchange with, and the account snapshot the admitted owner re-reads.
  const grant = {authority: 'local', serverId: pin.serverId,
    account: {id: ACCOUNT, username: 'owner', primaryProfileId: PROFILE.id, role: 'owner', revision: 1, disabled: false, allowedLibraries: [], hostedAccountId: HOSTED_ACCOUNT},
    canManage: true, capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}, profiles: [PROFILE],
    session: {accessToken: 'a'.repeat(43), expiresAt: '2030-01-02T04:00:00Z',
      viewer: {accountId: ACCOUNT, profileId: PROFILE.id, serverId: pin.serverId, authority: 'local', role: 'owner'},
      sessionFamilyId: FAMILY, tokenGeneration: '1', authorizationHorizon: '2030-03-01T00:00:00Z',
      installationId, serverIdentity: {publicKey: pin.publicKey, fingerprint: pin.fingerprint}}};
  const claimHash = routeBase64(await webRouteCrypto.sha256(Buffer.from(transportJSON(claim))));
  const answers: Record<string, (body: any) => Response> = {
    'GET /v1/setup/state': () => json(state),
    'POST /v1/setup/app': () => json({serverId: pin.serverId, setupToken: SETUP_TOKEN}),
    'POST /v1/setup/browser': () => failure(400, 'wrong_setup_route'),
    'POST /v1/setup': () => json({variant: 'claim-required', claimRequired: true, serverId: pin.serverId, requestId: REQUEST, authMode: 'hosted', revision: '2'}),
    'POST /v1/setup/approval': body => {
      const start = body.setupStart;
      const payload = {kind: 'portico.setup-start.v1', audience: claim.audience, claimHash, operationId: claim.operationId, serverId: claim.serverId,
        publicKey: claim.publicKey, localGeneration: claim.localGeneration, address: claim.address, purpose: 'setup',
        setupRequestId: REQUEST, setupRevision: '2', state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId,
        serverChallenge: parent.serverChallenge, issuedAt: parent.issuedAt, expiresAt: parent.expiresAt};
      const code = 'claim_' + 'K'.repeat(43);
      return json({state: 'prepared', identity: {serverId: claim.serverId, localGeneration: claim.localGeneration},
        operation: {operationId: claim.operationId, localGeneration: claim.localGeneration, revision: '2'},
        accountId: 'account-unbound', installationAcknowledged: false, approvalRequired: true, actions: ['cancel', 'approve'],
        approvalUrl: hostedOrigin + '/claim#' + new URLSearchParams({code, operation: claim.operationId, server: SERVER_NAME}),
        claimCode: code, setupContinuation: {transactionId: 'setup_transaction', phase: 'prepared', expiresAt: parent.expiresAt, claim: signed(claim), start: signed(payload)}});
    },
    'POST /v1/auth/portico-return/exchange': () => json(grant),
    'GET /v1/direct': () => json(grant),
    'DELETE /v1/sessions/current': () => json({}),
  };
  const fetcher: typeof fetch = async (url, init) => {
    const u = new URL(String(url)), method = init?.method ?? 'GET', headers = new Headers(init?.headers);
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({path: u.pathname, method, token: headers.get('Authorization')?.replace('Bearer ', ''), body});
    const answer = answers[`${method} ${u.pathname}`];
    return answer ? answer(body) : failure(404, 'not_found');
  };
  const route = {logicalOrigin: origin, pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const makeApi = () => {const api = new HttpLocalApi(origin, '', fetcher); api.useRoutes(route as never); return api;};
  let record: AppSessionRecord = {version: 1, revision: 0, servers: []};
  const sessions = new AppSessionStore({read: async () => record, change: async (update, beforeCommit) => {
    const next = update(record);
    if (!next) throw new Error('The fixture never empties the whole session record.');
    beforeCommit?.();
    record = next;
  }});
  const store = createMemorySetupStore(options.local ?? {});
  const remembered: Array<{family: string; hosted: any}> = [], retireCalls: string[] = [], disposedConnections: string[] = [];
  const connectCalls: Array<{origin: string; pin: ServerPin}> = [], connectPorticoCalls: Array<{origin: string; pin: ServerPin; source: any}> = [];
  const entered = deferred<void>();
  const connect = async (origin: string, pin: ServerPin) => {connectCalls.push({origin, pin}); return {api: makeApi(), connection: {dispose: () => {disposedConnections.push('connect');}} as never};};
  const connectPortico = async (origin: string, pin: ServerPin, source: any) => {connectPorticoCalls.push({origin, pin, source});
    return {api: makeApi(), connection: {dispose: () => {disposedConnections.push('portico');}} as never,
      hosted: {origin: hostedOrigin, accountId: HOSTED_ACCOUNT, key: {keyId: 'key_1', publicKey: 'k'.repeat(43)}}};};
  // The same storage call rememberNativeSession makes, with no route to establish: the session becomes this device's selection.
  const remember = async (_api: unknown, session: LocalSession, env: any, hosted: any, admitted: () => boolean) => {
    const connection = {version: '1', selectionId: routeBase64(webRouteCrypto.random(24)), logicalOrigin: origin, session,
      pin: {serverId: pin.serverId, publicKey: pin.publicKey, fingerprint: pin.fingerprint}, routes: {paired: [origin]}, ...(hosted ? {hosted} : {})};
    await env.storage.change(() => connection, () => {if (!admitted()) throw Object.assign(new Error('The selected account or server changed.'), {code: 'cancelled'});});
    remembered.push({family: session.sessionFamilyId, hosted});
    entered.resolve();
    if (options.rememberGate) await options.rememberGate;
  };
  const retire = async (_api: unknown, session: LocalSession) => {retireCalls.push(session.sessionFamilyId);};
  let pairDisposals = 0;
  const deps: SetupAdapterDeps = {
    tokenRoute: 'start_app_setup',
    finish: {kind: 'here', ports: {sessions, environment: () => ({}) as never, now: () => NOW, connect: connect as never, connectPortico: connectPortico as never,
      remember: remember as never, retire: retire as never}},
    hostedOrigin, pageOrigin: () => origin,
    pair: async () => ({server: {id: pin.serverId, name: 'Den'} as never, api: makeApi(), pin, dispose: () => {pairDisposals += 1;}}),
    sessions: () => sessions, environment: () => ({}) as never, restore: async () => undefined, remember: async () => {},
    store: () => store, installationId: () => installationId, navigate: url => {navigated.push(url);},
    ...(options.approveInApp ? {approveInApp: options.approveInApp} : {}),
    now: () => NOW, newRequestId: () => REQUEST,
  };
  const adapter = createSetupAdapter(deps);
  t.after(() => adapter.dispose());
  const context = (): SetupPortContext => ({signal: new AbortController().signal, intentId: 'intent-1'});
  const patchAttempt = async (patch: Partial<SetupAttempt>) => {
    const current = store.peek().attempt!;
    assert.equal(await store.change({attemptId: current.attemptId}, {attempt: {...current, ...patch}}), true);
  };
  const replaceFamily = async (family: string) => {record = {...record, servers: record.servers.map(entry =>
    entry.connection.pin.serverId === pin.serverId ? {...entry, connection: {...entry.connection, session: {...entry.connection.session, sessionFamilyId: family}}} : entry)};};
  return {adapter, ports: adapter.ports, calls, navigated, store, sessions, pin, context, setState: (value: unknown) => {state = value;},
    remembered, retireCalls, connectCalls, connectPorticoCalls, disposedConnections, entered: entered.promise,
    patchAttempt, replaceFamily};
}

/** The person has gone through start, initialize and the approval, so the store holds a bound, live attempt. */
async function approvalReady(f: Awaited<ReturnType<typeof fixture>>) {
  const read = await f.ports.readState(f.context());
  assert.equal(read.server.kind, 'state');
  assert.deepEqual(await f.ports.start(f.context()), {kind: 'startDone'});
  assert.deepEqual(await f.ports.initializeHosted('Den', f.context()), {kind: 'initializeDone'});
  const ready = await f.ports.requestApproval(false, f.context());
  assert.equal(ready.kind, 'approvalReady');
  return ready as {kind: 'approvalReady'; approvalUrl: string};
}

const installed = () => ({status: 'in_progress', setup: {variant: 'installed', requestId: REQUEST, revision: '2'}});
const lapsed = {kind: 'exchangeRefused', lapsed: true, error: {code: 'setup_attempt_lapsed', retryable: true}};
const hostedRecord: SetupRecord = {version: 2, serverId: claim.serverId, requestId: REQUEST, setupToken: 'saved-setup-token', tokenAt: 1, name: 'Den', authMode: 'hosted'};

test('start asks the app for a setup token at the app route, never the browser route', async t => {
  const f = await fixture(t);
  await f.ports.readState(f.context());
  assert.deepEqual(await f.ports.start(f.context()), {kind: 'startDone'});
  assert.equal(routePath('start_app_setup'), '/v1/setup/app');
  const posted = f.calls.filter(call => call.method === 'POST' && call.path.startsWith('/v1/setup'));
  assert.deepEqual(posted.map(call => `${call.method} ${call.path}`), ['POST /v1/setup/app']);
  assert.deepEqual(posted[0]!.body, {});
  assert.equal(f.calls.some(call => call.path === '/v1/setup/browser'), false);
});

test('a public http address is refused before any setup request', async t => {
  const f = await fixture(t, {origin: 'http://8.8.8.8:32500'});
  assert.deepEqual(await f.ports.start(f.context()), {kind: 'addressNotAllowed'});
  assert.deepEqual(f.calls, []);
});

test('the exchange with no attempt is refused and sends nothing', async t => {
  const f = await fixture(t, {local: {record: hostedRecord}});
  assert.deepEqual(await f.ports.exchange(f.context()), lapsed);
  assert.deepEqual(f.calls, []);
});

test('the exchange with a lapsed attempt is refused and leaves the attempt in place', async t => {
  const f = await fixture(t);
  await approvalReady(f);
  const attempt = f.store.peek().attempt!;
  await f.patchAttempt({expiresAt: '2030-01-02T03:00:00Z'});
  const sent = f.calls.length;
  assert.deepEqual(await f.ports.exchange(f.context()), lapsed);
  assert.equal(f.calls.length, sent);
  assert.equal(f.store.peek().attempt?.attemptId, attempt.attemptId);
});

test('the exchange with another installation\'s attempt is refused and sends nothing', async t => {
  const f = await fixture(t);
  await approvalReady(f);
  const attempt = f.store.peek().attempt!;
  await f.patchAttempt({installationId: 'j'.repeat(43)});
  const sent = f.calls.length;
  assert.deepEqual(await f.ports.exchange(f.context()), lapsed);
  assert.equal(f.calls.length, sent);
  assert.equal(f.store.peek().attempt?.attemptId, attempt.attemptId);
});

test('the exchange finishes here once the server reads installed, spends the attempt, and admits only the exchanged sign-in', async t => {
  const f = await fixture(t);
  const ready = await approvalReady(f);
  const attempt = f.store.peek().attempt!;
  assert.ok(attempt.handoff, 'the attempt is bound to its signed start');
  assert.equal(attempt.approvalUrl, ready.approvalUrl);
  f.setState(installed());
  assert.deepEqual(await f.ports.exchange(f.context()), {kind: 'exchangeDone'});
  // The attempt was spent and the setup record's work is done.
  assert.equal(f.store.peek().attempt, undefined);
  assert.equal(f.store.peek().record, undefined);
  // The exchange used the app's injected ports, never a real connection.
  assert.deepEqual(f.connectCalls.map(call => [call.origin, call.pin.serverId]), [[ORIGIN, claim.serverId]]);
  assert.equal(f.connectPorticoCalls.length, 1);
  assert.equal(f.connectPorticoCalls[0]!.origin, ORIGIN);
  assert.equal(f.connectPorticoCalls[0]!.source.origin, hostedOrigin);
  assert.equal(f.connectPorticoCalls[0]!.source.accountId, HOSTED_ACCOUNT);
  assert.deepEqual(f.remembered.map(entry => entry.family), [FAMILY]);
  assert.deepEqual(f.remembered[0]!.hosted, {origin: hostedOrigin, accountId: HOSTED_ACCOUNT, key: {keyId: 'key_1', publicKey: 'k'.repeat(43)}});
  const exchangeCall = f.calls.filter(call => call.method === 'POST' && call.path === '/v1/auth/portico-return/exchange');
  assert.equal(exchangeCall.length, 1);
  assert.deepEqual(exchangeCall[0]!.body, {transactionId: 'setup_transaction', state: attempt.state, verifier: attempt.verifier,
    installationId, serverChallenge: parent.serverChallenge, purpose: 'setup'});
  const selected = await f.sessions.selected();
  assert.equal(selected?.signIn, 'portico');
  assert.equal(selected?.connection.session.sessionFamilyId, FAMILY);
  assert.equal(selected?.connection.session.viewer.accountId, ACCOUNT);
  assert.equal(selected?.connection.hosted?.origin, hostedOrigin);
  assert.equal(selected?.connection.hosted?.accountId, HOSTED_ACCOUNT);
  // The owner is admitted from that selection, and its own account read.
  assert.deepEqual(await f.ports.adoptOwner(f.context()), {kind: 'ownerAdopted'});
  assert.equal(f.calls.filter(call => call.method === 'GET' && call.path === '/v1/direct').length, 1);
  assert.deepEqual(f.navigated, [], 'the app approves without sending the person to Portico Services');
});

test('the owner is not adopted once the selection is no longer the exchanged sign-in', async t => {
  const f = await fixture(t);
  await approvalReady(f);
  f.setState(installed());
  await f.ports.exchange(f.context());
  await f.replaceFamily('successor_family');
  const direct = f.calls.filter(call => call.path === '/v1/direct').length;
  await assert.rejects(f.ports.adoptOwner(f.context()), {code: 'cancelled'});
  assert.equal(f.calls.filter(call => call.path === '/v1/direct').length, direct);
});

test('in-app approval is unsupported without the app\'s own approval', async t => {
  const f = await fixture(t);
  await assert.rejects(f.ports.approveInApp(f.context()), {code: 'unsupported'});
  assert.deepEqual(f.calls, []);
});

test('in-app approval carries the bound attempt and its link, and returns approvedInApp', async t => {
  const captured: any[] = [];
  const f = await fixture(t, {approveInApp: async input => {captured.push(input); await input.current();}});
  const ready = await approvalReady(f);
  const attempt = f.store.peek().attempt!;
  assert.deepEqual(await f.ports.approveInApp(f.context()), {kind: 'approvedInApp'});
  assert.equal(captured.length, 1);
  assert.equal(captured[0]!.attempt.attemptId, attempt.attemptId);
  assert.ok(captured[0]!.attempt.handoff);
  assert.equal(captured[0]!.approvalUrl, ready.approvalUrl);
  assert.equal(captured[0]!.attempt.approvalUrl, attempt.approvalUrl);
  assert.ok(captured[0]!.signal instanceof AbortSignal);
});

test('in-app approval refuses once the attempt in the store is replaced', async t => {
  let store: SetupStore | undefined;
  const f = await fixture(t, {approveInApp: async input => {
    await input.current();
    assert.equal(await store!.change({attemptId: input.attempt.attemptId}, {attempt: {...input.attempt, attemptId: 'successor_attempt'}}), true);
    await input.current();
  }});
  store = f.store;
  await approvalReady(f);
  await assert.rejects(f.ports.approveInApp(f.context()), {code: 'cancelled'});
  assert.equal(f.store.peek().attempt?.attemptId, 'successor_attempt');
});

test('dispose during the exchange leaves nothing remembered', async t => {
  const gate = deferred<void>();
  const f = await fixture(t, {rememberGate: gate.promise});
  await approvalReady(f);
  f.setState(installed());
  const running = f.ports.exchange(f.context());
  await f.entered;
  assert.equal(f.remembered.length, 1);
  f.adapter.dispose();
  gate.resolve();
  await assert.rejects(running, {code: 'cancelled'});
  assert.deepEqual((await f.sessions.read()).servers, []);
  assert.deepEqual(f.retireCalls, [FAMILY]);
  assert.deepEqual(f.disposedConnections.sort(), ['connect', 'portico']);
});

test('the setup code the app shares uses no abort helper the app runtime lacks (Hermes has no AbortSignal.any or AbortSignal.timeout)', async () => {
  const {readFileSync} = await import('node:fs');
  for (const file of ['setup-adapter.ts', 'setup-exchange.ts', 'setup-approval-client.ts', 'setup-claim-client.ts', 'setup-controller.ts', 'setup-custody.ts', 'setup-slot-store.ts']) {
    const source = readFileSync(new URL('../src/' + file, import.meta.url), 'utf8').replace(/\/\/.*$|\/\*[\s\S]*?\*\//gm, '');
    assert.doesNotMatch(source, /AbortSignal\.(any|timeout)\s*\(/, file);
  }
});
