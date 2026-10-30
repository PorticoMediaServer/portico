import test from 'node:test';
import assert from 'node:assert/strict';
import {SetupPendingStorage, setupBrowserPending} from '../src/platform/setup-pending.ts';

const memory = () => {let value: unknown; return {read: async () => value, change: async (update: (v: unknown) => unknown) => {value = update(value);}} as never;};

test('setup pending storage needs only IndexedDB and locks, never a secure context or WebCrypto', async () => {
  const store = new SetupPendingStorage<{a: number}>(memory, () => ({indexedDB: true, locks: true}));
  store.assertAvailable();
  await store.change(() => ({a: 1})); assert.deepEqual(await store.read(), {a: 1});
});
test('setup pending storage refuses without IndexedDB or locks', () => {
  assert.throws(() => new SetupPendingStorage(memory, () => ({indexedDB: false, locks: true})).assertAvailable());
  assert.throws(() => new SetupPendingStorage(memory, () => ({indexedDB: true, locks: false})).assertAvailable());
});
test('the browser setup store is available on an insecure page with the tab-lock polyfill', () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, 'indexedDB'), nav = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  Object.defineProperty(globalThis, 'indexedDB', {configurable: true, value: {}});
  Object.defineProperty(globalThis, 'navigator', {configurable: true, value: {locks: {request: async () => {}}}});
  Object.defineProperty(globalThis, 'isSecureContext', {configurable: true, value: false});
  const subtle = Object.getOwnPropertyDescriptor(globalThis.crypto, 'subtle');
  Object.defineProperty(globalThis.crypto, 'subtle', {configurable: true, value: undefined});
  try {setupBrowserPending('portico.test').assertAvailable();}
  finally {
    if (subtle) Object.defineProperty(globalThis.crypto, 'subtle', subtle); else delete (globalThis.crypto as {subtle?: unknown}).subtle;
    delete (globalThis as {isSecureContext?: unknown}).isSecureContext;
    if (had) Object.defineProperty(globalThis, 'indexedDB', had); else delete (globalThis as {indexedDB?: unknown}).indexedDB;
    if (nav) Object.defineProperty(globalThis, 'navigator', nav);
  }
});
