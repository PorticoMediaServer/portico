import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, ViewerService, type LocalSession} from '../src/index.ts';
import {parseLocalSession} from '../src/local-session.ts';
import {selectedViewerRecord} from '../src/viewer-selection.ts';
import {installationHeaders, setCredentialLock, setInstallationIdentity} from '../src/installation.ts';
import {parseRememberedServer, refreshStoredSession, type ConnectionEnvironment, type RememberedServer} from '../src/server-connections.ts';
import {LocalSessionRefresher} from '../src/session-refresh.ts';

/**
 * Lane C's device-bound sessions (939a746): 15-minute access tokens, rotating refresh
 * credentials, an exact-retry receipt through the family horizon, and plain 401 for reuse or revocation.
 */
const ORIGIN = 'https://portico.test';
const INSTALLATION = 'install_0123456789abcdef0123456789';
const MIN = 60_000;
const viewer = {accountId: 'acct', profileId: 'prof', serverId: 'srv', authority: 'local', role: 'owner'};
let clock = Date.UTC(2026, 8, 22, 12);
const iso = (ms: number) => new Date(ms).toISOString().replace(/\.\d+Z$/, 'Z');

class FakeAuthServer {
 role=viewer.role;
  generation = 1;
  fixedHorizon?: number;
  access = new Map<string, {generation: number; expires: number}>();
  refresh = 'refresh_000000000001';
  receipts = new Map<string, {requestId: string; response: unknown; until: number}>();
  revoked = false;
  migrated = false;
  refreshCalls = 0;
  rotations = 0;
  meCalls = 0;
  /** Commit the rotation, then lose the response (the client sees a network error). */
  dropAfterCommit = 0;
  failNext: number[] = [];
  /** Answers given to the next refresh requests in place of this server's own (a proxy, another layer). */
  refreshAnswers: (() => Response)[] = [];
  headers: Record<string, string>[] = [];
  constructor() { this.access.set('access_1', {generation: 1, expires: clock + 15 * MIN}); }
  envelope(access: string, refresh: string, generation: number) {
    return {accessToken: access, refreshToken: refresh, deviceId: 'dev_1', expiresAt: iso(this.access.get(access)!.expires), viewer:{...viewer,role:this.role}, sessionFamilyId: 'family_1', tokenGeneration: String(generation), authorizationHorizon: iso(this.fixedHorizon ?? clock + 90 * 24 * 60 * MIN)};
  }
  initial(): LocalSession { return parseLocalSession(this.envelope('access_1', this.refresh, 1)); }
  expireAccess() { for (const v of this.access.values()) v.expires = clock - 1; }
  fetch: typeof fetch = async (input, init = {}) => {
    const url = new URL(String(input));
    const headers: Record<string, string> = {};
    new Headers(init.headers).forEach((v, k) => { headers[k] = v; });
    this.headers.push(headers);
    const json = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});
    const fail = this.failNext.shift();
    if (fail) return json(fail, {error: {code: 'unavailable'}});
    if (url.pathname === '/v1/auth/refresh') {
      this.refreshCalls++;
      const other = this.refreshAnswers.shift();
      if (other) return other();
      const q = JSON.parse(String(init.body)) as {refreshToken: string; installationId: string; requestId: string};
      if (this.migrated) return json(401, {error: {code: 'session_migrated'}});
      if (this.revoked || q.installationId !== INSTALLATION || !/^[A-Za-z0-9_-]{1,128}$/.test(q.requestId)) return json(401, {error: {code: 'unauthorized'}});
      if (q.refreshToken === this.refresh) {
        for (const v of this.access.values()) v.expires = Math.min(v.expires, clock - 1); // predecessor access retired
        const g = ++this.generation, access = 'access_' + g, next = 'refresh_' + String(g).padStart(12, '0');
        this.access.set(access, {generation: g, expires: Math.min(clock + 15 * MIN, this.fixedHorizon ?? Infinity)});
        this.refresh = next;
        this.rotations++;
        const response = this.envelope(access, next, g);
        this.receipts.set(q.refreshToken, {requestId: q.requestId, response, until: Date.parse(response.authorizationHorizon)});
        if (this.dropAfterCommit > 0) { this.dropAfterCommit--; throw new TypeError('network connection lost'); }
        return json(200, response);
      }
      const receipt = this.receipts.get(q.refreshToken);
      if (receipt && receipt.requestId === q.requestId) return receipt.until > clock ? json(200, receipt.response) : json(400, {error: {code: 'invalid_request'}});
      return json(401, {error: {code: 'unauthorized'}}); // reuse of a retired credential
    }
    if (url.pathname === '/v1/me') {
      this.meCalls++;
      const token = headers.authorization?.replace(/^Bearer /, '') ?? '';
      const a = this.access.get(token);
      if (this.revoked || !a || a.expires <= clock) return json(401, {error: {code: 'unauthorized'}});
      return json(200, {viewer:{...viewer,role:this.role}});
    }
    if (url.pathname === '/v1/direct/sign-in') return json(401, {error: {code: 'unauthorized'}});
    return json(404, {error: {code: 'not_found'}});
  };
}

