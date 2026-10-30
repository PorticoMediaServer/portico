import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppServerSession, type AppSessionRecord} from '../../../packages/client-core/src/app-sessions.ts';
import {forgetStoredNativeServer} from '../src-next/platform/forget-server.ts';
import {nativeIdentityFailure, nativeRouteIdentityFailure} from '../src-next/platform/identity-failure.ts';

function server(id: string): AppServerSession {
  return {signIn: 'local', connection: {version: '1', selectionId: id.padEnd(32, '_'), logicalOrigin: `https://${id}.test`,
    pin: {serverId: id, publicKey: 'p', fingerprint: 'fingerprint-' + id},
    routes: {paired: [`https://${id}.test`, `http://192.168.1.20:32500`], lastGood: 'https://away.test'},
    session: {accessToken: 'synthetic-access-' + id, expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
      sessionFamilyId: 'family-' + id, tokenGeneration: '1', viewer: {serverId: id, accountId: 'membership-' + id, profileId: 'profile-' + id, authority: 'local', role: 'member'}}}};
}
function fixture() {
  const a = server('a'), b = server('b');
  let record: AppSessionRecord | undefined = {version: 1, revision: 1, selectedServerId: 'a', servers: [a, b]};
  let failWrite = false, current = true;
  const sessions = new AppSessionStore({read: async () => structuredClone(record), change: async update => {
    const next = update(structuredClone(record));
    if (failWrite) throw new Error('synthetic_secure_write_failed');
    record = structuredClone(next);
  }});
  const certificates: {id: string; origins: readonly string[]}[] = [], retired: AppServerSession[][] = [];
  const ports = {sessions, current: () => current, forgetCertificates: async (id: string, origins: readonly string[]) => {certificates.push({id, origins});},
    retireRoutes: (values: readonly AppServerSession[]) => {retired.push([...values]);}};
  const binding = {serverId: 'a', familyId: 'family-a', fingerprint: 'fingerprint-a'};
  return {a, b, binding, ports, certificates, retired, sessions, setCurrent: (value: boolean) => {current = value;}, failWrite: () => {failWrite = true;}};
}

test('offline Forget removes only the captured family and all its route memory after durable certificate acknowledgement', async () => {
  const f = fixture(), previousFetch = globalThis.fetch;
  globalThis.fetch = async () => {throw new Error('Forget must never make a network request');};
  try {
    await forgetStoredNativeServer(f.binding, f.ports);
    assert.deepEqual(f.certificates, [{id: 'a', origins: ['https://a.test', 'http://192.168.1.20:32500', 'https://away.test']}]);
    assert.deepEqual((await f.sessions.read()).servers, [f.b]);
    assert.equal((await f.sessions.read()).selectedServerId, undefined);
    assert.deepEqual(f.retired[0]?.map(value => value.connection.pin.serverId), ['a', 'a']);
    assert.ok(f.retired[0]?.every(value => value.connection.session.sessionFamilyId === 'family-a'));
  } finally {globalThis.fetch = previousFetch;}
});

test('a certificate-store failure retains the original sign-in and does not retire any route', async () => {
  const f = fixture(), original = await f.sessions.read();
  f.ports.forgetCertificates = async () => {throw new Error('synthetic_certificate_write_failed');};
  await assert.rejects(forgetStoredNativeServer(f.binding, f.ports), /synthetic_certificate_write_failed/);
  assert.deepEqual(await f.sessions.read(), original); assert.deepEqual(f.retired, []);
});

test('a stale server family or fingerprint performs no certificate mutation', async () => {
  for (const stale of [{familyId: 'old-family'}, {fingerprint: 'old-fingerprint'}, {serverId: 'removed'}]) {
    const f = fixture();
    await assert.rejects(forgetStoredNativeServer({...f.binding, ...stale}, f.ports), {code: 'cancelled'});
    assert.deepEqual(f.certificates, []); assert.deepEqual(f.retired, []);
    assert.equal((await f.sessions.read()).servers.length, 2);
  }
});

test('a replacement family arriving during native certificate cleanup survives late Forget', async () => {
  const f = fixture();
  f.ports.forgetCertificates = async () => {
    const expectedRevision = (await f.sessions.read()).revision;
    await f.sessions.connectionStorage('a', {signIn: 'local', expectedRevision}).change(value => ({...value!,
      selectionId: 'new'.padEnd(32, '_'), session: {...value!.session, sessionFamilyId: 'replacement-family'}}));
  };
  await assert.rejects(forgetStoredNativeServer(f.binding, f.ports), {code: 'cancelled'});
  assert.equal((await f.sessions.selected())?.connection.session.sessionFamilyId, 'replacement-family');
  assert.equal((await f.sessions.read()).servers.length, 2); assert.deepEqual(f.retired, []);
});

test('authority retirement while native cleanup awaits cannot remove a sign-in', async () => {
  const f = fixture(), before = await f.sessions.read();
  f.ports.forgetCertificates = async () => {f.setCurrent(false);};
  await assert.rejects(forgetStoredNativeServer(f.binding, f.ports), {code: 'cancelled'});
  assert.deepEqual(await f.sessions.read(), before); assert.deepEqual(f.retired, []);
});

test('a failed atomic session write leaves its exact family available for retry', async () => {
  const f = fixture(), before = await f.sessions.read(); f.failWrite();
  await assert.rejects(forgetStoredNativeServer(f.binding, f.ports), /synthetic_secure_write_failed/);
  assert.equal(f.certificates.length, 1); assert.deepEqual(await f.sessions.read(), before); assert.deepEqual(f.retired, []);
});

test('identity failure view receives a saved name and exact family, never transport text or a credential', () => {
  const saved = {serverId: 'a', familyId: 'family-a', fingerprint: 'fingerprint-a', name: 'Synthetic library', signIn: 'local' as const};
  const changed = nativeIdentityFailure({code: 'identity_mismatch', message: 'untrusted diagnostic'}, saved)!;
  assert.equal(changed.changed, true); assert.deepEqual(changed.server, saved);
  assert.equal(JSON.stringify(changed).includes('untrusted diagnostic'), false);
  assert.equal(nativeIdentityFailure({code: 'certificate_untrusted'}, saved)?.changed, false);
  assert.equal(nativeIdentityFailure({code: 'identity_mismatch'}, {...saved, signIn: 'portico'})?.changed, false);
  assert.equal(nativeIdentityFailure({code: 'route_unavailable'}, saved), undefined);
  assert.equal(nativeIdentityFailure({code: 'identity_mismatch'}), undefined);
});

test('only terminal identity route outcomes replace the private runtime', () => {
  const saved = {serverId: 'a', familyId: 'family-a', fingerprint: 'fingerprint-a', name: 'Synthetic library', signIn: 'local' as const};
  assert.equal(nativeRouteIdentityFailure({phase: 'identity_mismatch'}, saved)?.changed, true);
  assert.equal(nativeRouteIdentityFailure({phase: 'unconfirmed', errorCode: 'certificate_untrusted'}, saved)?.code, 'certificate_untrusted');
  assert.equal(nativeRouteIdentityFailure({phase: 'unconfirmed', errorCode: 'untrusted transport text'}, saved)?.code, 'certificate_unverified');
  for (const phase of ['idle', 'probing', 'ready', 'offline']) assert.equal(nativeRouteIdentityFailure({phase}, saved), undefined);
});
