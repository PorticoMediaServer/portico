import test from 'node:test';
import assert from 'node:assert/strict';
import {refreshStoredSession, savedConnectionDamaged, type RememberedServer} from '@core/server-connections.ts';
import {BrowserProtectedStore} from '../src/platform/server-connections.ts';

import {fakeIndexedDB} from './protected-store-fixture.ts';

type Vault = {read(): Promise<unknown>; change(update: (current: any) => unknown): Promise<void>};
async function vault(t: import('node:test').TestContext, encryptedOnly = false) {
  const db = fakeIndexedDB();
  const had = ['indexedDB', 'navigator'].map(name => [name, Object.getOwnPropertyDescriptor(globalThis, name)] as const);
  Object.defineProperty(globalThis, 'indexedDB', {configurable: true, value: db.indexedDB});
  Object.defineProperty(globalThis, 'navigator', {configurable: true, value: {locks: {request: (_name: string, a: unknown, b?: unknown) => (typeof a === 'function' ? a : b as () => unknown)()}}});
  t.after(() => { for (const [name, descriptor] of had) { if (descriptor) Object.defineProperty(globalThis, name, descriptor); else delete (globalThis as Record<string, unknown>)[name]; } });
  return {db, store: new BrowserProtectedStore('portico.test.vault', 192000, encryptedOnly)};
}
const record = (token: string) => ({version: '1', token});
const refresh = (store: Vault) => {
  let asked = 0;
  const outcome = refreshStoredSession({env: {storage: store as unknown as {read(): Promise<RememberedServer | undefined>; change(u: (c: RememberedServer | undefined) => RememberedServer | undefined): Promise<void>}}, api: {request: async () => { asked++; throw new Error('the server must not be asked'); }}, family: {sessionFamilyId: 'fam', serverId: 'srv'}}).then(() => undefined, (e: {code?: string; signOut?: boolean}) => ({code: e.code, signOut: e.signOut}));
  return outcome.then(result => ({...result, asked}));
};

test('the vault keeps a record across reads and replaces or removes it on request', async t => {
  const {db, store} = await vault(t);
  assert.equal(await store.read(), undefined);
  await store.change(() => record('one'));
  assert.deepEqual(await store.read(), record('one'));
  assert.equal((db.data.get('selected') as {version: number}).version, 1, 'encrypted at rest');
  await store.change(() => undefined);
  assert.equal(await store.read(), undefined);
});
test('a retired attempt cannot write its encrypted record after encryption finishes', async t => {
  const {db, store} = await vault(t);
  await store.change(() => record('original'));
  const original = db.data.get('selected');
  let release!: () => void, entered!: () => void, current = true;
  const waiting = new Promise<void>(resolve => {release = resolve;});
  const started = new Promise<void>(resolve => {entered = resolve;});
  const encrypt = crypto.subtle.encrypt.bind(crypto.subtle);
  t.mock.method(crypto.subtle, 'encrypt', async (...args: Parameters<typeof encrypt>) => {
    const cipher = await encrypt(...args); entered(); await waiting; return cipher;
  });
  const cancelled = assert.rejects(store.change(() => record('cancelled'), () => {
    if (!current) throw new Error('Attempt ended.');
  }));
  await started; current = false; release(); await cancelled;
  assert.equal(db.data.get('selected'), original, 'no cancelled cipher or key was written');
  await store.change(() => record('successor'));
  assert.deepEqual(await store.read(), record('successor'));
});

// Each of these records is there and can never be read again, however long the app waits.
const damage: Record<string, (data: Map<string, unknown>) => void | Promise<void>> = {
  'a key for another algorithm': async data => { data.set('key', await crypto.subtle.generateKey({name: 'HMAC', hash: 'SHA-256'}, false, ['sign', 'verify'])); },
  'a key that may not decrypt': async data => { data.set('key', await crypto.subtle.generateKey({name: 'AES-GCM', length: 256}, false, ['encrypt'])); },
  'something that is not a key': data => { data.set('key', {type: 'secret'}); },
  'text that is not a record': data => { data.set('selected', {version: 2, plain: '{broken'}); },
  'a missing encryption key': data => { data.delete('key'); },
  'contents that fail authentication': data => { const kept = data.get('selected') as {version: 1; iv: Uint8Array; cipher: ArrayBuffer}; const cipher = kept.cipher.slice(0); new Uint8Array(cipher)[0]! ^= 1; data.set('selected', {...kept, cipher}); },
  'an unknown shape': data => { data.set('selected', {version: 1, iv: 'not bytes', cipher: null}); },
};
for (const [name, break_] of Object.entries(damage)) test('a saved record with ' + name + ' is reported as damaged, ends the sign-in at once, and is replaced by the next sign-in', async t => {
  const {db, store} = await vault(t);
  await store.change(() => record('one'));
  await break_(db.data);
  await assert.rejects(store.read(), savedConnectionDamaged);
  // Renewal gets a definite answer instead of retrying for ever, and never asks the server.
  assert.deepEqual(await refresh(store), {code: 'saved_sign_in_damaged', signOut: true, asked: 0});
  // Nothing removes the damaged record behind the person's back: forgetting and cache updates see no record and write nothing.
  const before = db.data.get('selected');
  let seen: unknown = 'unset';
  await store.change(current => { seen = current; return current; });
  assert.equal(seen, undefined);
  assert.equal(db.data.get('selected'), before);
  // Signing in again saves over it, and the vault reads normally from then on.
  await store.change(() => record('two'));
  assert.deepEqual(await store.read(), record('two'));
});