function memoryStore(record?: RememberedServer) {
  let value = record ? structuredClone(record) : undefined;
  return {
    read: async () => structuredClone(value),
    change: async (update: (v: RememberedServer | undefined) => RememberedServer | undefined) => { value = structuredClone(update(structuredClone(value))); },
    peek: () => value,
  };
}
const remembered = (session: LocalSession): RememberedServer => ({version: '1', selectionId: 'S'.repeat(32), logicalOrigin: ORIGIN, session, pin: {serverId: 'srv', publicKey: 'k'.repeat(43), fingerprint: 'f'.repeat(43)}, routes: {paired: [ORIGIN]}});

function fakeTimers() {
  const timers = new Map<number, {at: number; fn: () => void}>();
  let id = 0;
  return {
    setTimer: (fn: () => void, ms: number) => { timers.set(++id, {at: clock + ms, fn}); return id; },
    clearTimer: (t: unknown) => { timers.delete(t as number); },
    /** Advance the clock, firing due timers in order. */
    advance(ms: number) {
      const end = clock + ms;
      for (;;) {
        const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        timers.delete(due[0]); clock = Math.max(clock, due[1].at); due[1].fn();
      }
      clock = end;
    },
    pending: () => timers.size,
  };
}
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise(r => setImmediate(r)); };

function setup(t: import('node:test').TestContext, options: {session?: LocalSession} = {}) {
  t.mock.method(Date, 'now', () => clock);
  setInstallationIdentity({installationId: INSTALLATION, name: 'Test’s Mac', platform: 'macos', app: 'portico-web', appVersion: '1.0.0'});
  setCredentialLock(undefined);
  const server = new FakeAuthServer();
  const session = options.session ?? server.initial();
  const store = memoryStore(remembered(session));
  const env: ConnectionEnvironment = {storage: store};
  const timers = fakeTimers();
  const events = {rotated: [] as LocalSession[], signedOut: [] as string[]};
  const tab = () => {
    const api = new HttpLocalApi(ORIGIN, session.accessToken, server.fetch);
    const {refreshToken: _r, ...visible} = session;
    const refresher = new LocalSessionRefresher({env, api, session: visible, onRotated: s => events.rotated.push(s), onSignedOut: e => events.signedOut.push(e.code), setTimer: timers.setTimer, clearTimer: timers.clearTimer, random: () => 0});
    return {api, refresher};
  };
  t.after(() => setInstallationIdentity(undefined));
  return {server, store, env, timers, events, tab};
}

test('refreshes proactively two minutes before expiry and stores the rotation atomically', async t => {
  const {server, store, timers, events, tab} = setup(t);
  const {api, refresher} = tab();
  timers.advance(12 * MIN);
  await settle();
  assert.equal(server.refreshCalls, 0);
  timers.advance(1 * MIN + 1);
  await settle();
  assert.equal(server.rotations, 1);
  const saved = store.peek()!;
  assert.equal(saved.session.refreshToken, server.refresh, 'next refresh credential stored');
  assert.equal(saved.session.accessToken, 'access_2');
  assert.equal(saved.pendingRefresh, undefined, 'request id cleared once stored');
  assert.equal(refresher.current().accessToken, 'access_2');
  assert.equal('refreshToken' in refresher.current(), false, 'the refresh credential never leaves the store');
  assert.equal(events.rotated.length, 1);
  assert.deepEqual(await api.request('/v1/me'), {viewer});
  // …and keeps going: the next one is scheduled for the new expiry.
  timers.advance(13 * MIN + 1);
  await settle();
  assert.equal(server.rotations, 2);
  refresher.stop();
});

test('stopping inside rotation adoption stays stopped without reviving renewal timers', async t => {
  const {server, store, env, timers} = setup(t);
  const session = server.initial();
  const api = new HttpLocalApi(ORIGIN, session.accessToken, server.fetch);
  let refresher: LocalSessionRefresher;
  let rotations = 0;
  refresher = new LocalSessionRefresher({env, api, session,
    onRotated: () => { rotations++; refresher.stop(); },
    setTimer: timers.setTimer, clearTimer: timers.clearTimer, random: () => 0,
  });
  t.after(() => refresher.stop());
  const phases: string[] = [];
  refresher.subscribe(() => phases.push(refresher.getSnapshot().phase));
  timers.advance(13 * MIN + 1);
  await settle();
  assert.equal(rotations, 1, 'the actual stored refresh reached adoption');
  assert.equal(store.peek()?.session.accessToken, 'access_2');
  assert.equal(refresher.getSnapshot().phase, 'stopped');
  assert.deepEqual(phases, ['refreshing', 'stopped'], 'no active publication follows retirement');
  assert.equal(timers.pending(), 0, 'neither renewal nor permissions timer survived retirement');
  timers.advance(24 * 60 * MIN);
  await settle();
  assert.equal(server.refreshCalls, 1, 'the retired runtime never renews again');
  assert.equal(timers.pending(), 0);
});

