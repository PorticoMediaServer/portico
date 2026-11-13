import test from 'node:test';
import assert from 'node:assert/strict';
import {setInstallationIdentity} from '../src/installation.ts';
import {createLocalTVAuthorization, pollLocalTVAuthorization} from '../src/query/connection-operations.ts';
import type {DeviceAuthorization} from '../src/device-authorization.ts';
import {ApiError} from '../src/index.ts';

test('local TV journal adapts create, pending and approval to the rebuilt protocol', async () => {
  const device = {installationId: 'i'.repeat(43), name: 'Living room', platform: 'tvos', app: 'Portico', appVersion: '1'};
  setInstallationIdentity(device);
  const calls: unknown[][] = [], secret = 's'.repeat(43), session = {accessToken: 'synthetic'};
  let approved = false;
  const api = {baseUrl: 'http://server.test', request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push([path, method, body, signal]);
    return (path === '/v1/auth/codes' ? {code: '2345-ABCD', deviceSecret: secret, intervalMs: 5000, expiresAt: '2099-01-01T00:00:00Z'}
      : path.endsWith('/redeem') ? approved ? {status: 'approved', signIn: {session}} : {status: 'pending', intervalMs: 5000} : undefined) as T;
  }};
  try {
    const signal = new AbortController().signal;
    const code = await createLocalTVAuthorization<DeviceAuthorization>(api, 'request', signal);
    assert.equal(code.userCode, '2345-ABCD'); assert.equal(code.interval, 5);
    assert.equal(code.verificationUriComplete, 'http://server.test/device#code=2345-ABCD');
    assert.deepEqual(calls[0], ['/v1/auth/codes', 'POST', {device}, signal]);
    await assert.rejects(pollLocalTVAuthorization(api, code.deviceCode, signal), {code: 'authorization_pending'});
    approved = true; assert.equal(await pollLocalTVAuthorization(api, code.deviceCode, signal), session);
    for (const [from, to] of [['code_expired', 'expired_token'], ['sign_in_denied', 'access_denied']])
      await assert.rejects(pollLocalTVAuthorization({request: async () => {throw new ApiError(410, from, 'ended');}}, code.deviceCode), {code: to});
  } finally {setInstallationIdentity(undefined);}
});
