import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
import {purgeStaleRoutes, staleDirectRoute, withoutStaleRoutes} from '../src-next/platform/stale-routes.ts';

// E-mobile-01 ruling (Claude, 29 Oct): nothing between an accepted profile pick and Home waits on a
// stored or retired address, and ending a replaced session is queued, bounded and retried.

const fresh = 'https://203-0-113-7.ptc-abcdefghijklmnopqrsa.portico.direct:32500';
test('retired c- direct names are stale; IP-encoded portico.direct names and plain hosts are not', () => {
  for (const url of ['https://c-abc123.getportico.net', 'https://c4-abc.example.net:443', 'https://c6-9f.example.net']) assert.equal(staleDirectRoute(url), true, url);
  for (const url of [fresh, 'https://media.example.com', 'http://192.168.1.20:32500', 'not a url']) assert.equal(staleDirectRoute(url), false, url);
});

test('stored route evidence forgets only stale names, and an untouched record keeps its identity', () => {
  const routes = {paired: ['https://c-old.getportico.net', fresh], lastGood: 'https://c-old.getportico.net',
    current: {payload: 'https://c4-x.getportico.net'} as never, previous: {payload: fresh} as never};
  const cleaned = withoutStaleRoutes(routes);
  assert.deepEqual(cleaned.paired, [fresh]);
  assert.equal(cleaned.lastGood, undefined);
  assert.equal(cleaned.current, undefined, 'a signed document naming a stale host is dropped (it cannot be edited)');
  assert.equal(cleaned.previous, routes.previous);
  const clean = {paired: [fresh], lastGood: fresh};
  assert.equal(withoutStaleRoutes(clean), clean);
  const record = {version: 1, revision: 4, selectedServerId: 's', servers: [{signIn: 'local', connection: {routes: clean}}]} as never;
  assert.equal(purgeStaleRoutes(record), record, 'nothing to forget: no write');
  const stale = {version: 1, revision: 4, servers: [{signIn: 'local', connection: {routes}}]} as never as {revision: number; servers: {connection: {routes: typeof routes}}[]};
  const purged = purgeStaleRoutes(stale as never) as never as typeof stale;
  assert.equal(purged.revision, 4, 'selection and revision are unchanged');
  assert.deepEqual(purged.servers[0]!.connection.routes.paired, [fresh]);
});

async function revocation(answer: (attempt: number, signal?: AbortSignal) => Promise<void>) {
  const memory = new Map<string, unknown>();
  const requests: {token: string; path: string}[] = [];
  let attempts = 0;
  class FakeApi {
    token: string; constructor(_origin: string, token: string) {this.token = token;}
    useRoutes() {} setAccessToken(token: string) {this.token = token;}
    async request(path: string, _method: string, _body: unknown, signal?: AbortSignal) {
      requests.push({token: this.token, path}); attempts++;
      // The queue must already hold the credential before the first try leaves the device.
      assert.ok(Array.isArray(memory.get('accounts.revocations.v1')), 'queued before the attempt');
      await answer(attempts, signal);
    }
  }
  const module = await screenModule(new URL('../src-next/platform/session-revocation.ts', import.meta.url), {
    react: {default: {}}, '../../../../packages/client-core/src': {HttpLocalApi: FakeApi},
    '../../../../packages/contracts/src/routes': {routePath: () => '/v1/sessions/current'},
    '../../../../packages/client-core/src/server-connections': {refreshStoredSession: async () => ({accessToken: 'renewed'})},
    './queue-storage': {NativeQueueStore: class {async change(key: string, update: (raw: unknown) => {record: unknown; value: unknown}) {
      const result = update(memory.get(key)); if (result.record === null) memory.delete(key); else memory.set(key, result.record); return result.value;}}},
    './server-connections': {nativeNetworkFetch: {}, nativeServerEnvironment: () => ({})},
  }) as typeof import('../src-next/platform/session-revocation.ts');
  return {module, memory, requests};
}
const record = (family: string, viewer: {profileId?: string; horizonMs?: number; serverId?: string} = {}) => ({version: '1', logicalOrigin: fresh, pin: {serverId: viewer.serverId ?? 'srv'}, routes: {paired: []},
  session: {sessionFamilyId: family, accessToken: `old-${family}`, refreshToken: 'r', expiresAt: new Date(Date.now() + 600_000).toISOString(),
    authorizationHorizon: new Date(Date.now() + (viewer.horizonMs ?? 86_400_000)).toISOString(), viewer: {accountId: 'acct', profileId: viewer.profileId ?? family}}}) as never;
const live = {pin: {serverId: 'srv'}, logicalOrigin: fresh} as never;
const failure = (status: number) => Object.assign(new Error('no'), {status});
const settle = () => new Promise(resolve => setTimeout(resolve, 5));

