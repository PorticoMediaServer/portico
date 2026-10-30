import assert from 'node:assert/strict';
import {test} from 'node:test';
import {SetupApprovalFlow} from '../src/services/setup-approval-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {HostedSetupClaimReview} from '../src/services/setup-claim.ts';

function fixture() {
  let session = {account: {id: 'original-account', username: 'owner', displayName: 'Owner'}, familyId: 'original-family', accessToken: 'fixture-access'};
  let monotonic = 0;
  const accountListeners = new Set<() => void>();
  const timers = new Map<number, {callback: () => void; delay: number}>();
  let nextTimer = 0, reviewCalls = 0;
  let now = Date.now(), rejectDecision = false, completeCalls = 0, disposed = false, accountReadsClosed = false;
  const navigations: string[] = [], decisions: string[] = [];
  let completion = async () => 'https://original.example/auth/portico-return#transaction=original-transaction';
  const account = {service: {getSnapshot: () => ({session}), subscribe: (listener: () => void) => {accountListeners.add(listener); return () => accountListeners.delete(listener);}, synchronize: async () => {if (accountReadsClosed) throw new Error('account read unavailable');}, accessSession: async () => session}} as unknown as ServicesAccount;
  const review = {identity: {accountId: session.account.id, familyId: session.familyId}, name: 'Original server', address: 'https://original.example',
    requestedAt: new Date(now - 1000).toISOString(), expiresAt: new Date(now + 120000).toISOString(), status: 'pending'} as HostedSetupClaimReview;
  const controller = {review: async () => {reviewCalls++; return review;}, decide: async (decision: string) => {decisions.push(decision); if (rejectDecision) throw new Error('decision unavailable');},
    complete: async () => {completeCalls++; return completion();}, dispose: () => {disposed = true;}} as unknown as ConstructorParameters<typeof SetupApprovalFlow>[1];
  const flow = new SetupApprovalFlow(account, controller, location => navigations.push(location), () => now, {
    monotonicNow: () => monotonic,
    setTimeout: (callback, delay) => {const id = ++nextTimer; timers.set(id, {callback, delay}); return id as unknown as ReturnType<typeof setTimeout>;},
    clearTimeout: id => {timers.delete(id as unknown as number);},
  });
  return {flow, review, navigations, decisions, completion: (work: typeof completion) => {completion = work;},
    rejectDecision: () => {rejectDecision = true;}, replaceFamily: () => {session = {...session, familyId: 'successor-family'}; accountListeners.forEach(listener => listener());},
    closeAccountReads: () => {accountReadsClosed = true;},
    expireClock: () => {now += 180000;}, advance: (ms: number) => {now += ms; monotonic += ms;},
    wall: (ms: number) => {now += ms;}, tick: (ms: number) => {monotonic += ms;},
    timers, subscribers: () => accountListeners.size, reviewCalls: () => reviewCalls,
    fire: () => {const entry = timers.entries().next().value; assert.ok(entry); timers.delete(entry[0]); entry[1].callback();},
    refresh: () => {session = {...session, accessToken: 'refreshed'}; accountListeners.forEach(listener => listener());},
    restoreFamily: () => {session = {...session, familyId: 'original-family'}; accountListeners.forEach(listener => listener());},
    absentFamily: () => {session = {...session, familyId: ''}; accountListeners.forEach(listener => listener());}, completeCalls: () => completeCalls, disposed: () => disposed};
}

test('confirmed approval retries the same protected completion after refusal without a public-address bypass', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => {throw new Error('completion unavailable');});
  await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal(f.flow.getSnapshot().approved, true);
  f.flow.returnReviewed(); assert.deepEqual(f.navigations, []);
  f.completion(async () => 'https://original.example/auth/portico-return#transaction=original-transaction');
  await f.flow.complete();
  assert.equal(f.completeCalls(), 2); assert.deepEqual(f.decisions, ['approve']);
  assert.deepEqual(f.navigations, ['https://original.example/auth/portico-return#transaction=original-transaction']);
  f.flow.dispose();
});

test('a protected completed return has no second fallible account read after its attempt is spent', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => {f.closeAccountReads(); return 'https://original.example/auth/portico-return#transaction=original-transaction';});
  await f.flow.complete();
  assert.deepEqual(f.navigations, ['https://original.example/auth/portico-return#transaction=original-transaction']);
  assert.equal(f.completeCalls(), 1); f.flow.dispose();
});

test('reviewed error return refuses replacement, expiry and reentrant family change before navigation', async () => {
  for (const change of ['family', 'expiry', 'reentrant', 'dispose', 'none'] as const) {
    const f = fixture(); await f.flow.start(); f.rejectDecision(); await f.flow.decide('approve');
    if (change === 'family') f.replaceFamily();
    if (change === 'expiry') f.expireClock();
    if (change === 'dispose') f.flow.dispose();
    let changed = false;
    const off = f.flow.subscribe(() => {if (change === 'reentrant' && !changed && f.flow.getSnapshot().busy) {changed = true; f.replaceFamily();}});
    f.flow.returnReviewed(); off();
    assert.deepEqual(f.navigations, change === 'none' ? ['https://original.example'] : [], change);
    assert.equal(f.completeCalls(), 0); f.flow.dispose();
  }
});

