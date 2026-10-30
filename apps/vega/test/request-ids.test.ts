/** Shared services get their request ids on Vega whatever `crypto` the runtime has, or has not. */
import test from 'node:test';
import assert from 'node:assert/strict';

const uuidPattern = /^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$/;
const real = globalThis.crypto;
const set = (value: unknown) => Object.defineProperty(globalThis, 'crypto', {value, writable: true, configurable: true});
let run = 0;
const now = () => globalThis.crypto as Crypto;
const install = () => import(`../src/shims/request-ids.ts?run=${++run}`);

test('a runtime with a full `crypto` is left alone', async () => {
  const before = real.randomUUID;
  await install();
  assert.equal(globalThis.crypto, real);
  assert.equal(globalThis.crypto.randomUUID, before);
});

test('a runtime whose `crypto` has no `randomUUID` gains one and keeps the rest', async () => {
  const partial = {getRandomValues: (array: Uint8Array) => real.getRandomValues(array)};
  set(partial);
  await install();
  assert.ok(now() === (partial as unknown));
  assert.match(now().randomUUID(), uuidPattern);
  assert.notEqual(now().randomUUID(), now().randomUUID());
});

test('a runtime with no `crypto` at all gets one, from the system\'s random source', async () => {
  set(undefined);
  await install();
  assert.match(now().randomUUID(), uuidPattern);
  assert.equal(now().getRandomValues(new Uint8Array(8)).length, 8);
});

test('a sealed `crypto` is not broken: a new one stands in front of it', async () => {
  const sealed = Object.freeze({getRandomValues: (array: Uint8Array) => real.getRandomValues(array)});
  set(sealed);
  await install();
  assert.match(now().randomUUID(), uuidPattern);
  const bytes = now().getRandomValues(new Uint8Array(16));
  assert.equal(bytes.length, 16);
  assert.ok(bytes.some(byte => byte !== 0));
});

test('with a source of ids, client-core\'s own id works', async () => {
  const {randomId} = await import('../../../packages/client-core/src/random-id.ts');
  assert.match(randomId().toUpperCase(), uuidPattern);
  set(real);
});