test('a request that finds the token about to expire waits for one refresh first', async t => {
  const {server, tab} = setup(t);
  const {api, refresher} = tab();
  clock += 14.5 * MIN; // timers slept (app in background): no proactive refresh happened
  const results = await Promise.all(Array.from({length: 5}, () => api.request('/v1/me')));
  assert.equal(results.length, 5);
  assert.equal(server.rotations, 1, 'single flight');
  assert.equal(server.meCalls, 5, 'no request was sent with the stale token');
  refresher.stop();
});

test('concurrent 401s share one refresh and each request is retried once', async t => {
  const {server, tab} = setup(t);
  const {api, refresher} = tab();
  server.expireAccess(); // e.g. server clock ahead, or the token retired elsewhere
  const results = await Promise.all(Array.from({length: 8}, () => api.request<{viewer: unknown}>('/v1/me')));
  assert.ok(results.every(r => r.viewer));
  assert.equal(server.refreshCalls, 1);
  assert.equal(server.meCalls, 16, 'each request: one 401, one retry');
  refresher.stop();
});

test('revocation ends the sign-in: store cleared, requests fail with 401, no refresh loop', async t => {
  const {server, store, events, tab} = setup(t);
  const {api, refresher} = tab();
  server.revoked = true;
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.deepEqual(events.signedOut, ['refresh_refused']);
  assert.equal(store.peek(), undefined, 'stored sign-in removed');
  assert.equal(refresher.getSnapshot().phase, 'ended');
  const calls = server.refreshCalls;
  await assert.rejects(api.request('/v1/me'));
  assert.equal(server.refreshCalls, calls, 'an ended sign-in never refreshes again');
});

test('a session migration 0120 ended keeps its code (session_migrated), so apps re-admit silently', async t => {
  const {server, store, events, tab} = setup(t);
  const {api, refresher} = tab();
  server.revoked = true; // the old family no longer answers
  server.migrated = true;
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.deepEqual(events.signedOut, ['session_migrated']);
  assert.equal(store.peek(), undefined, 'the migrated credential is gone either way');
  assert.equal(refresher.getSnapshot().phase, 'ended');
});

const refreshDirectly = (env: ConnectionEnvironment, server: FakeAuthServer) => refreshStoredSession({env, api: new HttpLocalApi(ORIGIN, '', server.fetch), family: {sessionFamilyId: 'family_1', serverId: 'srv'}, rejected: 'access_1'});
const kept = (e: unknown) => (e as {signOut?: boolean}).signOut === false && (e as {code?: string}).code === 'refresh_unavailable_now';

test('a 403 that is not this server ending the sign-in keeps the stored credential', async t => {
  const {server, store, env} = setup(t);
  const answers: (() => Response)[] = [
    // A gateway or filter in front of the server: no Portico error at all.
    () => new Response('<html><body>Forbidden</body></html>', {status: 403, headers: {'Content-Type': 'text/html'}}),
    // The server itself, about something other than this sign-in.
    () => new Response(JSON.stringify({error: {code: 'origin_not_allowed', message: 'This origin is not permitted.', retryable: false}}), {status: 403, headers: {'Content-Type': 'application/json'}}),
  ];
  for (const answer of answers) {
    server.refreshAnswers.push(answer);
    await assert.rejects(refreshDirectly(env, server), kept);
    assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000001', 'credential kept');
    assert.ok(store.peek()!.pendingRefresh, 'the request id stays for the retry');
  }
  const next = await refreshDirectly(env, server);
  assert.equal(next.accessToken, 'access_2', 'the kept credential still renews');
  assert.equal(server.rotations, 1);
});

test('a renewal refused outside the account’s allowed hours keeps the sign-in and renews when the hours return', async t => {
  const {server, store, env} = setup(t);
  server.refreshAnswers.push(() => new Response(JSON.stringify({error: {code: 'outside_allowed_hours', message: 'This account cannot be used at this time of day.', retryable: false}}), {status: 403, headers: {'Content-Type': 'application/json'}}));
  await assert.rejects(refreshDirectly(env, server), (e: unknown) => (e as {signOut?: boolean}).signOut === false && (e as {code?: string}).code === 'outside_allowed_hours' && (e as {status?: number}).status === 403);
  assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000001', 'credential kept');
  const next = await refreshDirectly(env, server);
  assert.equal(next.accessToken, 'access_2', 'the kept credential renews once the server allows it');
  assert.equal(server.rotations, 1);
});

