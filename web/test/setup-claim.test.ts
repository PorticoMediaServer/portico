import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createPrivateKey, randomUUID, sign} from 'node:crypto';
import {BrowserSetupClaim, type SetupClaimRecovery} from '../src/platform/setup-claim.ts';
import {HttpLocalApi, transportJSON, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import type {SetupDraft} from '@core/setup-onboarding.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';

type Ports = ConstructorParameters<typeof BrowserSetupClaim>[0];
type Pending = Awaited<ReturnType<Ports['journal']['read']>>;
const vector = JSON.parse(readFileSync(new URL('../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const original = {...JSON.parse(vector.claimPayload), purpose: 'setup'};
const key = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), Buffer.alloc(32, 0x42)]), format: 'der', type: 'pkcs8'});
function signed(value: unknown) {const bytes = Buffer.from(transportJSON(value)); return {payload: bytes.toString('base64url'), signature: sign(null, bytes, key).toString('base64url')};}
async function fixture(t: TestContext, origin = original.address) {
  const hostedOrigin = original.audience, installation = randomUUID(), suffix = randomUUID();
  setInstallationIdentity({installationId: installation, name: 'Browser', platform: 'web', app: 'portico', appVersion: '1'});
  t.after(() => setInstallationIdentity(undefined));
  const pin = {serverId: original.serverId, publicKey: original.publicKey, fingerprint: routeBase64(await webRouteCrypto.sha256(routeBytes(original.publicKey, 32)))};
  let now = Date.parse('2030-01-02T03:04:07Z'), saved: Pending, secure = true, installed = false, prepared = false, cancelled = false;
  const oldSession: LocalSession = {accessToken: 'old-access-' + suffix, refreshToken: routeBase64(new Uint8Array(32).fill(4)), sessionFamilyId: 'old-family-' + suffix, tokenGeneration: '1',
    expiresAt: '2030-01-02T04:00:00Z', authorizationHorizon: '2030-02-02T04:00:00Z', viewer: {serverId: pin.serverId, accountId: 'local-setup', profileId: 'main', authority: 'local', role: 'owner'}};
  let record: AppSessionRecord = {version: 1, revision: 7, servers: []};
  let draft: SetupDraft | undefined = {version: 1, serverId: pin.serverId, requestId: '11111111-2222-4333-8444-555555555555', setupToken: 'actual-protected-setup-secret', name: original.name, authMode: 'hosted'};
  const sessions = new AppSessionStore({read: async () => record, change: async update => {record = update(record)!;}});
  const nextSession: LocalSession = {...oldSession, accessToken: 'new-access-' + suffix, sessionFamilyId: 'new-family-' + suffix,
    viewer: {...oldSession.viewer, role: 'account'}};
  const grant = {authority: 'local', serverId: pin.serverId, canManage: true, capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'local-setup', username: 'actual_setup', primaryProfileId: 'main', role: 'owner', revision: 2, disabled: false, allowedLibraries: [], hostedAccountId: 'approved-hosted-setup'},
    profiles: [{id: 'main', name: 'Main', primary: true, art: 'blue', position: 0, revision: 1, pinRevision: 1, pinRequired: false},
      {id: 'second', name: 'Second', primary: false, art: 'blue', position: 1, revision: 1, pinRevision: 1, pinRequired: false}], session: nextSession};
  const expected = {operationId: original.operationId, localGeneration: original.localGeneration, revision: '2'};
  const transactionId = 'actual_setup_transaction', registration = {id: 'ret_' + 'r'.repeat(43), origin: 'https://managed.example.test'};
  let handoff: NonNullable<Pending>['handoff'], firstSetupStart: unknown, originalSetupStart: unknown, generation = expected.localGeneration;
  const sent: {path: string; method: string; body: any; authorization: string | null}[] = [], retired: string[] = [];
  let hook = async (_path: string) => {}, lost = false, failStorage = false;
  const stamp = (time: number) => new Date(time).toISOString().replace('.000Z', 'Z');
  // A cancel advances the server's local generation; a later prepare lives in it.
  const status = () => ({state: installed ? 'installed' : prepared ? 'prepared' : 'unconfigured', identity: {serverId: pin.serverId, localGeneration: generation},
    ...(prepared ? {operation: {...expected, localGeneration: generation, revision: installed ? '5' : expected.revision}, accountId: installed ? 'account-approved' : 'account-unbound',
      approvalRequest: {purpose: 'setup', currentOwnerAccountId: '', previousOwnerRole: '', returnRegistrationId: '', operationId: expected.operationId, serverId: pin.serverId,
        publicKey: pin.publicKey, localGeneration: generation, name: original.name, expectedRevision: expected.revision}} : {}),
    installationAcknowledged: installed, approvalRequired: prepared && !installed, actions: prepared ? ['cancel', installed ? 'continue' : 'approve'] : ['prepare'],
    ...(installed ? {setupContinuation: {transactionId, phase: cancelled ? 'cancelled' : 'installed', expiresAt: handoff!.expiresAt}} : {})});
  const fetcher: typeof fetch = async (input, init) => {
    const path = new URL(String(input)).pathname, body = init?.body ? JSON.parse(String(init.body)) : undefined;
    sent.push({path, method: init?.method ?? 'GET', body, authorization: new Headers(init?.headers).get('Authorization')});
    let response: unknown;
    if (path === '/v1/setup/resume') {assert.equal(body.requestId, draft!.requestId); response={serverId: pin.serverId, requestId: draft!.requestId, authMode: 'hosted', claimRequired: true, revision: '2'};}
    else if (path === '/v1/networking/claim/prepare') {assert.equal(body.purpose, 'setup'); prepared = true; response = status();}
    else if (path === '/v1/networking/claim') response = status();
    else if (path === '/v1/networking/claim/cancel') {
      assert.deepEqual(body.expected, {...expected, localGeneration: generation, revision: installed ? '5' : expected.revision});
      cancelled = true; if (!installed) {prepared = false; firstSetupStart = undefined; generation = String(BigInt(generation) + 1n);} response = status();
    }
    else if (path === '/v1/networking/claim/web-approval') {
      // A retry freezes the original PKCE/state/installation; only an installed setup whose start
      // lapsed (a slow certificate) renews it with a fresh one.
      if (firstSetupStart && !installed) assert.deepEqual(body.setupStart, firstSetupStart, 'retry must freeze the original PKCE/state/installation');
      if (originalSetupStart && installed) assert.notDeepEqual(body.setupStart, originalSetupStart, 'a renewal binds a fresh browser start');
      firstSetupStart = body.setupStart; originalSetupStart ??= body.setupStart;
      const claim = {...original, localGeneration: generation, address: origin, issuedAt: stamp(now - 1000), expiresAt: stamp(now + 599000)}, proof = signed(claim);
      const inherited=JSON.parse(vector.startPayload);
      const start = {kind: 'portico.setup-start.v1', audience: hostedOrigin, claimHash: routeBase64(await webRouteCrypto.sha256(routeBytes(proof.payload))),
        operationId: expected.operationId, serverId: pin.serverId, publicKey: pin.publicKey, localGeneration: generation, address: origin, purpose: 'setup',
        setupRequestId: draft!.requestId, setupRevision: '2', state: body.setupStart.state, codeChallenge: body.setupStart.codeChallenge, installationId: body.setupStart.installationId,
        serverChallenge: inherited.serverChallenge, issuedAt: stamp(now), expiresAt: stamp(now + 299000)};
      handoff = {expected: {...expected, localGeneration: generation}, binding: {pin, origin, hostedOrigin, operationId: expected.operationId, localGeneration: generation, setupRequestId: draft!.requestId, setupRevision: '2', ...body.setupStart},
        transactionId, expiresAt: start.expiresAt, claim: proof, start: signed(start)};
      const code = 'claim_' + 'K'.repeat(43);
      response = {...status(), approvalUrl: hostedOrigin + '/claim#' + new URLSearchParams({code, operation: expected.operationId, server: original.name}), claimCode: code,
        setupContinuation: {transactionId, phase: 'prepared', expiresAt: start.expiresAt, claim: handoff.claim, start: handoff.start}};
    } else if (path === '/v1/auth/portico-return/exchange') {
      assert.equal(saved, undefined, 'private journal is spent before exchange');
      assert.deepEqual(Object.keys(body).sort(), ['installationId', 'purpose', 'serverChallenge', 'state', 'transactionId', 'verifier']);
      assert.equal('registrationId' in body, false); assert.equal(body.installationId, installation); assert.equal(body.purpose, 'setup');
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer actual-protected-setup-secret');
      if (lost) throw new Error('lost setup response');
      response = grant;
    } else throw new Error('unexpected setup API path');
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
    draft: () => ({read: async () => draft, change: async update => {draft=update(draft);}}),
    sessions, environment: () => ({storage: sessions.connectionStorage(pin.serverId)}), installationId: () => installation, pageOrigin: () => origin, now: () => now,
    connect: (async () => ({api, connection})) as Ports['connect'],
    connectPortico: (async (_origin, _pin, source) => {
      assert.equal(_origin, origin, 'no credential/storage migration to the new HTTPS origin'); assert.equal(source.accountId, 'approved-hosted-setup');
      return {api: boundAPI, connection, hosted: {origin: hostedOrigin, accountId: source.accountId, key: {keyId: 'key', publicKey: pin.publicKey}}};
    }) as Ports['connectPortico'],
    retire: async (_api, session) => {retired.push(session.sessionFamilyId);},
  };
  const flow = new BrowserSetupClaim(ports), callback = {href: origin + '/auth/portico-return', fragment: '#transaction=' + transactionId};
  return {flow, api, callback, grant, oldSession, nextSession, sent, retired, sessions, expected, registration, ports,
    prepare: () => flow.prepare(api, hostedOrigin), another: () => new BrowserSetupClaim(ports), pending: () => saved,
    install: () => {installed = true;}, expire: () => {now += 301000;}, advance: () => {now += 2000;}, insecure: () => {secure = false;}, lose: () => {lost = true;},
    draft: () => draft, replaceDraft: () => {draft={...draft!, requestId: '11111111-2222-4333-8444-555555555556'};},
    replaceSelection: () => {record={...record, revision: record.revision+1};},
    mutate: (change: (record: NonNullable<Pending>) => Pending) => {saved = change(saved!);},
    hook: (value: typeof hook) => {hook = value;}, failStorage: () => {failStorage = true;}};
}


