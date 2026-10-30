import test from 'node:test';
import assert from 'node:assert/strict';
import {LocalSessionRefresher} from '../src/session-refresh.ts';
import {SessionRefreshError} from '../src/server-connections.ts';
import type {LocalSession} from '../src/index.ts';

const settle = () => new Promise(resolve => setTimeout(resolve, 0));

function fixture(options: {random?: () => number} = {}) {
  let clock = Date.parse('2026-10-13T12:00:00.000Z');
  const timers: {at: number; fn: () => void}[] = [];
  const session: LocalSession = {
    accessToken: 'access_1', expiresAt: new Date(clock + 30_000).toISOString(), authorizationHorizon: new Date(clock + 86_400_000).toISOString(),
    sessionFamilyId: 'family', tokenGeneration: '1', viewer: {accountId: 'a', profileId: 'p', serverId: 'srv', authority: 'local', role: 'member'},
  } as LocalSession;
  let calls = 0;
  let outcome: () => Promise<LocalSession> = async () => { throw new Error('unset'); };
  const refresher = new LocalSessionRefresher({
    env: {storage: {} as never}, session,
    api: {request: async () => { throw new Error('not used'); }, setAccessToken: () => {}, setAuthRecovery: () => {}} as never,
    now: () => clock, random: options.random ?? (() => 0),
    setTimer: (fn, ms) => { const t = {at: clock + ms, fn}; timers.push(t); return t; },
    clearTimer: t => { const i = timers.indexOf(t as {at: number; fn: () => void}); if (i >= 0) timers.splice(i, 1); },
    refresh: () => { calls++; return outcome(); },
  });
  const advance = (ms: number) => {
    const end = clock + ms;
    for (;;) {
      const due = timers.filter(t => t.at <= end).sort((a, b) => a.at - b.at)[0];
      if (!due) break;
      timers.splice(timers.indexOf(due), 1); clock = Math.max(clock, due.at); due.fn();
    }
    clock = end;
  };
  const busy = (retryAfterSeconds?: number) => { const e = new SessionRefreshError('refresh_unavailable_now', 'busy', false, 503); if (retryAfterSeconds) e.retryAfterSeconds = retryAfterSeconds; return e; };
  return {refresher, advance, busy, calls: () => calls, set: (next: () => Promise<LocalSession>) => { outcome = next; }, nextTimer: () => Math.min(...timers.map(t => t.at)) - clock, session};
}

test('after a failed renewal, requests that find the token expired do not each start another one', async () => {
  const f = fixture();
  f.set(async () => { throw f.busy(); });
  assert.equal(await f.refresher.recover('access_1'), undefined);
  assert.equal(f.calls(), 1);
  // Three more requests hit a 401 before the retry is due.
  for (let i = 0; i < 3; i++) assert.equal(await f.refresher.recover('access_1'), undefined);
  assert.equal(await f.refresher.token('access_1'), 'access_1');
  assert.equal(f.calls(), 1, 'no renewal before its retry is due');
  f.advance(5_000);
  await settle();
  assert.equal(f.calls(), 2, 'the scheduled retry runs');
  f.refresher.stop();
});

test('the server’s wait is honoured by the timer, by requests and on returning to the foreground', async () => {
  const f = fixture();
  f.set(async () => { throw f.busy(60); });
  await f.refresher.recover('access_1');
  assert.equal(f.calls(), 1);
  assert.equal(f.nextTimer(), 60_000, 'the retry waits the server’s minute, not the app’s five seconds');
  f.advance(20_000);
  f.refresher.wake();
  await f.refresher.recover('access_1');
  await settle();
  assert.equal(f.calls(), 1, 'neither a return to the foreground nor a request goes inside the server’s wait');
  f.set(async () => ({...f.session, accessToken: 'access_2', expiresAt: new Date(Date.parse(f.session.expiresAt) + 900_000).toISOString()}));
  f.advance(40_000);
  await settle();
  assert.equal(f.calls(), 2);
  assert.equal(f.refresher.current().accessToken, 'access_2');
  f.refresher.stop();
});

test('returning to the foreground may cut the app’s own backoff short', async () => {
  const f = fixture();
  f.set(async () => { throw f.busy(); });
  await f.refresher.recover('access_1');
  f.advance(1_000);
  f.refresher.wake();
  await settle();
  assert.equal(f.calls(), 2);
  f.refresher.stop();
});

test('retries are spread: up to a quarter longer, never shorter', async () => {
  const f = fixture({random: () => 1});
  f.set(async () => { throw f.busy(); });
  await f.refresher.recover('access_1');
  assert.equal(f.nextTimer(), 6_250);
  const g = fixture({random: () => 1});
  g.set(async () => { throw g.busy(60); });
  await g.refresher.recover('access_1');
  assert.equal(g.nextTimer(), 75_000, 'spread beyond the server’s wait, never inside it');
  f.refresher.stop(); g.refresher.stop();
});