test('no 403 ends a sign-in, whatever its code', async t => {
  const {server, store, env} = setup(t);
  server.refreshAnswers.push(() => new Response(JSON.stringify({error: {code: 'access_limit', message: 'Not permitted.', retryable: false}}), {status: 403, headers: {'Content-Type': 'application/json'}}));
  await assert.rejects(refreshDirectly(env, server), kept);
  assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000001', 'credential kept');
});

test('a protected store that cannot be read right now keeps the sign-in and renews once it can', async t => {
  const {server, store, events, timers, tab} = setup(t);
  const {api, refresher} = tab();
  const read = store.read;
  let unreadable = 2;
  store.read = async () => { if (unreadable > 0) { unreadable--; throw new Error('The saved server connection could not be read or written.'); } return read(); };
  server.expireAccess();
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.deepEqual(events.signedOut, [], 'an unreadable store is not a refusal');
  assert.equal(refresher.getSnapshot().phase, 'retrying');
  assert.equal(refresher.getSnapshot().error?.code, 'store_unavailable');
  assert.equal(server.refreshCalls, 0, 'nothing was sent without the stored credential');
  assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000001', 'stored sign-in untouched');
  timers.advance(5_000);
  await settle();
  assert.equal(refresher.getSnapshot().phase, 'retrying', 'still unreadable: still kept');
  timers.advance(15_000);
  await settle();
  assert.equal(refresher.getSnapshot().phase, 'active');
  assert.deepEqual(await api.request('/v1/me'), {viewer}, 'the refused token is renewed from the kept credential');
  assert.equal(store.peek()!.session.accessToken, 'access_2');
  assert.deepEqual(events.signedOut, []);
  refresher.stop();
});

test('a saved sign-in the store reports as damaged ends here, without asking the server or waiting for it to mend', async t => {
  const {server, store, events, timers, tab} = setup(t);
  const {api, refresher} = tab();
  // What a platform store reports when the record is there but can never be read again.
  store.read = async () => { throw Object.assign(new Error('Saved server credentials are incomplete.'), {code: 'invalid_saved_connection'}); };
  server.expireAccess();
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.deepEqual(events.signedOut, ['saved_sign_in_damaged']);
  assert.equal(refresher.getSnapshot().phase, 'ended', 'a definite end, so the app can offer sign-in');
  assert.equal(refresher.getSnapshot().error?.status, 0, 'not presented as the server’s refusal');
  assert.equal(server.refreshCalls, 0);
  assert.equal(timers.pending(), 0, 'nothing is retried');
  assert.ok(store.peek(), 'the record itself is left for a new sign-in to replace');
});

test('a saved sign-in that reads but is not a valid record ends the same way', async t => {
  const {store, events, tab} = setup(t);
  const {api, refresher} = tab();
  await store.change(() => ({version: '1', nonsense: true}) as unknown as RememberedServer);
  await refresher.recover('access_1');
  assert.deepEqual(events.signedOut, ['saved_sign_in_damaged']);
  assert.equal(refresher.getSnapshot().phase, 'ended');
  void api;
});

test('a store that reads but no longer holds this sign-in still ends it', async t => {
  const {server, store, events, tab} = setup(t);
  const {api, refresher} = tab();
  await store.change(() => undefined);
  server.expireAccess();
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.deepEqual(events.signedOut, ['session_replaced']);
  assert.equal(refresher.getSnapshot().phase, 'ended');
});

test('reusing a retired refresh credential is refused and signs out', async t => {
  const {server, store, events, tab} = setup(t);
  const stale = store.peek()!;
  const {refresher} = tab();
  await refresher.recover('access_1'); // rotates to generation 2
  assert.equal(server.rotations, 1);
  // A backup restored the old record (predecessor credential, a new request id).
  await store.change(() => stale);
  await assert.rejects(refreshStoredSession({env: {storage: store}, api: new HttpLocalApi(ORIGIN, '', server.fetch), family: {sessionFamilyId: 'family_1', serverId: 'srv'}, rejected: 'access_1'}), (e: unknown) => (e as {signOut?: boolean}).signOut === true);
  assert.equal(store.peek(), undefined);
  assert.deepEqual(events.signedOut, []);
  refresher.stop();
});

