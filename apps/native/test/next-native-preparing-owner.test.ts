import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../../../packages/client-core/src/index';
import {parseStartPreparation} from '../../../packages/client-core/src/playback-v1/sessions';
import {captureNativePreparingOwner} from '../src-next/platform/playback/preparing-owner';

const sessionBody = () => ({id: 'original_session', revision: '1', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: '/v1/media/grant-original_session/1/master.m3u8', startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}}}});
const offered = () => parseStartPreparation({...sessionBody(), retryAfterMs: 500}).session!;

function fixture() {
  let selected: LocalSession | undefined = {accessToken: 'synthetic-original-token', expiresAt: '2099-01-01T00:00:00Z',
    sessionFamilyId: 'original-family', tokenGeneration: 'one', authorizationHorizon: '2099-01-01T00:00:00Z',
    deviceId: 'device', installationId: 'installation',
    viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'Owner'}};
  let epoch = 1, body: unknown = sessionBody(), duringGet: (() => void) | undefined;
  const requests: {url: unknown; init?: RequestInit}[] = [];
  const route = {pin: {serverId: 'server', publicKey: 'original-pin'}, getSnapshot: () => ({phase: 'ready'}),
    subscribe: () => {throw new Error('cleanup must not acquire a route subscription');},
    fetch: async (url: unknown, init?: RequestInit) => {
      requests.push({url, init});
      if (init?.method === 'GET') {duringGet?.(); return new Response(JSON.stringify(body), {status: 200, headers: {'Content-Type': 'application/json'}});}
      return new Response(null, {status: 204});
    }};
  const api = {baseUrl: 'https://original.invalid', getRouteConnection: () => route, getCertificateSessionEpoch: () => epoch,
    requestRaw: () => {throw new Error('cleanup must not borrow refresh-capable requestRaw');},
    setAuthRecovery: () => {throw new Error('cleanup must not install a renewal callback');}};
  const owner = captureNativePreparingOwner(api as never, () => selected)!;
  return {owner, requests, route, body: (next: unknown) => {body = next;}, duringGet: (run: () => void) => {duringGet = run;},
    rotate: () => {selected = {...selected!, accessToken: 'synthetic-successor-token', tokenGeneration: 'two'}; epoch++;},
    family: () => {selected = {...selected!, sessionFamilyId: 'successor-family'};}, end: () => {selected = undefined;}};
}

test('native preparing cleanup reads only the exact id then claims and stops it under the original pin/token', async () => {
  const f = fixture(); let claims = 0;
  assert.ok(f.owner);
  await f.owner.stop(offered(), new AbortController().signal, () => true, () => {claims++; return true;});
  assert.equal(claims, 1);
  assert.deepEqual(f.requests.map(value => [value.url, value.init?.method]), [
    ['https://original.invalid/v1/playback/sessions/original_session', 'GET'],
    ['https://original.invalid/v1/playback/sessions/original_session', 'DELETE'],
  ]);
  assert.ok(f.requests.every(value => new Headers(value.init?.headers).get('Authorization') === 'Bearer synthetic-original-token'));
  assert.ok(f.requests.every(value => value.init?.redirect === 'error' && value.init.credentials === 'omit' && value.init.body === undefined));
});

test('changed generation, target or local role after the exact read refuses the stop claim', async () => {
  for (const change of ['generation', 'item', 'role'] as const) {
    const f = fixture(), body = sessionBody(); let claims = 0;
    f.body(change === 'generation' ? {...body, presentation: {...body.presentation, generation: 2}}
      : change === 'item' ? {...body, itemId: 'successor'} : {...body, role: 'remote'});
    await f.owner.stop(offered(), new AbortController().signal, () => true, () => {claims++; return true;});
    assert.equal(claims, 0); assert.equal(f.requests.length, 1);
  }
});

test('refresh/rotation, family replacement and ended principal never supply cleanup authority', async () => {
  for (const change of ['rotate', 'family', 'end'] as const) {
    const f = fixture(); f[change]();
    assert.equal(f.owner.current(), false);
    await assert.rejects(f.owner.stop(offered(), new AbortController().signal, () => true, () => true));
    assert.equal(f.requests.length, 0);
  }
});

test('rotation while GET settles refuses DELETE and never renews or adopts the new token', async () => {
  const f = fixture(); f.duringGet(f.rotate);
  await assert.rejects(f.owner.stop(offered(), new AbortController().signal, () => true, () => true));
  assert.equal(f.requests.length, 1);
  assert.equal(new Headers(f.requests[0]!.init?.headers).get('Authorization'), 'Bearer synthetic-original-token');
});

test('local installation while the read settles refuses its later stop claim', async () => {
  const f = fixture(); let uninstalled = true, claims = 0;
  f.duringGet(() => {uninstalled = false;});
  await assert.rejects(f.owner.stop(offered(), new AbortController().signal, () => uninstalled, () => {claims++; return true;}));
  assert.equal(claims, 0); assert.equal(f.requests.length, 1);
});

test('changing the original pin during the atomic claim refuses the DELETE dispatch', async () => {
  const f = fixture();
  await assert.rejects(f.owner.stop(offered(), new AbortController().signal, () => true, () => {f.route.pin.publicKey = 'successor-pin'; return true;}));
  assert.equal(f.requests.length, 1);
});