test('initial setup captures the actual pending request without inventing a Local owner', async t => {
 const f=await fixture(t); const url=await f.prepare();
 assert.equal(f.pending()?.kind,'setup'); assert.equal((await f.sessions.read()).servers.length,0);
 assert.ok(f.sent.every(v=>v.authorization==='Bearer actual-protected-setup-secret'));
 for (const secret of [f.pending()!.setupToken,f.pending()!.verifier,f.pending()!.state]) assert.equal(url.includes(secret),false);
 await f.another().prepare(f.api,original.audience);
 assert.equal(f.sent.filter(v=>v.path.endsWith('/prepare')).length,1);
});
test('setup refuses insecure storage before any private or network work', async t=>{
 const f=await fixture(t);f.insecure();await assert.rejects(f.prepare(),/secure storage/);assert.equal(f.sent.length,0);
});
test('setup completion consumes once and adopts only the real admitted owner', async t=>{
 const f=await fixture(t);await f.prepare();f.install();
 assert.deepEqual(await f.flow.pendingReturn(),{kind:'setup',transactionId:'actual_setup_transaction'});
 const out=await f.another().finish(f.callback);
 assert.equal(out.result.snapshot.account.hostedAccountId,'approved-hosted-setup');
 assert.equal((await f.sessions.selected())?.signIn,'portico');assert.equal(f.draft(),undefined);
 await assert.rejects(f.another().finish(f.callback));assert.equal(f.sent.filter(v=>v.path.endsWith('/exchange')).length,1);
});
for(const mode of ['draft','selection','expiry'] as const) test('setup '+mode+' replacement blocks an exchange',async t=>{
 const f=await fixture(t);await f.prepare();f.install();
 if(mode==='draft') f.replaceDraft();if(mode==='selection')f.replaceSelection();if(mode==='expiry')f.expire();
 await assert.rejects(f.flow.finish(f.callback));assert.equal(f.sent.some(v=>v.path.endsWith('/exchange')),false);assert.equal((await f.sessions.read()).servers.length,0);
});
for(const fragment of ['#transaction=wrong','#transaction=actual_setup_transaction&state=x','#transaction=actual_setup_transaction&purpose=setup']) test('malformed setup callback preserves pending',async t=>{
 const f=await fixture(t);await f.prepare();f.install();const pending=f.pending(),count=f.sent.length;
 await assert.rejects(f.flow.finish({...f.callback,fragment}));assert.equal(f.pending(),pending);assert.equal(f.sent.length,count);
});
test('setup loses its response without retrying an owner grant',async t=>{
 const f=await fixture(t);await f.prepare();f.install();f.lose();await assert.rejects(f.flow.finish(f.callback));
 await assert.rejects(f.another().finish(f.callback));assert.equal(f.sent.filter(v=>v.path.endsWith('/exchange')).length,1);assert.equal((await f.sessions.read()).servers.length,0);
});
test('late setup grant is retired and a replaced draft survives',async t=>{
 const f=await fixture(t);await f.prepare();f.install();f.hook(async path=>{if(path.endsWith('/exchange'))f.replaceDraft();});
 await assert.rejects(f.flow.finish(f.callback));assert.deepEqual(f.retired,[f.nextSession.sessionFamilyId]);assert.equal(f.draft()?.requestId,'11111111-2222-4333-8444-555555555556');
});
test('setup admission expiry removes only its new family',async t=>{
 const f=await fixture(t);await f.prepare();f.install();
 const flow=new BrowserSetupClaim({...f.ports,remember:async(...args)=>{await rememberNativeSession(...args);f.expire();}});
 await assert.rejects(flow.finish(f.callback));assert.deepEqual(f.retired,[f.nextSession.sessionFamilyId]);assert.equal(await f.sessions.selected(),undefined);assert.ok(f.draft());
});

