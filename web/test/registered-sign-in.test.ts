import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowserRegisteredSignIn} from '../src/platform/registered-sign-in.ts';
import {routeBase64} from '@core/route-identity.ts';
import type {ReturnPending, RegisteredSignIn} from '@core/same-tab-return.ts';
import type {DirectSignIn} from '@core/profile-management.ts';

type Ports = ConstructorParameters<typeof BrowserRegisteredSignIn>[0];
type Pending = Awaited<ReturnType<Ports['journal']['read']>>;
const origin = 'https://server.example', hostedOrigin = 'https://accounts.example';
const installation = 'b938fb7e-54a0-425b-904e-f896ad76455a';
async function fixture() {
  const raw = new Uint8Array(32).fill(17), prefix = new TextEncoder().encode('portico.server.identity.v1\0');
  const bytes = new Uint8Array(prefix.length + raw.length); bytes.set(prefix); bytes.set(raw, prefix.length);
  const hash = async (data: Uint8Array) => routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(data))));
  const pin = {serverId: 'srv_' + await hash(bytes), publicKey: routeBase64(raw), fingerprint: await hash(raw)};
  const pending: ReturnPending = {serverId: pin.serverId, purpose: 'sign-in', claimOperationId: 'actual_installed_operation',
    registrationId: 'ret_' + 'r'.repeat(43), registrationOrigin: origin, state: 's'.repeat(43), verifier: 'v'.repeat(43),
    serverChallenge: 'c'.repeat(43), installationId: installation, createdAt: Date.now(), moveTicket: 'm'.repeat(43)};
  let saved: Pending, revision = 7, currentInstallation = installation, grant: DirectSignIn = {
    kind: 'signed-in', accountScoped: true, passwordChangeRequired: false,
    snapshot: {authority: 'local', serverId: pin.serverId, canManage: true,
      capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
      account: {id: 'server-account', username: 'person', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: [], hostedAccountId: 'actual-hosted-account'},
      profiles: [{id: 'main', name: 'Main', primary: true, art: 'blue', position: 0, revision: 1, pinRevision: 1, pinRequired: true}]},
    session: {accessToken: 'server-access', refreshToken: Buffer.alloc(32, 8).toString('base64url'), sessionFamilyId: 'new-server-family', tokenGeneration: '1', installationId: installation,
      expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId: pin.serverId, accountId: 'server-account', profileId: 'main', role: 'account'}},
  };
  let afterFinalWrite: (() => void) | undefined;
  let producerHeadroom = 299_000;
  const calls: string[] = [], retired: string[] = [], exposed: RegisteredSignIn[] = [];
  const connection = {pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {calls.push('dispose-prepared');}};
  const api = {baseUrl: origin, getRouteConnection: () => connection, setAccessToken: () => {},
    request: async (path: string, method: string, body: unknown) => {
      assert.equal(path, '/v1/auth/two-factor/challenge'); assert.equal(method, 'POST');
      assert.deepEqual(body, {token: 'actual-server-challenge', code: '123456'});
      calls.push('factor'); if (gate) await gate;
      assert.equal(grant.kind, 'signed-in');
      if (grant.kind !== 'signed-in') throw new Error('Expected a genuine factor grant.');
      return {...grant.snapshot, session: grant.session, passwordChangeRequired: grant.passwordChangeRequired};
    }} as never;
  const boundConnection = {dispose: () => {calls.push('dispose-bound');}};
  let gate: Promise<void> | undefined, resolveGate: (() => void) | undefined;
  const storage = {read: async () => undefined, change: async () => {}};
  const ports: Ports = {
    journal: {assertAvailable: () => {calls.push('secure-context');}, read: async () => saved, change: async update => {saved = update(saved);if (saved?.expiresAt) afterFinalWrite?.();}},
    sessions: {read: async () => ({version: 1, revision, servers: []}), connectionStorage: (_id, admission) => {assert.equal(admission?.expectedRevision, 7); calls.push('admission'); return storage;}},
    environment: () => ({storage}), installationId: () => currentInstallation, pageOrigin: () => origin,
    startURL: value => {assert.equal(saved?.pending?.verifier, pending.verifier); exposed.push(value); return hostedOrigin + '/#canonical-builder-result';},
    prepare: (async (_api, store) => {
      if (gate) await gate;
      await store.change(() => pending);
      return {start: {serverId: pin.serverId, purpose: 'sign-in', claimOperationId: pending.claimOperationId, registrationId: pending.registrationId,
        state: pending.state, codeChallenge: 'p'.repeat(43), serverChallenge: pending.serverChallenge, installationId: pending.installationId},
        registration: {id: pending.registrationId, origin}, hostedOrigin, expiresAt: new Date(pending.createdAt + producerHeadroom).toISOString()};
    }) as Ports['prepare'],
    connect: (async () => {calls.push('connect'); return {api, connection};}) as Ports['connect'],
    consume: (async (_api, store) => {assert.equal(saved, undefined, 'the durable journal is spent before the request'); assert.equal((await store.read())?.state, pending.state); calls.push('exchange'); if (gate) await gate; return grant;}) as Ports['consume'],
    connectPortico: (async (_origin, _pin, source) => {assert.equal(source.accountId, 'actual-hosted-account'); calls.push('route-key'); return {api, connection: boundConnection, hosted: {origin: hostedOrigin, accountId: source.accountId, key: {keyId: 'key', publicKey: pin.publicKey}}};}) as Ports['connectPortico'],
    remember: async () => {calls.push('remember');}, retire: async (_api, session) => {retired.push(session.sessionFamilyId);},
  };
  const flow = new BrowserRegisteredSignIn(ports);
  const callback = {href: origin + '/auth/portico-return', fragment: '#code=src_' + 'a'.repeat(43) + '&state=' + pending.state + '&transaction=srt_' + 't'.repeat(43)};
  const prepare = () => flow.prepare(api, origin, hostedOrigin, installation, {moveTicket: pending.moveTicket});
  return {flow, prepare, callback, calls, retired, exposed, saved: () => saved, another: () => new BrowserRegisteredSignIn(ports), api,
    afterFinalWrite: (run: () => void) => {afterFinalWrite = run;},
    ahead: (skew: number) => {producerHeadroom = 300_000 + skew;},
    revise: () => {revision++;}, changeInstallation: () => {currentInstallation = 'changed-installation'.padEnd(43, '_');},
    mutate: (update: (old: NonNullable<Pending>) => Pending) => {saved = update(saved!);},
    hold: () => {gate = new Promise(resolve => {resolveGate = resolve;});}, release: () => {resolveGate?.(); gate = undefined;},
    grant: (value: DirectSignIn) => {grant = value;}, answer: () => grant};
}
test('preparation protects verifier/move proof and captures actual UUID/revision before exposing only canonical RegisteredSignIn', async () => {
  const f = await fixture(); assert.equal(await f.prepare(), hostedOrigin + '/#canonical-builder-result');
  assert.equal(f.saved()?.expectedRevision, 7); assert.equal(f.saved()?.pending?.installationId, installation);
  assert.equal(f.exposed[0].start.claimOperationId, 'actual_installed_operation');
  assert.doesNotMatch(JSON.stringify(f.exposed), /verifier|moveTicket|server-access|server-refresh/);
});
test('cancelled preparation cannot leave a late durable pending record', async () => {
  const f = await fixture(); f.hold(); const work = f.prepare(); await new Promise(resolve => setImmediate(resolve));
  f.flow.cancel(); f.release(); await assert.rejects(work, {code: 'cancelled'}); assert.equal(f.saved(), undefined);
});
test('leaving after a completed preparation spends only its own pending before another controller can return', async () => {
  const f = await fixture(); await f.prepare(); assert.ok(f.saved());
  await f.flow.cancel(); assert.equal(f.saved(), undefined);
  await assert.rejects(f.another().finish(f.callback), {code: 'invalid_return'});
  assert.equal(f.calls.includes('exchange'), false);
});
test('an old or idle controller cancellation cannot spend a successor pending attempt', async () => {
  const f = await fixture(); await f.prepare();
  const successor = f.another(); await successor.prepare(f.api, origin, hostedOrigin, installation);
  const actual = f.saved(); await f.flow.cancel(); assert.equal(f.saved(), actual);
  await f.another().cancel(); assert.equal(f.saved(), actual);
  await successor.finish(f.callback); assert.equal(f.calls.includes('remember'), true);
});
test('a changed app selection prevents handoff and removes only this pending attempt', async () => {
  const f = await fixture(); f.hold(); const work = f.prepare(); await new Promise(resolve => setImmediate(resolve));
  f.revise(); f.release(); await assert.rejects(work, {code: 'cancelled'}); assert.equal(f.saved(), undefined);
});
test('normal completion spends the journal once and durably admits the exact server/account before returning', async () => {
  const f = await fixture(); await f.prepare(); await f.flow.finish(f.callback);
  assert.deepEqual(f.calls.filter(value => value !== 'secure-context'), ['connect', 'exchange', 'route-key', 'admission', 'remember', 'dispose-prepared']);
  assert.deepEqual(f.retired, []); await assert.rejects(f.flow.finish(f.callback), {code: 'invalid_return'});
});
test('Server MFA remains private after the one-use return and only its actual completion admits the account', async () => {
  const f = await fixture(), signedIn = f.answer(); await f.prepare();
  f.grant({kind: 'challenge', token: 'actual-server-challenge', expiresAt: new Date(Date.now() + 120000).toISOString()});
  const challenge = await f.flow.finish(f.callback);
  assert.equal(challenge.result.kind, 'challenge'); assert.equal(f.saved(), undefined);
  assert.equal(f.calls.includes('remember'), false); assert.equal(f.calls.includes('dispose-prepared'), false);
  f.grant(signedIn); const completed = await f.flow.completeChallenge('123456');
  assert.equal(completed.result.kind, 'signed-in'); assert.equal(f.calls.filter(value => value === 'exchange').length, 1);
  assert.equal(f.calls.filter(value => value === 'factor').length, 1); assert.equal(f.calls.includes('remember'), true);
  await assert.rejects(f.flow.completeChallenge('123456'), {code: 'invalid_return'});
});
for (const change of ['cancel', 'revision'] as const) test('a late factor grant after ' + change + ' is retired without adoption', async () => {
  const f = await fixture(), signedIn = f.answer(); await f.prepare();
  f.grant({kind: 'challenge', token: 'actual-server-challenge', expiresAt: new Date(Date.now() + 120000).toISOString()});
  await f.flow.finish(f.callback); f.grant(signedIn); f.hold();
  const work = f.flow.completeChallenge('123456');
  while (!f.calls.includes('factor')) await new Promise(resolve => setImmediate(resolve));
  if (change === 'cancel') await f.flow.cancel(); else f.revise();
  f.release(); await assert.rejects(work, {code: 'cancelled'});
  assert.deepEqual(f.retired, ['new-server-family']); assert.equal(f.calls.includes('remember'), false);
  assert.equal(f.calls.filter(value => value === 'dispose-prepared').length, 1);
  await assert.rejects(f.flow.completeChallenge('123456'), {code: 'invalid_return'});
});
for (const change of ['revision', 'installation', 'expired', 'owner-fragment'] as const) test('callback refuses ' + change + ' before a server exchange', async () => {
  const f = await fixture(); await f.prepare();
  if (change === 'revision') f.revise();
  if (change === 'installation') f.changeInstallation();
  if (change === 'expired') f.mutate(old => ({...old, expiresAt: '2000-01-01T00:00:00Z'}));
  await assert.rejects(f.flow.finish(change === 'owner-fragment' ? {...f.callback, fragment: '#transaction=owner'} : f.callback));
  assert.equal(f.calls.includes('exchange'), false); assert.equal(f.saved(), undefined);
});
test('a grant completed after cancellation retires only its captured family and never becomes a selected session', async () => {
  const f = await fixture(); await f.prepare(); f.hold(); const work = f.flow.finish(f.callback);
  while (!f.calls.includes('exchange')) await new Promise(resolve => setImmediate(resolve));
  f.flow.cancel(); f.release(); await assert.rejects(work, {code: 'cancelled'});
  assert.deepEqual(f.retired, ['new-server-family']); assert.equal(f.calls.includes('remember'), false);
});
test('a selection changed while exchanging cannot publish the returned server family', async () => {
  const f = await fixture(); await f.prepare(); f.hold(); const work = f.flow.finish(f.callback);
  while (!f.calls.includes('exchange')) await new Promise(resolve => setImmediate(resolve));
  f.revise(); f.release(); await assert.rejects(work, {code: 'cancelled'});
  assert.deepEqual(f.retired, ['new-server-family']); assert.equal(f.calls.includes('route-key'), false);
});
test('a later prepare in the same tab atomically supersedes another controller before its delayed response', async () => {
  const f = await fixture(); f.hold(); const earlier = f.prepare();
  await new Promise(resolve => setImmediate(resolve)); const first = f.saved()?.attemptId;
  const later = f.another().prepare(f.api, origin, hostedOrigin, installation);
  await new Promise(resolve => setImmediate(resolve)); assert.notEqual(f.saved()?.attemptId, first);
  f.release();
  const results = await Promise.allSettled([earlier, later]);
  assert.equal(results[0].status, 'rejected'); assert.equal(results[1].status, 'fulfilled');
  assert.equal(f.exposed.length, 1); assert.ok(f.saved()?.expiresAt);
});
for (const createdAt of [undefined, null, NaN, Infinity, 'not-a-local-timestamp', Date.now() + 120000]) {
  test('a corrupt or future local journal time is spent without any server exchange: ' + String(createdAt), async () => {
    const f = await fixture(); await f.prepare();
    f.mutate(old => ({...old, pending: {...old.pending!, createdAt: createdAt as number}}));
    await assert.rejects(f.flow.finish(f.callback), {code: 'invalid_return'});
    assert.equal(f.saved(), undefined); assert.equal(f.calls.includes('connect'), false); assert.equal(f.calls.includes('exchange'), false);
  });
}

