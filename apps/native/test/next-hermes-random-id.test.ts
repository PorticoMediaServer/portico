/** Hermes has no `crypto`: the app's launch shim must give shared code both of its random sources. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from 'react-native';
import {newUserMutationKey, isUserMutationKey} from '../../../packages/client-core/src/user-mutations.ts';

const real = globalThis.crypto;
const set = (value: unknown) => Object.defineProperty(globalThis, 'crypto', {value, writable: true, configurable: true});
let run = 0;
const install = () => import(`../src-next/platform/random-id.ts?run=${++run}`);
const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
const restore = {playbackRequestId: runtime.playbackRequestId, playbackRandomBytes: runtime.playbackRandomBytes};
test.afterEach(() => { set(real); Object.assign(runtime, restore); });

test('a runtime with no `crypto` can make mutation keys (the signed-in start builds one)', async () => {
  const asked: number[] = [];
  runtime.playbackRandomBytes = (n: number) => { asked.push(n); return Array.from({length: n}, (_, i) => (i * 37 + n) % 256); };
  set(undefined);
  await install();
  const key = newUserMutationKey(1_761_700_000_000);
  assert.ok(isUserMutationKey(key), key);
  assert.equal(typeof globalThis.crypto.randomUUID, 'function');
  const big = globalThis.crypto.getRandomValues(new Uint8Array(150));
  assert.deepEqual(asked.slice(1), [64, 64, 22], 'filled in chunks of at most 64');
  assert.equal(big[149], (21 * 37 + 22) % 256);
});

test('typed arrays wider than a byte are filled across their whole buffer', async () => {
  runtime.playbackRandomBytes = (n: number) => Array.from({length: n}, () => 255);
  set(undefined);
  await install();
  const words = globalThis.crypto.getRandomValues(new Uint32Array(4));
  assert.deepEqual([...words], [0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff]);
});

test('a bad native answer fails loudly instead of returning zeros', async () => {
  runtime.playbackRandomBytes = () => null;
  set(undefined);
  await install();
  assert.throws(() => globalThis.crypto.getRandomValues(new Uint8Array(4)), /unavailable/);
});

test('a runtime that already has Web Crypto keeps its own', async () => {
  const before = real.getRandomValues;
  await install();
  assert.equal(globalThis.crypto, real);
  assert.equal(globalThis.crypto.getRandomValues, before);
});
