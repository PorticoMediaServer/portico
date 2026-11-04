import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../../../packages/client-core/src/index';
import {SessionRefreshError} from '../../../packages/client-core/src/server-connections';
import {parseStartPreparation} from '../../../packages/client-core/src/playback-v1/sessions';
import {observeServerTime, resetServerClocks} from '../../../packages/client-core/src/server-clock';
import {captureNativePreparingOwner} from '../src-next/platform/playback/preparing-owner';
import {nativeSignInEnded, type NativeSignInHint} from '../src-next/platform/sign-in-ended';

// No client-clock requirements: a device a year ahead or behind still works, because a server-issued
// instant is judged on the server's clock (learned from its answers), never on this device's.
const YEAR = 365 * 24 * 3_600_000;
const origin = 'https://original.invalid';

/** The device clock reads `skew` away from real time, and a server answer has taught the difference. */
function skewed(t: TestContext, skew: number) {
  const real = Math.floor(Date.now() / 1000) * 1000, device = real + skew;
  t.mock.method(Date, 'now', () => device);
  resetServerClocks();
  observeServerTime(origin, new Date(real).toUTCString(), device, device);
  t.after(resetServerClocks);
  return real;
}
const iso = (at: number) => new Date(at).toISOString().replace(/\.\d{3}Z$/, 'Z');

const sessionBody = () => ({variant: 'session', id: 'original_session', revision: '1', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: '/v1/media/grant-original_session/1/master.m3u8', startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}}}});

for (const [name, skew] of [['a year ahead', YEAR], ['a year behind', -YEAR]] as const) {
  test(`${name}: a preparing start is still stopped under the sign-in the server has not yet ended`, async t => {
    const real = skewed(t, skew);
    const selected = {accessToken: 'synthetic-original-token', expiresAt: iso(real + 600_000), sessionFamilyId: 'original-family', tokenGeneration: 'one',
      authorizationHorizon: iso(real + 3_600_000), deviceId: 'device', installationId: 'installation',
      viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'Owner'}} as LocalSession;
    const methods: string[] = [];
    const route = {pin: {serverId: 'server', publicKey: 'original-pin'}, getSnapshot: () => ({phase: 'ready'}), subscribe: () => () => {},
      fetch: async (_url: unknown, init?: RequestInit) => {
        methods.push(init?.method ?? 'GET');
        return init?.method === 'GET' ? new Response(JSON.stringify(sessionBody()), {status: 200, headers: {'Content-Type': 'application/json'}}) : new Response(null, {status: 204});
      }};
    const api = {baseUrl: origin, getRouteConnection: () => route, getCertificateSessionEpoch: () => 1, requestRaw: () => {throw new Error('no renewal');}, setAuthRecovery: () => {}};
    const owner = captureNativePreparingOwner(api as never, () => selected)!;
    assert.ok(owner);
    const offered = parseStartPreparation({...sessionBody(), retryAfterMs: 500}).session!;
    await owner.stop(offered, new AbortController().signal, () => true, () => true);
    assert.deepEqual(methods, ['GET', 'DELETE']);
  });

  test(`${name}: an unavailable renewal only ends the sign-in once the server's own expiry has passed`, t => {
    const real = skewed(t, skew);
    const hint = {serverId: 's', accountId: 'a', familyId: 'f', fingerprint: 'fp', origin, serverName: 'Server', username: 'user', signIn: 'local'} as unknown as NativeSignInHint;
    const unavailable = new SessionRefreshError('refresh_unavailable', 'later', false);
    assert.equal(nativeSignInEnded(unavailable, hint, iso(real + 600_000)), undefined, 'the access token has ten minutes left on the server');
    assert.ok(nativeSignInEnded(unavailable, hint, iso(real - 600_000)), 'a token the server has expired is ended');
  });
}
