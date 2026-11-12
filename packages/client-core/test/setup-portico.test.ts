// First-run setup with a Portico Account on the rebuilt server, against a stand-in server: never the real Portico Services.
import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '../src/index.ts';
import {AppSessionStore, type AppSessionRecord} from '../src/app-sessions.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {routeBase64, routeBytes, webRouteCrypto, type ServerPin} from '../src/route-identity.ts';
import {createMemorySetupStore} from '../src/setup-store.ts';
import type {SetupPortContext} from '../src/setup-controller.ts';
import {createSetupAdapter} from '../src/setup-adapter.ts';
import {readSetupPageReturn, setupApprovalAccount, setupReturnUrl} from '../src/setup-portico.ts';

const INSTALLATION = 'installation_for_setup_portico_00001', ORIGIN = 'http://203.0.113.10:32500', HOSTED = 'https://web.getportico.example';
const SERVER = 'srv_setup_portico', PORTICO_ACCOUNT = 'pacct_owner', PUBLIC_KEY = Buffer.alloc(32, 7).toString('base64url');
const later = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();
const b64url = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('base64url');
/** What Portico Services hands back: a signed envelope (the stand-in signature is never checked here; the server checks it). */
const approvalFor = (over: Record<string, unknown> = {}) => b64url({payload: b64url({kind: 'portico.server-approval', approvalId: 'apv_1', serverId: SERVER, serverName: 'Den',
  purpose: 'setup', accountId: PORTICO_ACCOUNT, username: 'sam', displayName: 'Sam', issuedAt: later(0), expiresAt: later(10), ...over}), signature: 'sig', keyId: 'key'});
const session = (over: {profileId?: string; role?: string; accessToken?: string} = {}) => ({accessToken: over.accessToken ?? 'access_account', expiresAt: later(15), sessionId: 'ses_1',
  sessionFamilyId: 'family_1', tokenGeneration: '1', sessionExpiresAt: later(60 * 24 * 90), deviceId: 'dev_1', installationId: INSTALLATION, refreshToken: 'refresh_token_0000000001',
  viewer: {accountId: 'acct', serverId: SERVER, role: over.role ?? 'account', ...(over.profileId ? {profileId: over.profileId} : {})}});
const signedIn = {session: session(), account: {id: 'acct', username: 'sam', role: 'owner'}, profiles: [{id: 'main', name: 'Sam', color: 'blue', isPrimary: true, pinRequired: false}]};