test('a replaced session is queued first, revoked over the live route, and removed once the server confirms', async () => {
  const {module, memory, requests} = await revocation(async () => {});
  await module.revokeInBackground(record('a'), live);
  assert.deepEqual(requests, [{token: 'old-a', path: '/v1/sessions/current'}]);
  assert.equal(memory.has('accounts.revocations.v1'), false, 'confirmed: the last copy of the credential is gone');
});

test('a failed revocation stays queued and is retried until the server ends it', async () => {
  let failing = true;
  const {module, memory} = await revocation(async () => {if (failing) throw failure(503);});
  await module.revokeInBackground(record('b'), live);
  assert.equal((memory.get('accounts.revocations.v1') as unknown[]).length, 1, 'kept for the next launch');
  assert.equal(await module.drainRevocations('srv', live), 0, 'still failing: still queued');
  failing = false;
  assert.equal(await module.drainRevocations('srv', live), 1);
  assert.equal(memory.has('accounts.revocations.v1'), false);
});

test('a server that no longer knows the session counts as done; other servers are left for their own route', async () => {
  const {module, memory} = await revocation(async () => {throw failure(401);});
  await module.revokeInBackground(record('c'), live);
  assert.equal(memory.has('accounts.revocations.v1'), false, '401/404: already gone');
  await module.revokeInBackground(record('d', {serverId: 'other'}), undefined);
  assert.equal(await module.drainRevocations('srv', live), 0, 'only this server is retried over this route');
  assert.equal((memory.get('accounts.revocations.v1') as unknown[]).length, 1);
});

test('a dead route never holds anything: each try is bounded', async () => {
  const {module, memory} = await revocation((_attempt, signal) => new Promise((_resolve, reject) => {signal?.addEventListener('abort', () => reject(failure(0)), {once: true});}));
  const started = Date.now();
  await assert.rejects(module.revokeOverLiveRoute(record('e'), live, 60));
  assert.ok(Date.now() - started < 1000, 'aborted at its bound');
  // The background form returns to its caller at once and keeps the credential queued.
  const background = module.revokeInBackground(record('f'), live);
  await settle();
  assert.equal((memory.get('accounts.revocations.v1') as unknown[]).length, 1);
  void background;
});

test('a queued credential is dropped once it can no longer be used, so nothing is retried forever', async () => {
  const {module, memory} = await revocation(async () => {throw failure(503);});
  await module.revokeInBackground(record('g', {horizonMs: 30}), live);
  assert.equal((memory.get('accounts.revocations.v1') as unknown[]).length, 1);
  await new Promise(resolve => setTimeout(resolve, 60));
  assert.equal(await module.drainRevocations('srv', live), 0);
  assert.equal(memory.has('accounts.revocations.v1'), false, 'its own expiry passed: the last copy is deleted');
});

test('the queue holds one entry per server and profile, and at most eight', async () => {
  const {module, memory} = await revocation(async () => {throw failure(503);});
  await module.revokeInBackground(record('h1', {profileId: 'same'}), undefined);
  await module.revokeInBackground(record('h2', {profileId: 'same'}), undefined);
  const one = memory.get('accounts.revocations.v1') as {record: {session: {sessionFamilyId: string}}}[];
  assert.deepEqual(one.map(entry => entry.record.session.sessionFamilyId), ['h2'], 'a newer replacement supersedes the older one');
  for (let i = 0; i < 10; i++) await module.revokeInBackground(record(`p${i}`), undefined);
  const capped = memory.get('accounts.revocations.v1') as {record: {session: {sessionFamilyId: string}}}[];
  assert.equal(capped.length, module.REVOCATION_QUEUE_CAP);
  assert.equal(capped.at(-1)!.record.session.sessionFamilyId, 'p9', 'the oldest are dropped first');
});

test('a queued credential can only ever be used to end its own session', async () => {
  const {module, requests} = await revocation(async () => {});
  // Nothing exported reads the queue back: only queue, retry and the one revoke call exist.
  assert.deepEqual(Object.keys(module).sort(), ['REVOCATION_QUEUE_CAP', 'REVOCATION_TIMEOUT_MS', 'drainRevocations', 'revokeInBackground', 'revokeOverLiveRoute']);
  await module.revokeInBackground(record('i'), live);
  assert.deepEqual(requests.map(request => request.path), ['/v1/sessions/current'], 'the credential is sent only to the sign-out call');
  // No other source file reads or writes the queue's storage key.
  const {readdir, readFile} = await import('node:fs/promises');
  const root = new URL('../src-next/', import.meta.url);
  const files = (await readdir(root, {recursive: true})).filter(name => /\.(ts|tsx)$/.test(name));
  const readers = [];
  for (const name of files) if ((await readFile(new URL(name, root), 'utf8')).includes('accounts.revocations.v1')) readers.push(name);
  assert.deepEqual(readers, ['platform/session-revocation.ts']);
});

