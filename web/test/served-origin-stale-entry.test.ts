import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import type {LocalSession} from '@core/index.ts';
import {RouteError} from '@core/route-identity.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import {savedServerIsThisPage} from '../src/platform/served-origin.ts';

const origin = 'https://demo.example', serverId = 'srv_' + 's'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const session: LocalSession = {accessToken: 'access-a', refreshToken: 'refresh-credential-a', sessionFamilyId: 'family-a', tokenGeneration: '1',
  installationId: 'i'.repeat(43), deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
  viewer: {serverId, authority: 'local', accountId: 'account-a', profileId: 'main', role: 'owner'}};
const record: RememberedServer = {version: '1', logicalOrigin: origin, pin, session, routes: {paired: [origin]}};

async function fixture(served: string | undefined) {
  let raw: AppSessionRecord | undefined;
  const store = new AppSessionStore({read: async () => raw, change: async update => {raw = update(raw);}});
  await store.connectionStorage(serverId, {signIn: 'local', expectedRevision: (await store.read()).revision}).change(() => record);
  const forgotten: string[] = [];
  const entry = new BrowserAccountEntry({store,
    actions: {forgetServer: async (value: LocalSession, capture?: {isCurrent: () => boolean; expectedRevision: number}) => {
      forgotten.push(value.sessionFamilyId);
      return (await store.signOutServer(value.viewer.serverId, value.sessionFamilyId, capture?.isCurrent, capture?.expectedRevision)).length > 0;
    }} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}) as never,
    restore: async () => {throw new RouteError('identity_mismatch', 'This address belongs to a different server.');},
    inspect: async () => pin, connect: (async () => {throw new Error('unused');}) as never, remember: async () => {},
    serverName: () => 'Portico demo', servedOrigin: () => served,
  });
  await entry.loadProfiles();
  return {entry, store, forgotten};
}

test('the saved server is this page only when its address is the origin that served the page', () => {
  assert.equal(savedServerIsThisPage('https://demo.example', 'https://demo.example'), true);
  assert.equal(savedServerIsThisPage('https://demo.example/', 'https://demo.example'), true);
  assert.equal(savedServerIsThisPage('https://demo.example', 'https://other.example'), false);
  assert.equal(savedServerIsThisPage('http://192.168.1.20:32500', 'http://192.168.1.20:32400'), false);
  assert.equal(savedServerIsThisPage('https://demo.example', undefined), false);
  assert.equal(savedServerIsThisPage('not a url', 'https://demo.example'), false);
});

test('on the saved server’s own page a changed identity drops that sign-in and shows no identity page', async () => {
  const {entry, store, forgotten} = await fixture(origin);
  assert.deepEqual(forgotten, ['family-a']);
  assert.equal(entry.getSnapshot().phase, 'welcome');
  assert.equal(entry.getSnapshot().identityFailure, undefined);
  assert.equal(entry.serverIdentityFailure(() => {}), undefined);
  assert.equal((await store.read()).servers.length, 0);
});

test('a page another origin served (web.getportico.tv, another address) keeps the identity page and the record', async () => {
  for (const served of [undefined, 'https://other.example']) {
    const {entry, store, forgotten} = await fixture(served);
    assert.deepEqual(forgotten, [], String(served));
    assert.equal(entry.getSnapshot().phase, 'profiles');
    assert.equal((entry.getSnapshot().error as {code?: string}).code, 'identity_mismatch');
    assert.equal((await store.read()).servers.length, 1);
  }
});
