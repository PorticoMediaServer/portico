import test from 'node:test';
import assert from 'node:assert/strict';
import {SecurePendingStorage, ownerSwitchContextAllowed, type SecurePendingEnvironment} from '../src/platform/secure-pending.ts';

const secure: SecurePendingEnvironment = {protocol: 'https:', hostname: 'server.example', secureContext: true, encryption: true, indexedDB: true, locks: true};
for (const [name, facts] of Object.entries({http: {protocol: 'http:'}, 'network HTTP cannot claim a secure context': {protocol: 'http:', secureContext: true},
  'untrusted page context': {secureContext: false}, 'no encryption': {encryption: false}, 'no database': {indexedDB: false}, 'no locks': {locks: false}})) {
  test(`${name} refuses before any private store is constructed, read or written`, async () => {
    let touched = 0;
    const store = new SecurePendingStorage(() => {touched++; throw new Error('Private storage must stay untouched.');}, () => ({...secure, ...facts}));
    await assert.rejects(store.read(), {code: 'secure_context_required'});
    await assert.rejects(store.change(() => ({specimen: true})), {code: 'secure_context_required'});
    assert.equal(touched, 0);
  });
}
test('ruling 17 localhost exception requires the real browser context and does not widen to network HTTP', async () => {
  const env = {...secure, protocol: 'http:', hostname: 'localhost'};
  assert.equal(ownerSwitchContextAllowed(env), true);
  assert.equal(ownerSwitchContextAllowed({...env, secureContext: false}), false);
  for (const hostname of ['localhost.attacker.example', '192.168.1.20', '100.64.1.2', 'server.example'])
    assert.equal(ownerSwitchContextAllowed({...env, hostname}), false);
  let value: unknown;
  const store = new SecurePendingStorage(() => ({read: async () => value, change: async update => {value = update(value);}}), () => env);
  await store.change(() => ({specimen: true})); assert.deepEqual(await store.read(), {specimen: true});
});
test('secure pending uses one lazy store and rejects a context lost before its atomic update', async () => {
  let current: {specimen: string} | undefined, created = 0, writes = 0, valid = true, delay = false;
  const store = new SecurePendingStorage<{specimen: string}>(() => {created++; return {
    read: async () => current,
    change: async update => {if (delay) valid = false; const next = update(current); current = next; writes++;},
  };}, () => ({...secure, secureContext: valid}));
  assert.equal(created, 0); await store.change(() => ({specimen: 'first'}));
  assert.deepEqual(await store.read(), {specimen: 'first'}); assert.equal(created, 1);
  delay = true; await assert.rejects(store.change(() => ({specimen: 'late'})), {code: 'secure_context_required'});
  assert.deepEqual(current, {specimen: 'first'}); assert.equal(writes, 1);
});