test('a vault that cannot be opened right now is not damage: the sign-in is kept and read later', async t => {
  const {db, store} = await vault(t);
  await store.change(() => record('one'));
  db.failOpen(2);
  await assert.rejects(store.read(), (e: unknown) => !savedConnectionDamaged(e));
  assert.deepEqual(await refresh(store), {code: 'store_unavailable', signOut: false, asked: 0});
  assert.deepEqual(await store.read(), record('one'));
});

test('without browser encryption (a page on plain HTTP) a change reads, decides and writes in one database transaction', async t => {
  const {db, store} = await vault(t);
  // Count the transactions the vault opens, by mode.
  const opened: string[] = [];
  const open = db.indexedDB.open;
  db.indexedDB.open = () => {
    const r = open();
    const ready = () => { const database = r.result; const transaction = database.transaction; database.transaction = (name: string, mode: string, options?: {durability?: string}) => { opened.push(mode); return transaction(name, mode, options); }; };
    queueMicrotask(() => queueMicrotask(ready));
    return new Proxy(r, {set: (target, key, value) => { target[key] = key === 'onsuccess' ? () => { ready(); value(); } : value; return true; }});
  };
  const hadCrypto = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
  Object.defineProperty(globalThis, 'crypto', {configurable: true, value: {getRandomValues: (a: Uint8Array) => a}});
  t.after(() => { if (hadCrypto) Object.defineProperty(globalThis, 'crypto', hadCrypto); });

  await store.change(() => record('one'));
  assert.deepEqual(opened, ['readwrite'], 'no separate read before the write: another tab’s change cannot fall between them');
  assert.deepEqual(db.data.get('selected'), {version: 2, plain: JSON.stringify(record('one'))});
  assert.deepEqual(await store.read(), record('one'));

  opened.length = 0;
  let seen: unknown;
  await store.change(current => { seen = current; return current; });
  assert.deepEqual(seen, record('one'), 'the decision sees what is stored');
  assert.deepEqual(opened, ['readwrite']);
  assert.deepEqual(db.data.get('selected'), {version: 2, plain: JSON.stringify(record('one'))}, 'an update that changes nothing writes nothing');

  await store.change(() => undefined);
  assert.equal(db.data.has('selected'), false);
  // Damaged plain text reads as absent, and a save replaces it.
  db.data.set('selected', {version: 2, plain: '{broken'});
  await store.change(current => { seen = current; return record('two'); });
  assert.equal(seen, undefined);
  assert.deepEqual(await store.read(), record('two'));
});

test('encrypted-only pending vault refuses a plaintext record without consuming it', async t => {
  const {db, store} = await vault(t, true);
  const old = {version: 2, plain: JSON.stringify({pending: 'specimen'})};
  db.data.set('selected', old);
  await assert.rejects(store.read(), savedConnectionDamaged);
  assert.equal(db.data.get('selected'), old);
  await store.change(() => ({pending: 'fresh'}));
  assert.equal((db.data.get('selected') as {version: number}).version, 1);
  assert.deepEqual(await store.read(), {pending: 'fresh'});
});
test('encrypted-only pending vault cannot downgrade to HTTP plaintext storage', async t => {
  const {db, store} = await vault(t, true);
  const before = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
  Object.defineProperty(globalThis, 'crypto', {configurable: true, value: {getRandomValues() {throw new Error('No cryptographic work is allowed.');}}});
  t.after(() => {if (before) Object.defineProperty(globalThis, 'crypto', before);});
  await assert.rejects(async () => store.read(), {code: 'secure_context_required'});
  await assert.rejects(async () => store.change(() => ({pending: 'never-written'})), {code: 'secure_context_required'});
  assert.equal(db.data.size, 0);
});