test('a refresh whose answer was lost is replayed with the same request id', async t => {
  const {server, store, events, timers, tab} = setup(t);
  const {api, refresher} = tab();
  server.dropAfterCommit = 1;
  server.expireAccess();
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  const pending = store.peek()!.pendingRefresh;
  assert.ok(pending, 'request id persisted before sending');
  assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000001', 'credential kept on a transient failure');
  assert.equal(refresher.getSnapshot().phase, 'retrying');
  assert.deepEqual(events.signedOut, []);
  timers.advance(5_000);
  await settle();
  assert.equal(server.rotations, 1, 'the retry replayed the receipt; no second rotation');
  assert.equal(store.peek()!.session.accessToken, 'access_2');
  assert.equal(store.peek()!.pendingRefresh, undefined);
  assert.deepEqual(await api.request('/v1/me'), {viewer});
  refresher.stop();
});

test('an app restarted mid-refresh finishes it from the stored request id', async t => {
  const {server, store, env} = setup(t);
  await store.change(r => ({...r!, pendingRefresh: 'req_before_crash'}));
  // The first process sent it and died before storing the answer.
  server.receipts.set(server.refresh, {requestId: 'req_before_crash', response: undefined, until: 0});
  server.dropAfterCommit = 1;
  await assert.rejects(refreshStoredSession({env, api: new HttpLocalApi(ORIGIN, '', server.fetch), family: {sessionFamilyId: 'family_1', serverId: 'srv'}}));
  assert.equal(store.peek()!.pendingRefresh, 'req_before_crash');
  const next = await refreshStoredSession({env, api: new HttpLocalApi(ORIGIN, '', server.fetch), family: {sessionFamilyId: 'family_1', serverId: 'srv'}});
  assert.equal(next.accessToken, 'access_2');
  assert.equal(server.rotations, 1);
  assert.equal(parseRememberedServer(await store.read())!.session.refreshToken, server.refresh);
});

test('a second tab adopts the first tab’s rotation instead of refreshing (or signing out)', async t => {
  const {server, events, tab} = setup(t);
  const a = tab(), b = tab();
  await a.api.request('/v1/me');
  server.expireAccess();
  await a.api.request('/v1/me'); // tab A rotates; access_1 is retired
  assert.equal(server.rotations, 1);
  assert.deepEqual(await b.api.request('/v1/me'), {viewer}, 'tab B recovers from the store');
  assert.equal(server.refreshCalls, 1, 'tab B made no refresh request');
  assert.equal(b.refresher.current().accessToken, 'access_2');
  assert.deepEqual(events.signedOut, []);
  a.refresher.stop(); b.refresher.stop();
});

test('without a refresh credential (plain HTTP) the sign-in ends when the access token does', async t => {
  t.mock.method(Date, 'now', () => clock);
  const server0 = new FakeAuthServer();
  const {refreshToken: _r, ...plain} = server0.initial();
  const {server, events, tab} = setup(t, {session: plain as LocalSession});
  const {api} = tab();
  server.expireAccess();
  clock += 15 * MIN;
  await assert.rejects(api.request('/v1/me'), (e: unknown) => e instanceof ApiError && e.status === 401);
  assert.equal(server.refreshCalls, 0);
  assert.deepEqual(events.signedOut, ['refresh_unavailable']);
});

test('credential requests carry the installation claim; other requests never do', async t => {
  const {server} = setup(t);
  const api = new HttpLocalApi(ORIGIN, '', server.fetch);
  await assert.rejects(api.request('/v1/direct/sign-in', 'POST', {username: 'u', password: 'p'}));
  await api.request('/v1/me').catch(() => {});
  const [signIn, me] = server.headers;
  assert.equal(signIn!['x-portico-installation-id'], INSTALLATION);
  assert.equal(signIn!['x-portico-device-name'], "Test's Mac", 'folded to ASCII for the header');
  assert.equal(me!['x-portico-installation-id'], undefined);
  assert.throws(() => setInstallationIdentity({installationId: 'short', name: '', platform: '', app: '', appVersion: ''}));
  setInstallationIdentity(undefined);
  assert.deepEqual(installationHeaders(), {});
});

test('device-bound envelopes parse; the refresh credential stays out of viewer records and snapshots', async t => {
  const {server} = setup(t);
  const session = server.initial();
  assert.equal(session.deviceId, 'dev_1');
  assert.equal(session.refreshToken, 'refresh_000000000001');
  assert.throws(() => parseLocalSession({...server.envelope('access_1', 'bad token!', 1)}));
  assert.equal('refreshToken' in selectedViewerRecord(ORIGIN, session).session, false);
  const viewers = new ViewerService();
  viewers.select(ORIGIN, session);
  const generation = viewers.getSnapshot().generation;
  assert.equal('refreshToken' in viewers.getSnapshot().session!, false);
  const renewed = parseLocalSession(server.envelope('access_1', 'refresh_000000000009', 2));
  assert.equal(viewers.rotate(renewed), true);
  assert.equal(viewers.getSnapshot().generation, generation, 'rotation is not a new viewer');
  assert.equal(viewers.rotate({...renewed, sessionFamilyId: 'other'}), false);
  assert.equal(viewers.rotate({...renewed, tokenGeneration: '1'}), false, 'never rolls back');
});

