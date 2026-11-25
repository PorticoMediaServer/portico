import test from 'node:test';
import assert from 'node:assert/strict';
import {createSharedReads} from '../src-next/shell/shared-read.ts';
import {NativeShellBootstrapServices} from '../src-next/shell/bootstrap-services.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';

const flush = async () => {for (let pass = 0; pass < 4; pass++) await Promise.resolve();};

test('reads asked while one is in flight share its single request and answer', async () => {
  const shared = createSharedReads();
  let calls = 0, finish!: (value: string) => void;
  const read = () => {calls++; return new Promise<string>(resolve => {finish = resolve;});};
  const first = shared('/v1/home', undefined, read), second = shared('/v1/home', undefined, read), third = shared('/v1/home', new AbortController().signal, read);
  assert.equal(calls, 1);
  finish('home');
  assert.deepEqual(await Promise.all([first, second, third]), ['home', 'home', 'home']);
  await shared('/v1/home', undefined, async () => {calls++; return 'again';});
  assert.equal(calls, 2, 'a read after the first settles is a new request');
});

test('one caller leaving never cancels the others; the request stops only when every caller has gone', async () => {
  const shared = createSharedReads();
  let signal: AbortSignal | undefined, finish!: (value: string) => void;
  const read = (inner: AbortSignal) => {signal = inner; return new Promise<string>(resolve => {finish = resolve;});};
  const leaving = new AbortController(), staying = new AbortController();
  const left = shared('/v1/home', leaving.signal, read), kept = shared('/v1/home', staying.signal, read);
  leaving.abort();
  await assert.rejects(left, {name: 'AbortError'});
  assert.equal(signal?.aborted, false);
  finish('home'); assert.equal(await kept, 'home');

  const alone = new AbortController();
  const only = shared('/v1/home?limit=20', alone.signal, read);
  alone.abort(); await assert.rejects(only, {name: 'AbortError'}); await flush();
  assert.equal(signal?.aborted, true);
});

test('Home document GETs go through the shared read; rows and writes do not', async () => {
  const requests: {path: string; method: string; resolve: (value: unknown) => void}[] = [];
  const homePath = routePath('get_home');
  const api = {
    request: <T>(path: string, method = 'GET', _body?: unknown): Promise<T> => new Promise<T>(resolve => {
      requests.push({path, method, resolve: resolve as (value: unknown) => void});
    }),
  };
  const abort = new AbortController();
  const services = new NativeShellBootstrapServices(api, {
    viewer: {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', authority: 'local', role: 'member'},
    deviceClass: 'mobile',
    signal: abort.signal,
    current: () => true,
    onAuthorityEnded() {},
    accountBinding: {assertAccount() {}, assertCurrent: async () => {}, assertSameAccount() {}} as any,
  });
  try {
    // 1) Concurrent GET /v1/home loads share one in-flight transport request.
    const homeApi = (services.home as any).api() as {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>};
    const doc = {rows: []};
    const firstHome = homeApi.request(homePath, 'GET');
    const secondHome = homeApi.request(homePath, 'GET');
    assert.equal(requests.filter(r => r.path === homePath && r.method === 'GET').length, 1, 'concurrent Home GETs share one request');
    requests.shift()!.resolve(doc);
    assert.deepEqual(await Promise.all([firstHome, secondHome]), [doc, doc]);

    // 2) Row reads (different path) and writes (POST or with body) are never coalesced.
    const rowPath = `${homePath}/rows/continue`;
    const r1 = homeApi.request(rowPath, 'GET');
    const r2 = homeApi.request(rowPath, 'GET');
    assert.equal(requests.filter(r => r.path === rowPath).length, 2, 'row reads are not coalesced');
    requests.splice(0).forEach(r => r.resolve({items: []}));
    await Promise.all([r1, r2]);

    const w1 = homeApi.request(homePath, 'POST', {order: []});
    const w2 = homeApi.request(homePath, 'POST', {order: []});
    assert.equal(requests.filter(r => r.method === 'POST').length, 2, 'writes are not coalesced');
    requests.splice(0).forEach(r => r.resolve({ok: true}));
    await Promise.all([w1, w2]);
  } finally {
    services.dispose();
  }
});
