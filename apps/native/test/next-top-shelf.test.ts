import test from 'node:test';
import assert from 'node:assert/strict';
import {parseTopShelfToken} from '../../../packages/client-core/src/identity-devices.ts';
import {bindTopShelf, topShelfOrigin, topShelfOwnerKey, type NativeTopShelfOwner} from '../src-next/platform/top-shelf.ts';

const token = 'A'.repeat(43), expiresAt = new Date(Date.now() + 30 * 24 * 3600_000).toISOString();
const viewer = {serverId: 'srv', accountId: 'acc', profileId: 'pro', authority: 'local', installationId: 'i'.repeat(43)};

function rig(over: Partial<NativeTopShelfOwner> = {}) {
  const calls: unknown[][] = [], requests: unknown[][] = [], timers: {run: () => void; ms: number}[] = [], listeners = new Set<() => void>();
  let alive = true, key: string | undefined = 'item:one';
  const owner: NativeTopShelfOwner = {current: () => alive, viewer: () => viewer, baseUrl: 'https://media.example:32400',
    tokenApi: {request: async (...args) => {requests.push(args); return {token, expiresAt, feedUrl: '/v1/topshelf'};}},
    subscribePlayback: listener => {listeners.add(listener); return () => listeners.delete(listener);}, playbackKey: () => key, ...over};
  const port = {configure: async (...args: unknown[]) => {calls.push(['configure', ...args]);}, clear: async () => {calls.push(['clear']);}, changed: () => {calls.push(['changed']);}};
  const dispose = bindTopShelf(owner, port, error => {throw error;}, {set: (run, ms) => {timers.push({run, ms}); return timers.length;}, clear: () => {}});
  return {calls, requests, timers, dispose, retire: () => {alive = false;}, play: (next: string | undefined) => {key = next; for (const listener of listeners) listener();}};
}
const settle = () => new Promise(resolve => setImmediate(resolve));

test('N3-1: a local viewer on Apple TV hands the shelf the server origin, a fresh feed token and who it belongs to', async () => {
  const {calls, requests, timers, dispose} = rig();
  await settle();
  assert.deepEqual(requests, [['/v1/topshelf/token', 'POST', {installationId: viewer.installationId}, requests[0][3]]]);
  assert.deepEqual(calls, [['configure', 'https://media.example:32400', token, expiresAt, topShelfOwnerKey(viewer)]]);
  assert.equal(timers.length, 1, 'renewal is scheduled');
  assert.ok(timers[0].ms <= 2 ** 31 - 1 && timers[0].ms >= 60_000);
  dispose(); await settle();
  assert.deepEqual(calls.at(-1), ['clear'], 'sign-out or a profile switch clears the shelf');
});

test('the token is renewed when its timer fires', async () => {
  const {requests, timers} = rig();
  await settle(); timers[0].run(); await settle();
  assert.equal(requests.length, 2);
});

test('the shelf is told to refresh when playback stops or moves to another title, not on every tick', async () => {
  const {calls, play} = rig();
  await settle(); calls.length = 0;
  play('item:one'); assert.deepEqual(calls, []);
  play('item:two'); play(undefined); assert.deepEqual(calls, [['changed'], ['changed']]);
  play('item:three'); assert.deepEqual(calls, [['changed'], ['changed']], 'starting from nothing changes no Continue Watching');
});

test('a hosted account, a server under a path, or no installation leaves the shelf cleared and asks for no token', async () => {
  for (const over of [{viewer: () => ({...viewer, authority: 'hosted'})}, {baseUrl: 'https://example.com/portico'}, {viewer: () => ({...viewer, installationId: undefined})}] as Partial<NativeTopShelfOwner>[]) {
    const {calls, requests} = rig(over);
    await settle();
    assert.deepEqual(requests, []); assert.deepEqual(calls, [['clear']]);
  }
});

test('a viewer retired while the token was being issued never configures the shelf', async () => {
  let release!: (value: unknown) => void;
  const {calls, retire, dispose} = rig({tokenApi: {request: () => new Promise(resolve => {release = resolve;})}});
  retire(); dispose(); release({token, expiresAt, feedUrl: '/v1/topshelf'}); await settle();
  assert.deepEqual(calls, [['clear']]);
});

test('only an origin the extension accepts is handed to it', () => {
  assert.equal(topShelfOrigin('http://192.168.1.4:32400/'), 'http://192.168.1.4:32400');
  for (const bad of ['https://u:p@example.com', 'https://example.com/a', 'https://example.com/?x=1', 'ftp://example.com', 'not a url']) assert.equal(topShelfOrigin(bad), undefined, bad);
});

test('the token reply is read strictly', () => {
  assert.deepEqual(parseTopShelfToken({token, expiresAt, feedUrl: '/v1/topshelf'}), {token, expiresAt, feedUrl: '/v1/topshelf'});
  for (const bad of [{token: 'short', expiresAt, feedUrl: '/v1/topshelf'}, {token, expiresAt: 'soon', feedUrl: '/v1/topshelf'}, {token, expiresAt, feedUrl: 'https://elsewhere/feed'}, {token, expiresAt, feedUrl: '/v1/topshelf', extra: 1}])
    assert.throws(() => parseTopShelfToken(bad));
});

test('televisions bind the shelf (Apple TV, Android TV, Fire TV); phones and tablets never do', async () => {
  const {Platform, NativeModules} = await import('react-native') as any;
  const {bindNativeTopShelf} = await import('../src-next/platform/top-shelf.ts');
  const before = {...Platform}, module = NativeModules.PorticoTopShelf, cleared: string[] = [];
  NativeModules.PorticoTopShelf = {configure: async () => {}, clear: async () => {cleared.push(Platform.OS);}, changed() {}};
  const owner: NativeTopShelfOwner = {current: () => true, viewer: () => undefined, baseUrl: 'https://example.com', tokenApi: {request: async () => ({})}, subscribePlayback: () => () => {}, playbackKey: () => undefined};
  try {
    for (const [OS, isTV, bound] of [['ios', true, true], ['android', true, true], ['ios', false, false], ['android', false, false]] as const) {
      Object.assign(Platform, {OS, isTV});
      const dispose = bindNativeTopShelf(owner, error => {throw error;});
      assert.equal(!!dispose, bound, `${OS} tv=${isTV}`); dispose?.();
    }
  } finally {Object.assign(Platform, before); NativeModules.PorticoTopShelf = module;}
});
