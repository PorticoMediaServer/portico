import test from 'node:test';
import assert from 'node:assert/strict';
import type {DiscoveryRoute} from '@core/route-connection.ts';
import {browserDiscoveryProvider, installBrowserDiscoveryProvider} from '../src/platform/discovery-provider.ts';

const rows: readonly DiscoveryRoute[] = Object.freeze([{baseUrl: 'https://192.168.1.2:32500', serverId: 'srv_' + 'A'.repeat(43), fingerprint: 'A'.repeat(43), port: 32500, path: '/', expiresAt: 2000000000000}]);
const deferred = <T,>() => {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};};
const isAborted = (error: unknown) => error instanceof DOMException && error.name === 'AbortError';

test('ordinary browsers have no provider, and registration/capture perform no discovery', t => {
  assert.equal(browserDiscoveryProvider(), undefined);
  let calls = 0;
  const uninstall = installBrowserDiscoveryProvider(async () => {calls++;return rows;});t.after(uninstall);
  assert.equal(typeof browserDiscoveryProvider(), 'function');assert.equal(calls, 0);
  uninstall();uninstall();
  assert.equal(browserDiscoveryProvider(), undefined);assert.equal(calls, 0);
});
test('discovery value-calls an injected transport and forwards candidate facts without adding authority', async t => {
  let receiver: unknown = 'not-called', observed: AbortSignal | undefined;
  const uninstall = installBrowserDiscoveryProvider(async function(this: unknown, signal) {
    receiver = this;observed = signal;return rows;
  });t.after(uninstall);
  const result = await browserDiscoveryProvider()!(new AbortController().signal);
  assert.equal(receiver, undefined);assert.ok(observed instanceof AbortSignal);
  assert.equal(result, rows);
});
test('an explicitly bound producer retains its own receiver', async t => {
  const owner = {rows};
  const uninstall = installBrowserDiscoveryProvider(async function(this: typeof owner) {return this.rows;}.bind(owner));t.after(uninstall);
  assert.equal(await browserDiscoveryProvider()!(new AbortController().signal), rows);
});
test('uninstall aborts an in-flight provider that ignores cancellation and never delivers its late rows', async () => {
  const result = deferred<readonly DiscoveryRoute[]>();let observed: AbortSignal | undefined;
  const uninstall = installBrowserDiscoveryProvider(signal => {observed = signal;return result.promise;});
  const discover = browserDiscoveryProvider()!, pending = discover(new AbortController().signal);
  await Promise.resolve();assert.ok(observed);assert.equal(observed.aborted, false);
  const refused = assert.rejects(pending, isAborted);
  uninstall();await refused;assert.equal(observed.aborted, true);
  result.resolve(rows);await Promise.resolve();
  await assert.rejects(discover(new AbortController().signal), isAborted);
  assert.equal(browserDiscoveryProvider(), undefined);
});
test('replacing a provider retires old requests, and its old uninstaller cannot remove the successor', async t => {
  const result = deferred<readonly DiscoveryRoute[]>();let oldCalls = 0, nextCalls = 0;
  const oldUninstall = installBrowserDiscoveryProvider(() => {oldCalls++;return result.promise;});t.after(oldUninstall);
  const oldDiscover = browserDiscoveryProvider()!, pending = oldDiscover(new AbortController().signal);
  await Promise.resolve();
  const refused = assert.rejects(pending, isAborted);
  const nextUninstall = installBrowserDiscoveryProvider(async () => {nextCalls++;return rows;});t.after(nextUninstall);
  await refused;oldUninstall();
  await assert.rejects(oldDiscover(new AbortController().signal), isAborted);
  assert.equal(await browserDiscoveryProvider()!(new AbortController().signal), rows);
  assert.equal(oldCalls, 1);assert.equal(nextCalls, 1);result.resolve(rows);
});
test('caller cancellation propagates to the bridge, while a pre-aborted call never invokes it', async t => {
  let calls = 0, observed: AbortSignal | undefined;const result = deferred<readonly DiscoveryRoute[]>();
  const uninstall = installBrowserDiscoveryProvider(signal => {calls++;observed = signal;return result.promise;});t.after(uninstall);
  const discover = browserDiscoveryProvider()!, before = new AbortController();before.abort();
  await assert.rejects(discover(before.signal), isAborted);assert.equal(calls, 0);
  const caller = new AbortController(), pending = discover(caller.signal);await Promise.resolve();
  const refused = assert.rejects(pending, isAborted);caller.abort();await refused;
  assert.equal(observed?.aborted, true);assert.equal(calls, 1);result.resolve(rows);
});
test('retirement between a provider response and delivery still refuses its result', async () => {
  let uninstall!: () => void;
  uninstall = installBrowserDiscoveryProvider(async () => {uninstall();return rows;});
  await assert.rejects(browserDiscoveryProvider()!(new AbortController().signal), isAborted);
  assert.equal(browserDiscoveryProvider(), undefined);
});
test('producer failure is preserved without inventing routes or a browser fallback', async t => {
  const failure = new Error('Native discovery unavailable.');
  const uninstall = installBrowserDiscoveryProvider(async () => {throw failure;});t.after(uninstall);
  await assert.rejects(browserDiscoveryProvider()!(new AbortController().signal), error => error === failure);
});
test('the real browser connection environment captures the installed lease before its listener fast path', async t => {
  const {browserConnectionEnvironment, installBrowserDiscoveryProvider: install} = await import('../src/platform/server-connections.ts');
  const page = new EventTarget();
  for (const [key, value] of [['addEventListener', page.addEventListener.bind(page)], ['navigator', {}], ['document', page]] as const) {
    const before = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, {configurable: true, value});
    t.after(() => {if (before) Object.defineProperty(globalThis, key, before);else delete (globalThis as Record<string, unknown>)[key];});
  }
  assert.equal(browserConnectionEnvironment().discover, undefined);
  let calls = 0;
  const uninstall = install(async () => {calls++;return rows;});t.after(uninstall);
  const first = browserConnectionEnvironment(), second = browserConnectionEnvironment();
  assert.equal(calls, 0);assert.ok(first.discover);assert.ok(second.discover);
  assert.equal(await first.discover(new AbortController().signal), rows);
  assert.equal(await second.discover(new AbortController().signal), rows);
  uninstall();
  assert.equal(browserConnectionEnvironment().discover, undefined);
  await assert.rejects(first.discover(new AbortController().signal), isAborted);
});
