// The app's one Portico Account holder (`platform/portico-account-holder.ts`), against stand-ins
// for the account pages' session: what is left of the old sign-in, and what a listed server says.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';

const memory = (entries: Record<string, string>) => {
  const map = new Map(Object.entries(entries));
  return {get length() {return map.size;}, key: (i: number) => [...map.keys()][i] ?? null, getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => {map.set(k, v);}, removeItem: (k: string) => {map.delete(k);}, keys: () => [...map.keys()].sort()};
};
async function holder(listed: unknown[] = []) {
  return componentModule(new URL('../src/platform/portico-account-holder.ts', import.meta.url), {
    '../services/account-session': {accountSession: {subscribe: () => () => {}, signedIn: () => true}, account: {}},
    '@core/portico-server-list.ts': {openAccountServerList: async () => ({accountId: 'acct_1', items: listed, version: 2, listToken: 't'})},
    '@core/route-identity.ts': {webRouteCrypto: {}},
    './portico-server-access': {porticoServerAccess: () => ({})},
  });
}

test('what the old version kept of the Portico Account in this browser is removed, and nothing else', async () => {
  const storage = memory({
    'portico.account.v1:https://web.getportico.tv': 'old tokens', 'portico.account.csrf.v1:https://web.getportico.tv': 'x',
    'portico.account.csrf.v2:["https://web.getportico.tv","https://web.getportico.tv"]': 'y', 'portico.account.csrf.v2:["https://web.getportico.tv","https://web.getportico.tv"]:name': 'z',
    'portico.account.signedIn': '1', 'portico.profile-installation.v1': 'kept',
  });
  (await holder()).forgetOldAccountStorage(storage);
  assert.deepEqual(storage.keys(), ['portico.account.signedIn', 'portico.profile-installation.v1']);
});

test('a listed server is neither online nor offline: the signed list says nothing about whether it is up', async () => {
  const {porticoAccountHolder} = await holder([{id: 'srv_1', name: 'Den', baseUrl: 'https://a1.ns.portico.direct:32400', publicKey: 'k', policyRevision: 0, role: 'owner', ownedByMe: true}]);
  const list = await porticoAccountHolder.servers();
  assert.equal(list.accountId, 'acct_1');
  assert.deepEqual(list.items, [{id: 'srv_1', name: 'Den', baseUrl: 'https://a1.ns.portico.direct:32400', publicKey: 'k', role: 'owner', ownedByMe: true, status: 'listed', presence: {lastSeenAt: null}}]);
});