test('two tabs with no lock between them (plain HTTP) send one renewal request id, so the server never sees a reused credential', async t => {
  const {server, store, env} = setup(t);
  // No cross-tab lock: each tab's "lock" lets its own work straight through, as a tab-local lock does for another tab.
  setCredentialLock((_name, work) => work());
  t.after(() => setCredentialLock(undefined));
  server.expireAccess();
  const ids: string[] = [];
  const fetch: typeof globalThis.fetch = async (input, init) => {
    if (String(input).endsWith('/v1/auth/refresh')) ids.push((JSON.parse(String(init?.body)) as {requestId: string}).requestId);
    return server.fetch(input, init);
  };
  const tab = () => refreshStoredSession({env, api: new HttpLocalApi(ORIGIN, 'access_1', fetch), family: {sessionFamilyId: 'family_1', serverId: 'srv'}, rejected: 'access_1'});
  const [a, b] = await Promise.all([tab(), tab()]);
  assert.equal(a.accessToken, 'access_2');
  assert.equal(b.accessToken, 'access_2', 'the second tab gets the same renewed sign-in, not a refusal');
  assert.equal(server.rotations, 1);
  assert.equal(new Set(ids).size, 1, 'both tabs named the same request');
  assert.equal(store.peek()!.session.refreshToken, 'refresh_000000000002');
  assert.equal(store.peek()!.pendingRefresh, undefined);
});


test('live role changes refresh the viewer and protected record before token renewal',async t=>{
 const {server,store,timers,events,tab}=setup(t);const {refresher}=tab();
 server.role='member';timers.advance(30_000);await settle();
 assert.equal(refresher.current().viewer.role,'member');assert.equal(store.peek()!.session.viewer.role,'member');assert.equal(events.rotated.at(-1)!.viewer.role,'member');assert.equal(server.rotations,0);
 server.role='admin';assert.equal(await refresher.permissionDenied(),true);assert.equal(refresher.current().viewer.role,'admin');
 const viewers=new ViewerService();viewers.select(ORIGIN,server.initial());server.role='member';assert.equal(viewers.rotate({...refresher.current(),viewer:{...refresher.current().viewer,role:'member'}}),true);
});

test('credential renewal accepts the server’s current role without changing viewer identity',async t=>{
 const {server,tab}=setup(t);const {refresher}=tab();server.role='member';server.expireAccess();
 await refresher.recover('access_1');assert.equal(refresher.current().viewer.role,'member');assert.equal(server.rotations,1);
});


test('fixed short authorization horizon rotates once proactively, then waits for expiry while 401 still recovers', async t => {
  t.mock.method(Date, 'now', () => clock);
  const horizon = clock + 90_000;
  const initialServer = new FakeAuthServer();
  initialServer.fixedHorizon = horizon;
  initialServer.access.get('access_1')!.expires = horizon;
  const {server, timers, events, tab} = setup(t, {session: initialServer.initial()});
  server.fixedHorizon = horizon;
  const {refresher} = tab();
  t.after(() => refresher.stop());
  timers.advance(0); await settle();
  assert.equal(server.refreshCalls, 1);
  for (let i = 0; i < 8; i++) {refresher.wake(); await refresher.token(refresher.current().accessToken); timers.advance(1_000); await settle();}
  assert.equal(server.refreshCalls, 1, 'unchanged expiry cannot cause another proactive rotation');
  const rejected = refresher.current().accessToken;
  const recovered = await Promise.all([refresher.recover(rejected), refresher.recover(rejected)]);
  assert.ok(recovered[0]); assert.equal(recovered[0], recovered[1]);
  assert.equal(server.refreshCalls, 2, 'concurrent rejected bearer recovery joins one flight');
  refresher.wake(); timers.advance(1_000); await settle(); assert.equal(server.refreshCalls, 2);
  server.revoked = true; timers.advance(horizon - clock); await settle();
  assert.equal(server.refreshCalls, 3); assert.equal(events.signedOut.length, 1);
});

test('an initially short sliding family still gets its first proactive renewal', async t => {
  t.mock.method(Date, 'now', () => clock);
  const initialServer = new FakeAuthServer(); initialServer.access.get('access_1')!.expires = clock + 90_000;
  const {server, timers, tab} = setup(t, {session: initialServer.initial()});
  const {refresher} = tab(); t.after(() => refresher.stop());
  timers.advance(0); await settle();
  assert.equal(server.refreshCalls, 1);
  assert.ok(Date.parse(refresher.current().expiresAt) > clock + 90_000);
  timers.advance(13 * MIN); await settle();
  assert.equal(server.refreshCalls, 2, 'advancing expiry schedules normal proactive refresh');
});