test('a late protected completion cannot navigate after the original approval expires', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  let finish!: (location: string) => void;
  f.completion(() => new Promise(resolve => {finish = resolve;}));
  const pending = f.flow.complete(); f.flow.expire();
  finish('https://original.example/auth/portico-return#transaction=original-transaction'); await pending;
  assert.deepEqual(f.navigations, []); assert.equal(f.disposed(), true); assert.equal(f.flow.getSnapshot().phase, 'expired');
  f.flow.dispose();
});

test('Return before the secure return is ready says so and retries, never a silent no-op', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => undefined as unknown as string);
  await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().approved, true); assert.equal(f.flow.getSnapshot().busy, false);
  assert.deepEqual(f.navigations, []);
  // Try again uses the same protected completion; once ready, it returns.
  f.completion(async () => 'https://original.example/auth/portico-return#transaction=original-transaction');
  await f.flow.complete();
  assert.equal(f.completeCalls(), 2);
  assert.deepEqual(f.navigations, ['https://original.example/auth/portico-return#transaction=original-transaction']);
  f.flow.dispose();
});

test('Return is offered only once Hosted reports the approved claim installed', async () => {
  const f = fixture(); Object.assign(f.review, {status: 'approved', installed: true});
  await f.flow.start();
  assert.equal(f.flow.getSnapshot().phase, 'approved'); assert.equal(f.flow.getSnapshot().installed, true); f.flow.dispose();
  const g = fixture(); Object.assign(g.review, {status: 'approved', installed: false});
  await g.flow.start();
  assert.equal(g.flow.getSnapshot().phase, 'approved'); assert.equal(g.flow.getSnapshot().installed, undefined);
  g.completion(async () => undefined as never);
  Object.assign(g.review, {installed: true});
  await g.flow.complete();
  assert.equal(g.flow.getSnapshot().installed, true); assert.deepEqual(g.navigations, []); g.flow.dispose();
  const h = fixture(); await h.flow.start(); await h.flow.decide('approve');
  assert.equal(h.flow.getSnapshot().installed, undefined); h.flow.dispose();
});

// A slow or failing Portico Services keeps the server finishing after Approve. Try again
// keeps looking it up, and past two minutes the page says so honestly (the rig's d2).
test('finishing long after Approve is marked slow; shortly after it is not', async () => {
  const f = fixture();
  (f.review as {expiresAt: string}).expiresAt = new Date(Date.now() + 30 * 60_000).toISOString();
  await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => undefined as unknown as string);
  f.advance(30_000); await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().slow, undefined);
  f.advance(150_000); await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().slow, true);
  f.completion(async () => 'https://original.example/auth/portico-return#transaction=original-transaction');
  await f.flow.complete();
  assert.deepEqual(f.navigations, ['https://original.example/auth/portico-return#transaction=original-transaction']);
  f.flow.dispose();
});

function longFixture() {
  const f = fixture();
  Object.assign(f.review, {expiresAt: new Date(Date.now() + 30 * 60_000).toISOString()});
  return f;
}

test('passive approval publishes only slow at the exact monotonic boundary, with or without early Return', async () => {
  for (const earlyReturn of [false, true]) {
    const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
    if (earlyReturn) {f.completion(async () => undefined as never); await f.flow.complete();}
    const before = f.flow.getSnapshot(), reads = f.reviewCalls(), calls = f.completeCalls();
    assert.equal(f.timers.size, 1); assert.equal(f.subscribers(), 1);
    f.tick(119999); f.fire();
    assert.deepEqual(f.flow.getSnapshot(), before);
    assert.equal([...f.timers.values()][0].delay, 1);
    f.tick(1); f.fire();
    assert.deepEqual(f.flow.getSnapshot(), {...before, slow: true});
    assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0);
    assert.equal(f.reviewCalls(), reads); assert.equal(f.completeCalls(), calls);
    assert.deepEqual(f.decisions, ['approve']); assert.deepEqual(f.navigations, []);
    f.completion(async () => undefined as never); await f.flow.complete();
    assert.equal(f.flow.getSnapshot().slow, true);
    f.completion(async () => 'https://original.example/return'); await f.flow.complete();
    assert.deepEqual(f.navigations, ['https://original.example/return']); f.flow.dispose();
  }
});

test('restored observation seeds once, installed Return-ready never schedules', async () => {
  for (const installed of [false, true]) {
    const f = longFixture(); Object.assign(f.review, {status: 'approved', installed});
    await f.flow.start();
    assert.equal(f.timers.size, installed ? 0 : 1); assert.equal(f.subscribers(), installed ? 0 : 1);
    if (!installed) {
      f.tick(60000); await f.flow.start(); f.tick(60000); f.fire();
      assert.equal(f.flow.getSnapshot().slow, true);
    }
    assert.deepEqual(f.decisions, []); assert.equal(f.completeCalls(), 0); assert.deepEqual(f.navigations, []);
    f.flow.dispose(); assert.equal(f.subscribers(), 0); assert.equal(f.timers.size, 0);
  }
});

