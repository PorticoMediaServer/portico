import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {CredentialSessionService, type CredentialRecord} from '../../../packages/client-core/src/credentials.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';
import {readPorticoAccount} from '../../../packages/client-core/src/portico-services-native.ts';
import {accountExpectation, nativeAccountRead} from '../../../packages/client-core/src/query/native-account-work.ts';

test('hosted password reset checks once on return: cancellation/network keep the session, Services refusal ends it normally', async () => {
  let record: CredentialRecord | undefined, mode: 'valid' | 'offline' | 'ended' = 'valid', reads = 0, renewed = 0, openingFails = false;
  const credentials = new CredentialSessionService({authorityFromSession: session => session.refreshToken!,
    revoke: async () => {}, refresh: async () => {renewed++; throw new ApiError(401, 'signed_out', 'ended');}},
    {load: async () => structuredClone(record), save: async value => {record = structuredClone(value);}}, async () => crypto.randomUUID());
  const original = {account: {id: 'account', username: 'fixture', displayName: 'Fixture'}, accessToken: 'fixture-token', refreshToken: 'fixture-refresh',
    familyId: 'family', expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString()};
  await credentials.adopt(original);
  const listeners = new Set<(state: string) => void>();
  const fetcher = (async (url: string, init: RequestInit) => {
    reads++; assert.equal(url, 'https://services.example.invalid/v1/account');
    assert.equal(new Headers(init.headers).get('Authorization'), 'Bearer fixture-token');
    if (mode === 'offline') throw new Error('unreachable');
    return new Response(JSON.stringify(mode === 'ended' ? {error: {code: 'signed_out'}} : {id: 'account'}), {status: mode === 'ended' ? 401 : 200});
  }) as typeof fetch;
  const {openNativeAccountPasswordReset} = await screenModule(new URL('../src-next/platform/account-password-reset.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {AppState: {addEventListener: (_: string, listener: (state: string) => void) => {
      listeners.add(listener); return {remove: () => listeners.delete(listener)};
    }}, Linking: {openURL: async (url: string) => {
      assert.equal(url, 'https://services.example.invalid/reset?next=%2Faccount%3Ftab%3Dsecurity');
      if (openingFails) throw new Error('browser unavailable');
    }}},
    '../../../../packages/client-core/src/http': {ApiError},
    '../../../../packages/client-core/src/portico-services-native': {readPorticoAccount},
    './account-work': {accountExpectation, nativeAccountRead}, './account-authority': {hostedUrl: 'https://services.example.invalid'},
    './server-connections': {nativeNetworkFetch: fetcher},
  });
  const scope = {accountId: 'account', familyId: 'family'};
  const returned = async () => {
    const pending = openNativeAccountPasswordReset(credentials, scope);
    for (const state of ['active', 'background', 'active', 'active']) for (const listener of [...listeners]) listener(state);
    await pending; assert.equal(listeners.size, 0);
  };
  await returned(); assert.equal(reads, 1); assert.equal(renewed, 0); assert.equal(credentials.getSnapshot().session?.accessToken, original.accessToken);
  mode = 'offline'; await returned(); assert.equal(reads, 2); assert.equal(credentials.getSnapshot().session?.accessToken, original.accessToken);
  let endedNormally = false; const stop = credentials.subscribe(() => {if (credentials.getSnapshot().ended) endedNormally = true;});
  mode = 'ended'; await returned(); stop();
  assert.equal(reads, 3); assert.equal(renewed, 1); assert.equal(endedNormally, true);
  assert.equal(record?.active, undefined); assert.equal(credentials.getSnapshot().session, undefined);
  await credentials.adopt(original);
  const pending = openNativeAccountPasswordReset(credentials, scope);
  await credentials.adopt({...original, account: {...original.account, id: 'other'}, familyId: 'other-family'});
  await pending; assert.equal(listeners.size, 0); assert.equal(reads, 3); assert.equal(credentials.getSnapshot().session?.account.id, 'other');
  openingFails = true;
  await assert.rejects(openNativeAccountPasswordReset(credentials, {accountId: 'other', familyId: 'other-family'}), /browser unavailable/);
  assert.equal(listeners.size, 0); assert.equal(credentials.getSnapshot().session?.account.id, 'other');
});