type Call = {method: string; path: string; body?: any};
async function fixture(t: TestContext, options: {answers?: Record<string, (body: any) => unknown>; pageReturn?: ReturnType<typeof readSetupPageReturn>} = {}) {
  setInstallationIdentity({installationId: INSTALLATION, name: 'Browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const pin: ServerPin = {serverId: SERVER, publicKey: PUBLIC_KEY, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(PUBLIC_KEY, 32)))};
  const calls: Call[] = [], navigated: string[] = [];
  let setupRequired = true;
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
  const refusal = (status: number, code: string) => json({error: {code, message: code, retry: false}}, status);
  const answers: Record<string, (body: any) => unknown> = {
    'GET /v1/system': () => ({id: SERVER, name: 'Den', apiMajor: 1, capabilities: [], setupRequired, signInMethods: ['password'], signIn: setupRequired ? 'local' : 'portico', atHome: false}),
    'POST /v1/setup/portico': () => ({approvalId: 'apv_1', approvalUrl: HOSTED + '/approve/apv_1', code: 'ABCD-EFGH', pollToken: 'poll_token_1', expiresAt: later(10), pollAfterMs: 3000}),
    'POST /v1/setup/portico/finish': () => {setupRequired = false; return signedIn;},
    'POST /v1/profiles/main/select': () => session({profileId: 'main', role: 'owner', accessToken: 'access_main'}),
    'GET /v1/me': () => ({account: {id: 'acct', username: 'sam', role: 'owner'}, profile: {id: 'main', name: 'Sam', color: 'blue', isPrimary: true, pinRequired: false}, role: 'owner', canManageAccount: true}),
    'GET /v1/profiles': () => ({items: [{id: 'main', name: 'Sam', color: 'blue', isPrimary: true, pinRequired: false}]}),
    ...options.answers,
  };
  const fetcher: typeof fetch = async (url, init) => {
    const u = new URL(String(url)), method = init?.method ?? 'GET', body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({method, path: u.pathname, body});
    const answer = answers[`${method} ${u.pathname}`];
    if (!answer) return refusal(404, 'not_found');
    const value = answer(body);
    return value instanceof Response ? value : json(value);
  };
  const route = {logicalOrigin: ORIGIN, pin, fetch: fetcher, subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  let record: AppSessionRecord = {version: 1, revision: 0, servers: []};
  const sessions = new AppSessionStore({read: async () => record, change: async (update, beforeCommit) => {const next = update(record); beforeCommit?.(); if (next) record = next;}});
  const store = createMemorySetupStore({});
  // The same storage write rememberNativeSession makes: the session becomes this browser's selected server.
  const remember = async (_api: unknown, value: LocalSession, env: any, hosted: any) => {
    const connection = {version: '1', selectionId: routeBase64(webRouteCrypto.random(24)), logicalOrigin: ORIGIN, session: value,
      pin: {serverId: pin.serverId, publicKey: pin.publicKey, fingerprint: pin.fingerprint}, routes: {paired: [ORIGIN]}, ...(hosted ? {hosted} : {})};
    await env.storage.change(() => connection);
  };
  let pageReturn = options.pageReturn;
  const adapter = createSetupAdapter({tokenRoute: 'start_browser_setup', finish: {kind: 'return-page'}, hostedOrigin: HOSTED, pageOrigin: () => ORIGIN,
    pair: async () => {const api = new HttpLocalApi(ORIGIN, '', fetcher); api.useRoutes(route as never); return {server: {id: SERVER, name: 'Den'} as never, api, pin, dispose: () => {}};},
    sessions: () => sessions, environment: () => ({}) as never, restore: async () => undefined, remember: remember as never, store: () => store,
    installationId: () => INSTALLATION, navigate: url => {navigated.push(url);}, pageReturn: () => {const value = pageReturn; pageReturn = undefined; return value;}});
  t.after(() => adapter.dispose());
  const context = (): SetupPortContext => ({signal: new AbortController().signal, intentId: 'intent-1'});
  return {ports: adapter.ports, calls, navigated, store, record: () => record, context};
}

test('Portico Accounts asks for the setup code at any address, plain http on a public address included', async t => {
  const f = await fixture(t);
  assert.deepEqual(await f.ports.start(f.context()), {kind: 'setupCodeRequired'});
  assert.deepEqual(await f.ports.start(f.context(), 'ABCD-EFGH'), {kind: 'startDone'});
  assert.deepEqual(f.calls.filter(call => call.method !== 'GET'), [], 'the code is held until the next step sends it');
});

test('the setup code and the server\'s name go to the server, with a way back to this server\'s setup page; nothing is kept in the browser', async t => {
  const f = await fixture(t);
  await f.ports.start(f.context(), 'ABCD-EFGH');
  assert.deepEqual(await f.ports.initializeHosted('Den', undefined, f.context()), {kind: 'initializeDone'});
  assert.deepEqual(f.calls.at(-1), {method: 'POST', path: '/v1/setup/portico', body: {setupCode: 'ABCD-EFGH', serverName: 'Den', returnUrl: ORIGIN + '/setup?portico=return'}});
  const ready = await f.ports.requestApproval(true, f.context());
  assert.deepEqual([ready.kind, ready.kind === 'approvalReady' ? ready.approvalUrl : ''], ['approvalReady', HOSTED + '/approve/apv_1']);
  assert.deepEqual(f.store.peek(), {}, 'no setup record or attempt is written');
  assert.deepEqual(f.record().servers, [], 'no sign-in is kept before the approval comes back');
});

test('a wrong setup code is asked again', async t => {
  const f = await fixture(t, {answers: {'POST /v1/setup/portico': () => new Response(JSON.stringify({error: {code: 'setup_code_invalid', message: 'x'}}), {status: 403, headers: {'Content-Type': 'application/json'}})}});
  await f.ports.start(f.context(), 'WRONG-CODE');
  assert.deepEqual(await f.ports.initializeHosted('Den', undefined, f.context()), {kind: 'setupCodeInvalid'});
});

test('back with the approval: it goes to the server once, the owner is signed in, and the sign-in is kept as Portico, labelled with the approving account', async t => {
  const approval = approvalFor();
  const f = await fixture(t, {pageReturn: {returned: true, approval}});
  assert.equal((await f.ports.readState(f.context())).server.kind, 'approvalReturned');
  assert.deepEqual(await f.ports.exchange(f.context()), {kind: 'exchangeDone'});
  const finish = f.calls.find(call => call.path === '/v1/setup/portico/finish');
  assert.equal(finish?.body.approval, approval);
  assert.equal(finish?.body.device.installationId, INSTALLATION);
  assert.deepEqual(await f.ports.adoptOwner(f.context()), {kind: 'ownerAdopted'});
  const kept = f.record().servers;
  assert.equal(kept.length, 1);
  assert.equal(kept[0]!.signIn, 'portico');
  assert.deepEqual(kept[0]!.connection.hosted, {origin: HOSTED, accountId: PORTICO_ACCOUNT}, 'the account label, with no route key');
  assert.equal(kept[0]!.connection.session.viewer.role, 'owner');
  await assert.rejects(f.ports.exchange(f.context()), {code: 'cancelled'}, 'an approval is used once');
  assert.equal(f.calls.filter(call => call.path === '/v1/setup/portico/finish').length, 1);
});

test('an approval meant for another server, or not for setup, is never sent and labels nothing', async t => {
  for (const over of [{serverId: 'srv_someone_else'}, {purpose: 'switch'}, {kind: 'portico.something-else'}]) {
    const f = await fixture(t, {pageReturn: {returned: true, approval: approvalFor(over)}});
    await f.ports.readState(f.context());
    assert.deepEqual(await f.ports.exchange(f.context()), {kind: 'exchangeRefused', lapsed: false, error: {code: 'approval_declined', retryable: false}}, JSON.stringify(over));
    assert.equal(f.calls.some(call => call.path === '/v1/setup/portico/finish'), false);
  }
});

test('back without approving: not approved; the server\'s refusal of an approval says the same', async t => {
  const back = await fixture(t, {pageReturn: {returned: true}});
  assert.equal((await back.ports.readState(back.context())).server.kind, 'approvalNotGiven');
  const refused = await fixture(t, {pageReturn: {returned: true, approval: approvalFor()}, answers: {'POST /v1/setup/portico/finish': () =>
    new Response(JSON.stringify({error: {code: 'approval_declined', message: 'x'}}), {status: 409, headers: {'Content-Type': 'application/json'}})}});
  await refused.ports.readState(refused.context());
  assert.deepEqual(await refused.ports.exchange(refused.context()), {kind: 'exchangeRefused', lapsed: false, error: {code: 'approval_declined', retryable: false}});
  assert.deepEqual(refused.record().servers, []);
});

test('approved on another device, or in a phone app: the page asks the server how it stands until it is given, and a decline ends it', async t => {
  let answer: unknown = {state: 'waiting', pollAfterMs: 3000};
  const f = await fixture(t, {answers: {'POST /v1/hosted/approvals/apv_1/poll': body => {assert.deepEqual(body, {pollToken: 'poll_token_1'}); return answer;}}});
  await f.ports.start(f.context(), 'ABCD-EFGH');
  await f.ports.initializeHosted('Den', undefined, f.context());
  assert.deepEqual((await f.ports.readState(f.context())).server, {kind: 'approvalWaiting', pollAfterMs: 3000});
  answer = {state: 'approved', approval: approvalFor(), pollAfterMs: 3000};
  assert.deepEqual((await f.ports.readState(f.context())).server, {kind: 'approvalReturned'});
  assert.deepEqual(await f.ports.exchange(f.context()), {kind: 'exchangeDone'});
  const declined = await fixture(t, {answers: {'POST /v1/hosted/approvals/apv_1/poll': () =>
    new Response(JSON.stringify({error: {code: 'approval_declined', message: 'x'}}), {status: 409, headers: {'Content-Type': 'application/json'}})}});
  await declined.ports.start(declined.context(), 'ABCD-EFGH');
  await declined.ports.initializeHosted('Den', undefined, declined.context());
  assert.deepEqual((await declined.ports.readState(declined.context())).server, {kind: 'approvalNotGiven'});
});

test('the setup page reads its own return once: the marker and the approval in the fragment', () => {
  assert.equal(setupReturnUrl(ORIGIN), ORIGIN + '/setup?portico=return');
  const approval = approvalFor();
  assert.deepEqual(readSetupPageReturn('?portico=return', '#approval=' + approval), {returned: true, approval});
  assert.deepEqual(readSetupPageReturn('?portico=return', ''), {returned: true});
  assert.equal(readSetupPageReturn('', ''), undefined);
  assert.deepEqual(readSetupPageReturn('?portico=return', '#approval=not%20base64url!'), {returned: true}, 'an approval that is not base64url is dropped');
  assert.equal(setupApprovalAccount(approval, SERVER), PORTICO_ACCOUNT);
  assert.equal(setupApprovalAccount('garbage', SERVER), undefined);
});