test('a signed prepare that expires during its final private write never yields a handoff URL', async t => {
  const f = await fixture(), future = Date.now() + 600000;
  f.afterFinalWrite(() => {t.mock.method(Date, 'now', () => future);});
  await assert.rejects(f.prepare());
  assert.equal(f.saved(), undefined, 'only this expired attempt is removed');
  assert.equal(f.calls.includes('exchange'), false);
  assert.equal(f.calls.includes('remember'), false);
  assert.deepEqual(f.retired, []);
});

/** Same real Chromium trace: the signed prepare answered while this browser's
 * clock trailed the Server, so the producer's expiry sat more than one lifetime
 * past the browser-recorded createdAt and the callback refused a genuine return. */
test('a Server clock slightly ahead still completes the one-use return inside the bounded skew', async () => {
  for (const skew of [1, 5_000, 60_000]) {
    const f = await fixture(); f.ahead(skew); await f.prepare();
    assert.equal(f.saved()?.expiresAt, new Date(f.saved()!.pending!.createdAt + 300_000 + skew).toISOString());
    await f.flow.finish(f.callback);
    assert.equal(f.calls.filter(value => value === 'exchange').length, 1);
    assert.equal(f.calls.includes('remember'), true);
    assert.equal(f.saved(), undefined, 'the journal is still spent once');
  }
});
test('one millisecond past the bounded skew is refused before any server exchange', async () => {
  const f = await fixture(); f.ahead(60_001); await f.prepare();
  await assert.rejects(f.flow.finish(f.callback), {code: 'invalid_return'});
  assert.equal(f.calls.includes('connect'), false); assert.equal(f.calls.includes('exchange'), false);
  assert.equal(f.saved(), undefined);
});
test('an expiry this browser already reached is refused inside the same bounded window', async t => {
  const f = await fixture(); f.ahead(5_000); await f.prepare();
  const reached = Date.parse(f.saved()!.expiresAt);
  t.mock.method(Date, 'now', () => reached);
  await assert.rejects(f.flow.finish(f.callback), {code: 'invalid_return'});
  assert.equal(f.calls.includes('exchange'), false); assert.equal(f.calls.includes('remember'), false);
});