test('an expired replay is durably adopted and renewed inside one lock before concurrent callers receive access', async t => {
  const {server, store, env} = setup(t);
  const sent: {refreshToken: string; requestId: string}[] = [];
  const api = new HttpLocalApi(ORIGIN, '', async (input, init) => {
    const body = JSON.parse(String(init?.body));
    sent.push(body);
    assert.equal(store.peek()?.session.refreshToken, body.refreshToken, 'successor was saved before use');
    assert.equal(store.peek()?.pendingRefresh, body.requestId, 'each credential uses its durable request id');
    return server.fetch(input, init);
  });
  const refresh = () => refreshStoredSession({env, api, family: {sessionFamilyId: 'family_1', serverId: 'srv'}, rejected: 'access_1'});
  server.dropAfterCommit = 1;
  await assert.rejects(refresh(), /network connection lost/);
  const lostId = store.peek()!.pendingRefresh;
  clock += 16 * MIN;
  const [a, b] = await Promise.all([refresh(), refresh()]);
  assert.equal(a.accessToken, 'access_3');assert.equal(b.accessToken, 'access_3');
  assert.ok(Date.parse(a.expiresAt) > clock);
  assert.equal(server.rotations, 2, 'one lost rotation and one successor rotation');
  assert.equal(sent.length, 3, 'the concurrent waiter adopts the fresh durable result');
  assert.equal(sent[1]!.requestId, lostId);
  assert.notEqual(sent[2]!.requestId, lostId);
  assert.equal(store.peek()?.session.accessToken, 'access_3');assert.equal(store.peek()?.pendingRefresh, undefined);
});

test('an expired successor is never rotated before a failed durable adoption can be retried', async t => {
  const {server, store, env} = setup(t);
  server.dropAfterCommit = 1;
  await assert.rejects(refreshDirectly(env, server));
  const original = structuredClone(store.peek());
  clock += 16 * MIN;
  let fail = true;
  const flaky: ConnectionEnvironment = {storage: {read: store.read, change: async update => {
    if (fail) throw new Error('secure store busy');
    await store.change(update);
  }}};
  await assert.rejects(refreshDirectly(flaky, server), {code: 'store_unavailable', signOut: false});
  assert.equal(server.rotations, 1, 'no request spends the unpersisted successor');
  assert.deepEqual(store.peek(), original, 'predecessor and exact receipt id remain recoverable');
  fail = false;
  const restarted = await refreshDirectly(flaky, server);
  assert.equal(restarted.accessToken, 'access_3');assert.equal(server.rotations, 2);
});

test('expired replay validation retains every family, viewer, pin, installation and horizon fence', async t => {
  const {server, store, env} = setup(t);
  const original = structuredClone(store.peek()!);
  const expired = {...server.envelope('access_1', 'refresh_000000000002', 2), expiresAt: iso(clock - MIN)};
  const invalid = [
    {...expired, sessionFamilyId: 'other'},
    {...expired, tokenGeneration: '1'},
    {...expired, viewer: {...viewer, authority: 'hosted'}},
    {...expired, viewer: {...viewer, accountId: 'other'}},
    {...expired, viewer: {...viewer, profileId: 'other'}},
    {...expired, viewer: {...viewer, serverId: 'other'}},
    {...expired, serverIdentity: {publicKey: 'x'.repeat(43), fingerprint: 'y'.repeat(43)}},
    {...expired, installationId: 'another_'.repeat(6)},
    {...expired, deviceId: 'another_device'},
    {...expired, authorizationHorizon: iso(clock - 1)},
    {...expired, refreshToken: original.session.refreshToken},
    {...expired, unexpected: true},
  ];
  for (const answer of invalid) {
    await store.change(() => ({...original, pendingRefresh: 'held_receipt'}));
    server.refreshAnswers.push(() => new Response(JSON.stringify(answer), {headers: {'Content-Type': 'application/json'}}));
    await assert.rejects(refreshDirectly(env, server), {code: 'refresh_invalid', signOut: false});
    assert.equal(store.peek()?.session.tokenGeneration, '1');
    assert.equal(store.peek()?.pendingRefresh, 'held_receipt');
  }
  assert.equal(server.rotations, 0);
});

test('a losing refresh claim never dispatches when its verification read fails', async t => {
  const {server, store} = setup(t);
  let reads = 0;
  const env: ConnectionEnvironment = {storage: {
    read: async () => { if (++reads === 2) throw new Error('temporarily locked'); return store.read(); },
    change: async update => {
      await store.change(current => ({...current!, pendingRefresh: 'winning_other_context'}));
      await store.change(update);
    },
  }};
  await assert.rejects(refreshDirectly(env, server), {code: 'refresh_unavailable_now', signOut: false});
  assert.equal(server.refreshCalls, 0);
  assert.equal(store.peek()?.pendingRefresh, 'winning_other_context');
  assert.equal(store.peek()?.session.accessToken, 'access_1');
});

