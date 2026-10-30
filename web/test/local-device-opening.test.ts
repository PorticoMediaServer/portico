import test from 'node:test';
import assert from 'node:assert/strict';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {captureLocalDeviceOpening, type LocalDeviceLocation} from '../src/screens/device/local-device-opening.ts';

const review = routePath('post_quick_connect_review'), decision = routePath('post_quick_connect_decision');
function fixture() {
  let current = true, now = Date.parse('2026-10-26T12:00:00Z');
  let session: LocalSession = {accessToken: 'original-test-token', tokenGeneration: '1', expiresAt: '2026-10-26T12:10:00Z', authorizationHorizon: '2026-10-26T13:00:00Z', sessionFamilyId: 'original-family',
    viewer: {authority: 'local', serverId: 'original-server', accountId: 'original-account', profileId: 'original-profile', role: 'owner'}};
  let location: LocalDeviceLocation = {pathname: '/device', href: '/device', state: {__TSR_key: 'original-entry'}};
  let state = {phase: 'ready', origin: 'https://server.example'};
  const connection = {pin: {serverId: 'original-server', publicKey: 'original-key', fingerprint: 'original-fingerprint'}, logicalOrigin: 'https://server.example', getSnapshot: () => state};
  let route = connection;
  let respond: (path: string, signal?: AbortSignal) => Promise<unknown> = async () => ({review: true});
  const calls: {path: string; body: unknown; signal?: AbortSignal}[] = [];
  const api = {baseUrl: 'https://server.example', getRouteConnection: () => route, request: async <T>(path: string, _method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, body, signal});return await respond(path, signal) as T;
  }} as unknown as Pick<HttpLocalApi, 'baseUrl' | 'getRouteConnection' | 'request'>;
  const capture = (pageOrigin = 'https://server.example') => captureLocalDeviceOpening({pageOrigin, api, session: () => session, isCurrent: () => current, location, getLocation: () => location, now: () => now});
  return {capture, api, calls, connection, setSession(value: Partial<LocalSession>) {session = {...session, ...value};},
    setViewer(value: Partial<LocalSession['viewer']>) {session = {...session, viewer: {...session.viewer, ...value}};},
    setLocation(value: LocalDeviceLocation) {location = value;}, setCurrent(value: boolean) {current = value;}, setNow(value: number) {now = value;},
    setConnection(value: typeof connection) {route = value;}, setState(value: typeof state) {state = value;}, setResponse(value: typeof respond) {respond = value;}};
}
test('capture performs no IO; only the two actual local POST review/decision routes are exposed', async () => {
  const f = fixture(), opening = f.capture()!;assert.ok(opening);assert.equal(f.calls.length, 0);
  await assert.rejects(opening.api.request('/v1/unrelated', 'POST'), {name: 'AbortError'});
  await assert.rejects(opening.api.request(review, 'GET'), {name: 'AbortError'});assert.equal(f.calls.length, 0);
  await opening.api.request(review, 'POST', {userCode: 'ABCD-EFGH'});
  await opening.api.request(decision, 'POST', {userCode: 'ABCD-EFGH', decision: 'deny'});
  assert.deepEqual(f.calls.map(value => value.path), [review, decision]);opening.dispose();
});
test('wrong origin, unverified connection and foreign server never capture an approval opening', () => {
  let f = fixture();assert.equal(f.capture('https://other.example'), undefined);
  f = fixture();f.setState({phase: 'probing', origin: 'https://server.example'});assert.equal(f.capture(), undefined);
  f = fixture();f.setViewer({serverId: 'another-server'});assert.equal(f.capture(), undefined);
  f = fixture();f.setLocation({pathname: '/device', href: '/device', state: {}});assert.equal(f.capture(), undefined);
});
test('an actual verified alternate address can capture only that same pinned connection', () => {
  const f = fixture();f.setState({phase: 'ready', origin: 'https://alternate.example'});
  const opening = f.capture('https://alternate.example')!;assert.ok(opening);assert.equal(opening.current(), true);opening.dispose();
});
test('token renewal preserves the original family and horizon; every authority/entry replacement retires it', () => {
  for (const replace of [
    (f: ReturnType<typeof fixture>) => f.setSession({sessionFamilyId: 'replacement'}),
    (f: ReturnType<typeof fixture>) => f.setSession({authorizationHorizon: '2026-10-26T14:00:00Z'}),
    (f: ReturnType<typeof fixture>) => f.setViewer({accountId: 'other'}),
    (f: ReturnType<typeof fixture>) => f.setViewer({profileId: 'other'}),
    (f: ReturnType<typeof fixture>) => f.setViewer({role: 'member'}),
    (f: ReturnType<typeof fixture>) => f.setViewer({authority: 'hosted'}),
    (f: ReturnType<typeof fixture>) => f.setLocation({pathname: '/device', href: '/device', state: {__TSR_key: 'successor-entry'}}),
    (f: ReturnType<typeof fixture>) => f.setCurrent(false),
    (f: ReturnType<typeof fixture>) => f.setConnection({...f.connection}),
    (f: ReturnType<typeof fixture>) => f.setNow(Date.parse('2026-10-26T13:00:00Z')),
  ]) {
    const f = fixture(), opening = f.capture()!;
    f.setSession({accessToken: 'renewed-test-token', tokenGeneration: '2', expiresAt: '2026-10-26T12:20:00Z'});
    assert.equal(opening.current(), true);replace(f);assert.equal(opening.current(), false);assert.equal(opening.signal.aborted, true);
  }
});
test('late original HTTP responses and old disposal cannot publish into a successor opening', async () => {
  const f = fixture();let resolve!: (value: unknown) => void;
  f.setResponse(() => new Promise(done => {resolve = done;}));
  const old = f.capture()!, pending = old.api.request(review, 'POST', {userCode: 'ABCD-EFGH'});
  old.dispose();const successor = f.capture()!;old.dispose();assert.equal(successor.current(), true);
  assert.equal(f.calls[0].signal?.aborted, true);resolve({review: true});
  await assert.rejects(pending, {name: 'AbortError'});assert.equal(successor.current(), true);successor.dispose();
});
test('caller abort remains terminal even if a transport resolves its original response', async () => {
  const f = fixture(), caller = new AbortController();let resolve!: (value: unknown) => void;
  f.setResponse(() => new Promise(done => {resolve = done;}));
  const opening = f.capture()!, pending = opening.api.request(review, 'POST', {}, caller.signal);
  caller.abort();resolve({review: true});await assert.rejects(pending, {name: 'AbortError'});opening.dispose();
});