test('wall jumps cannot accelerate elapsed display and expiry still retires the clock', async () => {
  for (const jump of [-3600000, 300000, 3600000]) {
    const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
    f.wall(jump); f.fire(); assert.equal(f.flow.getSnapshot().slow, undefined);
    if (jump === 3600000) {assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0);}
    else {f.tick(120000); f.fire(); assert.equal(f.flow.getSnapshot().slow, true);}
    f.flow.dispose();
  }
});

test('early callbacks rearm rounded-up remaining time with just one timeout', async () => {
  const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
  f.tick(0.25); f.fire(); assert.equal(f.timers.size, 1);
  assert.equal([...f.timers.values()][0].delay, 120000);
  f.tick(119999.75); f.fire(); assert.equal(f.flow.getSnapshot().slow, true); f.flow.dispose();
});

test('same-family refresh preserves deadline; foreign or absent family retires permanently', async () => {
  for (const mode of ['refresh', 'replace', 'absent'] as const) {
    const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
    const late = [...f.timers.values()][0].callback;
    f.tick(60000);
    if (mode === 'refresh') f.refresh();
    else {if (mode === 'replace') f.replaceFamily(); else f.absentFamily(); f.restoreFamily();}
    f.tick(60000); if (mode === 'refresh') f.fire(); else late();
    assert.equal(f.flow.getSnapshot().slow, mode === 'refresh' ? true : undefined);
    assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0);
    assert.equal(f.completeCalls(), 0); assert.deepEqual(f.navigations, []); f.flow.dispose();
  }
});

test('dispose, expiry and successful returning cancel queued callbacks and account subscription', async () => {
  for (const mode of ['dispose', 'expire', 'return', 'wallExpiry'] as const) {
    const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
    const late = [...f.timers.values()][0].callback;
    if (mode === 'dispose') f.flow.dispose();
    if (mode === 'expire') f.flow.expire();
    if (mode === 'return') await f.flow.complete();
    if (mode === 'wallExpiry') {f.expireClock(); f.fire();}
    const before = f.flow.getSnapshot(); f.tick(120000); late();
    assert.deepEqual(f.flow.getSnapshot(), before);
    assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0); f.flow.dispose();
  }
});

test('display deadline preserves in-flight busy state and epoch, then completion settles', async () => {
  const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
  let finish!: (location: string) => void;
  f.completion(() => new Promise(resolve => {finish = resolve;}));
  const pending = f.flow.complete(), before = f.flow.getSnapshot();
  assert.equal(before.busy, true); f.tick(120000); f.fire();
  assert.deepEqual(f.flow.getSnapshot(), {...before, slow: true});
  finish('https://original.example/return'); await pending;
  assert.equal(f.flow.getSnapshot().phase, 'returning'); assert.equal(f.flow.getSnapshot().busy, false);
  assert.deepEqual(f.navigations, ['https://original.example/return']); f.flow.dispose();
});

test('reentrant disposal on approval or slow publication never rearms', async () => {
  for (const onSlow of [false, true]) {
    const f = longFixture(); await f.flow.start();
    f.flow.subscribe(() => {const state = f.flow.getSnapshot(); if (onSlow ? state.slow : state.approved) f.flow.dispose();});
    await f.flow.decide('approve'); if (onSlow) {f.tick(120000); f.fire();}
    assert.equal(f.subscribers(), 0); assert.equal(f.timers.size, 0);
    assert.deepEqual(f.navigations, []); assert.equal(f.completeCalls(), 0);
  }
});

test('unapproved and denied states never acquire clock resources or passive consent', async () => {
  const f = longFixture(); await f.flow.start();
  f.tick(120000); assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0);
  assert.deepEqual(f.decisions, []); assert.equal(f.completeCalls(), 0);
  await f.flow.decide('deny'); assert.equal(f.flow.getSnapshot().phase, 'denied');
  assert.equal(f.timers.size, 0); assert.equal(f.subscribers(), 0);
  assert.deepEqual(f.navigations, []); f.flow.dispose();
});

test('in-flight completion refusal settles after slow publication without losing retry', async () => {
  const f = longFixture(); await f.flow.start(); await f.flow.decide('approve');
  let refuse!: (error: Error) => void;
  f.completion(() => new Promise((_resolve, reject) => {refuse = reject;}));
  const pending = f.flow.complete(); f.tick(120000); f.fire();
  assert.equal(f.flow.getSnapshot().busy, true);
  const error = new Error('still unavailable'); refuse(error); await pending;
  assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal(f.flow.getSnapshot().error, error);
  assert.equal(f.flow.getSnapshot().slow, true); assert.equal(f.flow.getSnapshot().busy, false);
  assert.deepEqual(f.navigations, []); assert.equal(f.completeCalls(), 1);
  f.completion(async () => 'https://original.example/return'); await f.flow.complete();
  assert.deepEqual(f.navigations, ['https://original.example/return']); f.flow.dispose();
});
