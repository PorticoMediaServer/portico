import test from 'node:test';
import assert from 'node:assert/strict';
import {readRememberedLocalAccounts, rememberLocalAccount, rememberedLocalKey} from '../src/screens/accounts/remembered-local-accounts.ts';

const pin = {serverId: 'server', publicKey: 'public-key', fingerprint: 'fingerprint'};
function store() {const values = new Map<string, string>();return {values, getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => {values.set(key, value);}};}

test('local username hints are scoped to the verified server identity and contain no credentials', () => {
  const storage = store(), key = rememberedLocalKey(pin);
  assert.equal(rememberLocalAccount(storage, key, {accountId: 'one', username: 'justin', password: 'must-not-persist', accessToken: 'must-not-persist'} as never), true);
  assert.deepEqual(JSON.parse(storage.values.get(key)!), [{accountId: 'one', username: 'justin'}]);
  assert.deepEqual(readRememberedLocalAccounts(storage, rememberedLocalKey({...pin, serverId: 'other'})), []);
  assert.deepEqual(readRememberedLocalAccounts(storage, rememberedLocalKey({...pin, publicKey: 'replacement-key'})), []);
  assert.deepEqual(readRememberedLocalAccounts(storage, key), [{accountId: 'one', username: 'justin'}]);
});
test('successful accounts move to the front, rename without duplication, and keep a bounded history', () => {
  const storage = store(), key = rememberedLocalKey(pin);
  for (let index = 0; index < 12; index++) rememberLocalAccount(storage, key, {accountId: String(index), username: `user${index}`});
  rememberLocalAccount(storage, key, {accountId: '5', username: 'renamed'});
  const rows = readRememberedLocalAccounts(storage, key);
  assert.equal(rows.length, 10);assert.deepEqual(rows[0], {accountId: '5', username: 'renamed'});
  assert.equal(rows.filter(row => row.accountId === '5').length, 1);assert.equal(rows.some(row => row.accountId === '0'), false);
});
test('malformed or denied optional local storage cannot prevent ordinary sign-in', () => {
  const storage = store(), key = rememberedLocalKey(pin);
  storage.setItem(key, '{broken');assert.deepEqual(readRememberedLocalAccounts(storage, key), []);
  storage.setItem(key, JSON.stringify([{accountId: 'ok', username: 'user', secret: 'discard'}, {accountId: 'bad', username: ''}, {accountId: 'ok', username: 'duplicate'}]));
  assert.deepEqual(readRememberedLocalAccounts(storage, key), [{accountId: 'ok', username: 'user'}]);
  const denied = {getItem: () => {throw new Error('denied');}, setItem: () => {throw new Error('denied');}};
  assert.deepEqual(readRememberedLocalAccounts(denied, key), []);assert.equal(rememberLocalAccount(denied, key, {accountId: 'ok', username: 'user'}), false);
  assert.equal(rememberLocalAccount(undefined, key, {accountId: 'ok', username: 'user'}), false);
});