// Auth1d accepting-recovery source vectors; intentionally not run per item.
async function recovery(t: TestContext) {
 const f=await fixture(t);let receipt:SetupClaimRecovery|undefined,live=true;
 const abort=new AbortController(),draft=f.draft()!;
 await f.flow.prepare(f.api,original.audience,{
  setup:{serverId:draft.serverId,requestId:draft.requestId,authMode:'hosted',claimRequired:true,revision:'2'},
  signal:abort.signal,isCurrent:()=>live,assertOriginal:async()=>{if(!live)throw new Error('retired original opening');},capture:value=>{receipt=value;},
 });
 assert.ok(receipt);
 return {...f,receipt,retire:()=>{live=false;abort.abort();}};
}
test('local recovery cancels only the original prepared claim and keeps its setup proof',async t=>{
 const f=await recovery(t),draft=f.draft();
 await f.receipt.useLocal();
 const commands=f.sent.filter(value=>value.path.endsWith('/cancel'));
 assert.equal(commands.length,1);assert.deepEqual(commands[0].body,{expected:f.expected});
 assert.equal(commands[0].authorization,'Bearer actual-protected-setup-secret');
 assert.equal(f.pending(),undefined);assert.deepEqual(f.draft(),draft);
 const before=f.sent.length;await assert.rejects(f.receipt.useLocal());assert.equal(f.sent.length,before);
});
for(const replacement of ['draft','selection','expiry','opening','installed'] as const) test('local recovery refuses '+replacement+' without cancelling a successor',async t=>{
 const f=await recovery(t);
 if(replacement==='draft')f.replaceDraft();if(replacement==='selection')f.replaceSelection();if(replacement==='expiry')f.expire();
 if(replacement==='opening')f.retire();if(replacement==='installed')f.install();
 const pending=f.pending();await assert.rejects(f.receipt.useLocal());
 assert.equal(f.sent.some(value=>value.path.endsWith('/cancel')),false);assert.equal(f.pending(),pending);assert.ok(f.draft());
});
test('a lost cancel response never clears the private receipt or borrows a new cancel target',async t=>{
 const f=await recovery(t),pending=f.pending(),draft=f.draft();
 f.hook(async path=>{if(path.endsWith('/cancel'))throw new Error('lost original cancel result');});
 await assert.rejects(f.receipt.useLocal());
 assert.equal(f.pending(),pending);assert.deepEqual(f.draft(),draft);
 await assert.rejects(f.receipt.useLocal());
 assert.equal(f.sent.filter(value=>value.path.endsWith('/cancel')).length,1);assert.equal(f.pending(),pending);
});