test('a missing or replaced post-claim selection ends only the stale waiter without dispatch', async t => {
  const {server, store} = setup(t);
  const original = structuredClone(store.peek()!);
  for (const replacement of [undefined, {...original, selectionId: 'N'.repeat(32)}, {...original, session: {...original.session, sessionFamilyId: 'another_family'}}]) {
    await store.change(() => original);
    const env: ConnectionEnvironment = {storage: {read: store.read, change: async () => {await store.change(() => replacement);}}};
    await assert.rejects(refreshDirectly(env, server), {code: 'session_replaced', signOut: true});
    assert.equal(server.refreshCalls, 0);
    assert.deepEqual(store.peek(), replacement);
  }
});


test('an expired receipt whose guarded adoption becomes a no-op is not spent', async t => {
  const {server, store} = setup(t);
  await store.change(current=>({...current!,pendingRefresh:'saved_receipt'}));
  const answer={...server.envelope('access_1','refresh_000000000002',2),expiresAt:iso(clock-MIN)};
  server.refreshAnswers.push(()=>new Response(JSON.stringify(answer),{headers:{'Content-Type':'application/json'}}));
  const env:ConnectionEnvironment={storage:{read:store.read,change:async update=>{
    await store.change(current=>({...current!,selectionId:'N'.repeat(32)}));
    await store.change(update);
  }}};
  await assert.rejects(refreshDirectly(env,server),{code:'refresh_unavailable_now',signOut:false});
  assert.equal(server.refreshCalls,1);assert.equal(store.peek()?.session.tokenGeneration,'1');
  assert.equal(store.peek()?.pendingRefresh,'saved_receipt');assert.equal(store.peek()?.selectionId,'N'.repeat(32));
});

test('a fresh answer that could not be saved remains recoverable after restart and access expiry', async t => {
  const {server,store,env}=setup(t);let fail=true;
  const first:ConnectionEnvironment={storage:{read:store.read,change:async update=>{
    const next=update(await store.read());
    if(fail&&next?.session.tokenGeneration==='2'){fail=false;throw new Error('write failed');}
    await store.change(()=>next);
  }}};
  assert.equal((await refreshDirectly(first,server)).accessToken,'access_2');
  assert.equal(store.peek()?.session.accessToken,'access_1');assert.ok(store.peek()?.pendingRefresh);
  clock+=16*MIN;
  assert.equal((await refreshDirectly(env,server)).accessToken,'access_3');assert.equal(server.rotations,2);
});

test('an unhelpful server cannot create an unbounded chain of expired successors',async t=>{
  const {server,store,env}=setup(t);
  await store.change(current=>({...current!,pendingRefresh:'saved_receipt'}));
  for(const generation of [2,3,4]){
    const answer={...server.envelope('access_1','refresh_'+String(generation).padStart(12,'0'),generation),expiresAt:iso(clock-MIN)};
    server.refreshAnswers.push(()=>new Response(JSON.stringify(answer),{headers:{'Content-Type':'application/json'}}));
  }
  await assert.rejects(refreshDirectly(env,server),{code:'refresh_unavailable_now',signOut:false});
  assert.equal(server.refreshCalls,2);assert.equal(store.peek()?.session.tokenGeneration,'3');assert.equal(store.peek()?.pendingRefresh,undefined);
});

test('overflowing transport Retry-After survives the real credential error adapter for detailed permission admission',async t=>{
  const {server,timers,events,tab}=setup(t),{refresher}=tab();t.after(()=>refresher.stop());
  server.refreshAnswers.push(()=>new Response(JSON.stringify({error:{code:'unavailable'}}),{status:429,headers:{'Content-Type':'application/json','Retry-After':'9'.repeat(400)}}));
  await refresher.recover('access_1');assert.equal(server.refreshCalls,1);assert.equal(refresher.getSnapshot().phase,'retrying');
  assert.equal(refresher.getSnapshot().error?.retryAfterSeconds,Infinity);
  // The existing credential retry delay is still local/capped; the new permission
  // obligation remembers the original full floor independently.
  clock+=60_001;
  assert.deepEqual(await refresher.reconcilePermissions(),{kind:'deferred',nextAllowedAt:Number.MAX_VALUE,reason:'server-floor'});
  assert.equal(server.meCalls,0);assert.equal(server.refreshCalls,1);assert.deepEqual(events.signedOut,[]);
  assert.ok(timers.pending()>0);
});
