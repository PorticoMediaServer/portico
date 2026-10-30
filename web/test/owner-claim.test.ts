import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, randomUUID, sign} from 'node:crypto';
import {BrowserOwnerClaim} from '../src/platform/owner-claim.ts';
import {HttpLocalApi, transportJSON, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';

type Ports = ConstructorParameters<typeof BrowserOwnerClaim>[0];
type Pending = Awaited<ReturnType<Ports['journal']['read']>>;
const vector = JSON.parse(readFileSync(new URL('../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const original = JSON.parse(vector.claimPayload);
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
function signed(value: unknown) {const bytes = Buffer.from(transportJSON(value)); return {payload: bytes.toString('base64url'), signature: sign(null, bytes, key).toString('base64url')};}
async function fixture(t: TestContext, origin = original.address) {
  const hostedOrigin = original.audience, installation = randomUUID(), suffix = randomUUID();
  setInstallationIdentity({installationId: installation, name: 'Browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const pin = {serverId: original.serverId, publicKey: original.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(original.publicKey, 32)))};
  let now = Date.parse('2030-01-02T03:04:07Z'), saved: Pending, secure = true, installed = false, prepared = false, cancelled = false;
  const oldSession: LocalSession = {accessToken: 'old-access-' + suffix, refreshToken: routeBase64(new Uint8Array(32).fill(4)), sessionFamilyId: 'old-family-' + suffix, tokenGeneration: '1',
    expiresAt: '2030-01-02T04:00:00Z', authorizationHorizon: '2030-02-02T04:00:00Z', viewer: {serverId: pin.serverId, accountId: 'local-owner', profileId: 'main', authority: 'local', role: 'owner'}};
  let record: AppSessionRecord = {version: 1, revision: 7, selectedServerId: pin.serverId, servers: [{signIn: 'local', connection: {version: '1', selectionId: 'x'.repeat(32),
    logicalOrigin: origin, pin, routes: {paired: [origin]}, session: oldSession}}]};
  const sessions = new AppSessionStore({read: async () => record, change: async update => {record = update(record)!;}});
  const nextSession: LocalSession = {...oldSession, accessToken: 'new-access-' + suffix, sessionFamilyId: 'new-family-' + suffix,
    viewer: {...oldSession.viewer, role: 'account'}};
  const grant = {authority: 'local', serverId: pin.serverId, canManage: true, capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'local-owner', username: 'actual_owner', primaryProfileId: 'main', role: 'owner', revision: 2, disabled: false, allowedLibraries: [], hostedAccountId: 'approved-hosted-owner'},
    profiles: [{id: 'main', name: 'Main', primary: true, art: 'blue', position: 0, revision: 1, pinRevision: 1, pinRequired: false},
      {id: 'second', name: 'Second', primary: false, art: 'blue', position: 1, revision: 1, pinRevision: 1, pinRequired: false}], session: nextSession};
  const expected = {operationId: original.operationId, localGeneration: original.localGeneration, revision: '2'};
  const transactionId = 'actual_owner_transaction', registration = {id: 'ret_' + 'r'.repeat(43), origin: 'https://managed.example.test'};
  let handoff: NonNullable<Pending>['handoff'], firstOwnerStart: unknown;
  const sent: {path: string; method: string; body: any; authorization: string | null}[] = [], retired: string[] = [];
  let hook = async (_path: string) => {}, lost = false, failStorage = false;
  const stamp = (time: number) => new Date(time).toISOString().replace('.000Z', 'Z');
  const status = () => ({state: installed ? 'installed' : prepared ? 'prepared' : 'unconfigured', identity: {serverId: pin.serverId, localGeneration: cancelled && !installed ? String(BigInt(expected.localGeneration) + 1n) : expected.localGeneration},
    ...(prepared ? {operation: {...expected, revision: installed ? '5' : expected.revision}, accountId: 'account-unbound',
      approvalRequest: {purpose: 'switch', currentOwnerAccountId: '', previousOwnerRole: '', returnRegistrationId: '', operationId: expected.operationId, serverId: pin.serverId,
        publicKey: pin.publicKey, localGeneration: expected.localGeneration, name: original.name, expectedRevision: expected.revision}} : {}),
    installationAcknowledged: installed, approvalRequired: !installed, actions: prepared ? ['cancel', installed ? 'continue' : 'approve'] : ['prepare'],
    ...(installed ? {ownerContinuation: {transactionId, phase: cancelled ? 'cancelled' : 'installed', expiresAt: handoff!.expiresAt, returnRegistration: registration}} : {})});
  const fetcher: typeof fetch = async (input, init) => {
    const path = new URL(String(input)).pathname, body = init?.body ? JSON.parse(String(init.body)) : undefined;
    sent.push({path, method: init?.method ?? 'GET', body, authorization: new Headers(init?.headers).get('Authorization')});
    let response: unknown;
    if (path === '/v1/networking/claim/prepare') {assert.equal(body.purpose, 'switch'); prepared = true; response = status();}
    else if (path === '/v1/networking/claim') response = status();
    else if (path === '/v1/networking/claim/cancel') {
      assert.deepEqual(body.expected, {...expected, revision: installed ? '5' : expected.revision});
      cancelled = true; if (!installed) prepared = false; response = status();
    }
    else if (path === '/v1/networking/claim/web-approval') {
      if (firstOwnerStart) assert.deepEqual(body.ownerStart, firstOwnerStart, 'retry must freeze the original PKCE/state/installation');
      firstOwnerStart = body.ownerStart;
      const claim = {...original, address: origin, issuedAt: stamp(now - 1000), expiresAt: stamp(now + 599000)}, proof = signed(claim);
      const start = {...JSON.parse(vector.startPayload), claimHash: routeBase64(await webRouteCrypto.sha256(routeBytes(proof.payload))), address: origin,
        ...body.ownerStart, issuedAt: stamp(now), expiresAt: stamp(now + 299000)};
      handoff = {expected, binding: {pin, origin, hostedOrigin, operationId: expected.operationId, localGeneration: expected.localGeneration, ...body.ownerStart},
        transactionId, expiresAt: start.expiresAt, claim: proof, start: signed(start)};
      const code = 'claim_' + 'K'.repeat(43);
      response = {...status(), approvalUrl: hostedOrigin + '/claim#' + new URLSearchParams({code, operation: expected.operationId, server: original.name}), claimCode: code,
        ownerContinuation: {transactionId, phase: 'prepared', expiresAt: start.expiresAt, claim: handoff.claim, start: handoff.start}};
    } else if (path === '/v1/auth/portico-return/exchange') {
      assert.equal(saved, undefined, 'private journal is spent before exchange');
      assert.deepEqual(Object.keys(body).sort(), ['installationId', 'purpose', 'registrationId', 'serverChallenge', 'state', 'transactionId', 'verifier']);
      assert.equal(body.registrationId, registration.id); assert.equal(body.installationId, installation); assert.equal(body.purpose, 'switch');
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer ' + oldSession.accessToken);
      if (lost) throw new Error('lost owner response');
      response = grant;
    } else throw new Error('unexpected owner API path');
    await hook(path);
    return new Response(JSON.stringify(response), {headers: {'Content-Type': 'application/json'}});
  };
  const connection = {logicalOrigin: origin, pin, fetch: fetcher, getSnapshot: () => ({phase: 'ready'}), subscribe: () => () => {}, dispose: () => {},
    getMemory: () => ({paired: new Set([origin])}), setPersistence: () => {}};
  const api = new HttpLocalApi(origin, oldSession.accessToken, fetcher); api.useRoutes(connection as never);
  const boundAPI = new HttpLocalApi(origin, '', fetcher); boundAPI.useRoutes(connection as never);
  const ports: Ports = {
    journal: {assertAvailable: () => {if (!secure) throw new Error('secure storage unavailable');}, read: async () => saved,
      change: async update => {if (failStorage && saved?.handoff) throw new Error('storage unavailable'); saved = update(saved);}},
    sessions, environment: () => ({storage: sessions.connectionStorage(pin.serverId)}), installationId: () => installation, pageOrigin: () => origin, now: () => now,
    connect: (async () => ({api, connection})) as Ports['connect'],
    connectPortico: (async (_origin, _pin, source) => {
      assert.equal(_origin, origin, 'no credential/storage migration to the new HTTPS origin'); assert.equal(source.accountId, 'approved-hosted-owner');
      return {api: boundAPI, connection, hosted: {origin: hostedOrigin, accountId: source.accountId, key: {keyId: 'key', publicKey: pin.publicKey}}};
    }) as Ports['connectPortico'],
    retire: async (_api, session) => {retired.push(session.sessionFamilyId);},
  };
  const flow = new BrowserOwnerClaim(ports), callback = {href: origin + '/auth/portico-return', fragment: '#transaction=' + transactionId};
  return {flow, api, callback, grant, oldSession, nextSession, sent, retired, sessions, expected, registration, ports,
    prepare: () => flow.prepare(api, hostedOrigin), another: () => new BrowserOwnerClaim(ports), pending: () => saved,
    install: () => {installed = true;}, expire: () => {now += 301000;}, advance: () => {now += 2000;}, insecure: () => {secure = false;}, lose: () => {lost = true;},
    mutate: (change: (record: NonNullable<Pending>) => Pending) => {saved = change(saved!);},
    hook: (value: typeof hook) => {hook = value;}, failStorage: () => {failStorage = true;}};
}

test('owner initiation journals full signed binding before returning the actual existing approval URL', async t => {
  const f = await fixture(t), url = await f.prepare();
  assert.equal(f.pending()?.kind, 'owner'); assert.equal(f.pending()?.handoff?.transactionId, 'actual_owner_transaction');
  assert.equal(new URL(url).pathname, '/claim'); assert.equal(url.includes(f.pending()!.verifier), false);
  assert.equal(f.sent.some(value => value.path.endsWith('/prepare') && value.body.purpose === 'switch'), true);
  await f.another().prepare(f.api, original.audience);
  assert.equal(f.sent.filter(value => value.path.endsWith('/prepare')).length, 1);
});
test('insecure context refuses before private reads/writes or network', async t => {
  const f = await fixture(t); f.insecure(); await assert.rejects(f.prepare(), /secure storage/); assert.equal(f.sent.length, 0); assert.equal(f.pending(), undefined);
});
test('preparation captures the actual selected bearer instead of an API mutable bearer or recovery policy', async t => {
  const f = await fixture(t); f.api.setAccessToken('unrelated-bearer');
  f.api.setAuthRecovery({token: async () => {throw new Error('generic renewal must not run');}, recover: async () => {throw new Error('generic retry must not run');}});
  await f.prepare(); assert.ok(f.sent.every(value => value.authorization === 'Bearer ' + f.oldSession.accessToken));
});
for (const installed of [false, true]) test('explicit operation cancellation uses the latest guarded revision ' + (installed ? 'after install' : 'before install'), async t => {
  const f = await fixture(t); await f.prepare(); if (installed) f.install();
  await f.flow.cancelOperation(f.api); assert.equal(f.pending(), undefined);
  assert.equal(f.sent.filter(value => value.path.endsWith('/cancel')).length, 1);
  assert.equal(f.sent.some(value => value.path.endsWith('/exchange') || value.path === '/v1/servers/leave'), false);
  assert.equal((await f.sessions.selected())?.signIn, 'local', 'no local authority/kind rewrite by the client');
});
test('a transaction-only URL never selects owner without the actual protected owner KIND', async t => {
  const f = await fixture(t); await f.prepare(); f.install(); const count = f.sent.length;
  f.mutate(old => ({...old, kind: 'registered'}) as never);
  assert.equal(await f.flow.pendingReturn(), undefined); await assert.rejects(f.flow.finish(f.callback)); assert.equal(f.sent.length, count);
});
test('actual installed registration and one owner exchange admit the same owner account without profile promotion', async t => {
  const f = await fixture(t); await f.prepare(); f.install();
  assert.deepEqual(await f.flow.pendingReturn(), {kind: 'owner', transactionId: 'actual_owner_transaction'});
  const out = await f.another().finish(f.callback);
  assert.equal(out.result.accountScoped, true); assert.equal(out.result.session.viewer.role, 'account'); assert.equal(out.result.snapshot.profiles.length, 2);
  const selected = await f.sessions.selected(); assert.equal(selected?.signIn, 'portico'); assert.equal(selected?.connection.session.sessionFamilyId, f.nextSession.sessionFamilyId);
  assert.equal(f.sent.some(value => /\/me$|\/auth\/refresh|\/profiles\//.test(value.path)), false); assert.equal(f.pending(), undefined);
  await assert.rejects(f.another().finish(f.callback)); assert.equal(f.sent.filter(value => value.path.endsWith('/exchange')).length, 1);
});
test('literal localhost keeps the original protected context and uses only the genuine HTTPS registration id', async t => {
  const f = await fixture(t, 'http://localhost:32500'); await f.prepare(); f.install();
  await f.another().finish(f.callback);
  assert.equal((await f.sessions.selected())?.connection.logicalOrigin, 'http://localhost:32500');
});
test('a later Server challenge keeps its exact signed expiry without extending the earlier private deadline', async t => {
  const f = await fixture(t); f.hook(async path => {if (path.endsWith('/prepare')) f.advance();});
  await f.prepare(); const pending = f.pending()!;
  assert.equal(Date.parse(pending.expiresAt), pending.createdAt + 300000);
  assert.ok(Date.parse(pending.handoff!.expiresAt) > Date.parse(pending.expiresAt));
  f.install(); await f.flow.finish(f.callback);
});
for (const fragment of ['#transaction=wrong', '#transaction=actual_owner_transaction&transaction=actual_owner_transaction', '#transaction=actual_owner_transaction&state=not-allowed', '#code=src_fake&state=s&transaction=actual_owner_transaction'])
  test('owner callback refuses wrong or mixed fragment ' + fragment.split('&')[0], async t => {
    const f = await fixture(t); await f.prepare(); f.install(); const count = f.sent.length;
    await assert.rejects(f.flow.finish({...f.callback, fragment})); assert.equal(f.sent.length, count); assert.ok(f.pending());
  });
test('selection replacement during a late preparation cannot overwrite a successor journal', async t => {
  const f = await fixture(t);
  f.hook(async path => {if (path.endsWith('/web-approval')) {f.mutate(old => ({...old, attemptId: 'successor'})); await f.sessions.select(original.serverId);}});
  await assert.rejects(f.prepare()); assert.equal(f.pending()?.attemptId, 'successor');
});
test('idle controller cancellation cannot clear another attempt', async t => {
  const f = await fixture(t); await f.prepare(); const record = f.pending(); await f.another().cancel(); assert.equal(f.pending(), record);
  f.mutate(old => ({...old, attemptId: 'successor'})); await f.flow.cancel(); assert.equal(f.pending()?.attemptId, 'successor');
});
test('expired pending refuses before exchange or admission', async t => {
  const f = await fixture(t); await f.prepare(); f.install(); f.expire(); await assert.rejects(f.flow.finish(f.callback)); assert.equal(f.sent.some(value => value.path.endsWith('/exchange')), false);
  assert.equal(f.pending(), undefined);
});
test('return dispatch drops an expired or deselected owner pending without choosing it over an ordinary return', async t => {
  const f = await fixture(t); await f.prepare(); await f.sessions.select(original.serverId);
  assert.equal(await f.another().pendingReturn(), undefined); assert.equal(f.pending(), undefined);
});
test('post-storage expiry removes only this unexposed admission and retires its exact family', async t => {
  const f = await fixture(t); await f.prepare(); f.install();
  const flow = new BrowserOwnerClaim({...f.ports, remember: async (...args) => {await rememberNativeSession(...args); f.expire();}});
  await assert.rejects(flow.finish(f.callback)); assert.deepEqual(f.retired, [f.nextSession.sessionFamilyId]); assert.equal(await f.sessions.selected(), undefined);
});
test('lost response spends only this browser attempt and never retries or issues another claim', async t => {
  const f = await fixture(t); await f.prepare(); f.install(); f.lose(); await assert.rejects(f.flow.finish(f.callback), /lost owner/);
  await assert.rejects(f.another().finish(f.callback)); assert.equal(f.sent.filter(value => value.path.endsWith('/exchange')).length, 1);
  assert.equal((await f.sessions.selected())?.signIn, 'local'); assert.equal(f.pending(), undefined);
});
test('selection change after exchange retires the late actual family and preserves the new selection', async t => {
  const f = await fixture(t); await f.prepare(); f.install();
  f.hook(async path => {if (path.endsWith('/exchange')) await f.sessions.select(original.serverId);});
  await assert.rejects(f.flow.finish(f.callback)); assert.deepEqual(f.retired, [f.nextSession.sessionFamilyId]);
  assert.equal((await f.sessions.selected())?.connection.session.sessionFamilyId, f.oldSession.sessionFamilyId);
});
test('another account in an otherwise well-formed result is retired, never adopted', async t => {
  const f = await fixture(t); await f.prepare(); f.install(); f.grant.account.id = 'another-owner'; f.grant.session.viewer.accountId = 'another-owner';
  await assert.rejects(f.flow.finish(f.callback)); assert.deepEqual(f.retired, [f.nextSession.sessionFamilyId]); assert.equal((await f.sessions.selected())?.signIn, 'local');
});
test('a missing installed registration refuses rather than deriving one from the callback origin', async t => {
  const f = await fixture(t); await f.prepare();
  await assert.rejects(f.flow.finish(f.callback)); assert.equal(f.sent.some(value => value.path.endsWith('/exchange')), false);
});