// A certificate issued after several attempts outlives the five-minute attempt. Back on the
// original setup page, continuing renews the start for the installed claim, and the setup
// return then admits the owner exactly as before: no second approval, no cleared state.
test('a slow installation renews the lapsed start and setup completes', async t => {
 const f = await fixture(t); await f.prepare(); const first = f.pending()!;
 f.install(); f.expire();
 const url = await f.prepare(), renewed = f.pending()!;
 assert.match(url, /\/claim#/);
 assert.notEqual(renewed.attemptId, first.attemptId); assert.notEqual(renewed.verifier, first.verifier);
 assert.equal(renewed.expected?.revision, '5', 'the renewal names the installed operation');
 assert.equal(renewed.setupRequestId, first.setupRequestId, 'the same setup, never a new one');
 assert.equal(f.sent.filter(v => v.path === '/v1/networking/claim/web-approval').length, 2);
 assert.equal(f.sent.some(v => v.path === '/v1/networking/claim/cancel'), false, 'an installed claim is never cancelled');
 const done = await f.flow.finish(f.callback);
 assert.equal(done.result.snapshot.account.role, 'owner');
 assert.equal(f.pending(), undefined);
});

test('an expired attempt whose claim was never approved restarts setup cleanly', async t => {
 const f = await fixture(t); await f.prepare(); const first = f.pending()!;
 f.expire();
 await f.prepare(); const fresh = f.pending()!;
 assert.equal(f.sent.filter(v => v.path === '/v1/networking/claim/cancel').length, 1, 'only this record\'s own operation is retired');
 assert.equal(f.sent.filter(v => v.path === '/v1/networking/claim/prepare').length, 2);
 assert.notEqual(fresh.attemptId, first.attemptId);
 assert.equal(f.draft()?.setupToken, 'actual-protected-setup-secret', 'the setup proof is kept');
});

// P2-59 on Contabo: back on the original page, Try again on the captured receipt after the
// certificate finally issued. It renews instead of throwing "pending setup changed".
test('Try again after a slow certificate renews from the original receipt', async t => {
 const f = await recovery(t); const first = f.pending()!;
 f.install(); f.expire();
 const url = await f.receipt.retry();
 assert.match(url, /\/claim#/);
 assert.notEqual(f.pending()!.attemptId, first.attemptId);
 assert.equal(f.sent.some(value => value.path.endsWith('/cancel')), false);
 // Use local still refuses a lapsed receipt: it never cancels on a stale record.
 await assert.rejects(f.receipt.useLocal());
});

// Review finding (liveness): a renewal whose Hosted call failed is retried at once with the same
// fresh attempt, never blocked until it expires.
test('a renewal retried after a failed Hosted call asks for the same renewal again', async t => {
 const f = await fixture(t); await f.prepare();
 f.install(); f.expire();
 let failures = 1;
 f.hook(async path => {if (path === '/v1/networking/claim/web-approval' && failures-- > 0) throw new Error('lost Hosted answer');});
 await assert.rejects(f.prepare()); const renewed = f.pending()!;
 assert.equal(renewed.expected?.revision, '5');
 const url = await f.prepare();
 assert.match(url, /\/claim#/); assert.equal(f.pending()!.attemptId, renewed.attemptId, 'the same fresh attempt, not another');
 await f.flow.finish(f.callback);
});
