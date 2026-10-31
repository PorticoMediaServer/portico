import assert from 'node:assert/strict';
import {test} from 'node:test';
import {SetupApprovalFlow} from '../src/services/setup-approval-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {HostedSetupClaimReview} from '../src/services/setup-claim.ts';
import {ApiError} from '@core/index.ts';
import {createManualScheduler} from '@core/timer.ts';

function fixture() {
  let session = {account: {id: 'original-account', username: 'owner', displayName: 'Owner'}, familyId: 'original-family', accessToken: 'fixture-access'};
  const accountListeners = new Set<() => void>();
  const scheduler = createManualScheduler();
  let lookups = 0, now = Date.now(), rejectDecision = false, completeCalls = 0, disposed = false, accountReadsClosed = false;
  const navigations: string[] = [], decisions: string[] = [];
  let completion = async () => 'https://original.example/auth/portico-return#transaction=original-transaction';
  let reviewing = async () => {};
  const account = {service: {getSnapshot: () => ({session}), subscribe: (listener: () => void) => {accountListeners.add(listener); return () => accountListeners.delete(listener);}, synchronize: async () => {if (accountReadsClosed) throw new Error('account read unavailable');}, accessSession: async () => session}} as unknown as ServicesAccount;
  const review = {identity: {accountId: session.account.id, familyId: session.familyId}, name: 'Original server', address: 'https://original.example',
    requestedAt: new Date(now - 1000).toISOString(), expiresAt: new Date(now + 120000).toISOString(), status: 'pending'} as HostedSetupClaimReview;
  // Each controller call that reaches Hosted is one lookup: review, and complete (which hands its lookup back).
  const controller = {review: async () => {lookups++; await reviewing(); return review;}, decide: async (decision: string) => {decisions.push(decision); if (rejectDecision) throw new Error('decision unavailable');},
    complete: async () => {completeCalls++; lookups++; const location = await completion(); return location ? {location} : {review};}, dispose: () => {disposed = true;}} as unknown as ConstructorParameters<typeof SetupApprovalFlow>[1];
  const flow = new SetupApprovalFlow(account, controller, location => navigations.push(location), () => now, scheduler);
  return {flow, review, navigations, decisions, scheduler, completion: (work: typeof completion) => {completion = work;}, reviewing: (work: typeof reviewing) => {reviewing = work;},
    rejectDecision: () => {rejectDecision = true;}, replaceFamily: () => {session = {...session, familyId: 'successor-family'}; accountListeners.forEach(listener => listener());},
    closeAccountReads: () => {accountReadsClosed = true;},
    expireClock: () => {now += 180000;}, lookups: () => lookups, completeCalls: () => completeCalls, disposed: () => disposed};
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

const RETURN = 'https://original.example/auth/portico-return#transaction=original-transaction';
const phaseOf = (f: ReturnType<typeof fixture>, status: 'pending' | 'approved', phase?: object, installed = false) => Object.assign(f.review, {status, installed, phase});

test('each press of Try again is exactly one lookup, and Return goes ahead once installed', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  assert.equal(f.lookups(), 1);
  f.completion(async () => undefined as never);
  await f.flow.complete(); assert.equal(f.lookups(), 2); assert.equal(f.flow.getSnapshot().phase, 'finishing');
  await f.flow.complete(); assert.equal(f.lookups(), 3);
  f.completion(async () => RETURN); await f.flow.complete();
  assert.equal(f.lookups(), 4); assert.deepEqual(f.navigations, [RETURN]); f.flow.dispose();
});

test('each phase variant reads as its own state, slow only while the server has not been heard from', async () => {
  const at = (ms: number) => new Date(Date.now() + ms).toISOString();
  const cases: [string, 'pending' | 'approved', object | undefined, boolean, Record<string, unknown>][] = [
    ['review', 'pending', {variant: 'review'}, false, {phase: 'review'}],
    ['approved', 'approved', {variant: 'approved', approvedAt: at(-1000)}, false, {phase: 'approved', approved: true}],
    ['finishing', 'approved', {variant: 'finishing', approvedAt: at(-1000), serverSeenAt: at(-500)}, false, {phase: 'approved', approved: true}],
    ['installed', 'approved', {variant: 'installed'}, true, {phase: 'approved', approved: true, installed: true}],
    ['slow, never seen', 'approved', {variant: 'approved', slow: true, approvedAt: at(-200000)}, false, {phase: 'approved', approved: true, slow: true}],
    ['slow, seen since approval', 'approved', {variant: 'finishing', slow: true, approvedAt: at(-200000), serverSeenAt: at(-1000)}, false, {phase: 'approved', approved: true}],
    ['slow, seen before approval', 'approved', {variant: 'finishing', slow: true, approvedAt: at(-1000), serverSeenAt: at(-200000)}, false, {phase: 'approved', approved: true, slow: true}],
    ['slow, seen but approval unknown', 'approved', {variant: 'finishing', slow: true, serverSeenAt: at(-1000)}, false, {phase: 'approved', approved: true, slow: true}],
  ];
  for (const [name, status, phase, installed, expected] of cases) {
    const f = fixture(); phaseOf(f, status, phase, installed); await f.flow.start();
    const {phase: shown, approved, installed: ready, slow} = f.flow.getSnapshot();
    assert.deepEqual({phase: shown, approved, installed: ready, slow}, {phase: undefined, approved: undefined, installed: undefined, slow: undefined, ...expected}, name);
    f.flow.dispose();
  }
});

test('Try again carries the slow reading from the same lookup, and clears it once the server is heard from', async () => {
  const f = fixture(); phaseOf(f, 'approved', {variant: 'approved', slow: true, approvedAt: new Date(Date.now() - 200000).toISOString()});
  await f.flow.start(); f.completion(async () => undefined as never);
  await f.flow.complete(); assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().slow, true);
  phaseOf(f, 'approved', {variant: 'finishing', slow: true, approvedAt: new Date(Date.now() - 200000).toISOString(), serverSeenAt: new Date().toISOString()});
  await f.flow.complete(); assert.equal(f.flow.getSnapshot().slow, undefined); assert.equal(f.lookups(), 3);
  f.flow.dispose();
});

test('an older Hosted that answers without a phase works as before', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => undefined as never); await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().slow, undefined);
  Object.assign(f.review, {status: 'approved', installed: true}); await f.flow.complete();
  assert.equal(f.flow.getSnapshot().installed, true); assert.equal(f.lookups(), 3);
  f.completion(async () => RETURN); await f.flow.complete(); assert.deepEqual(f.navigations, [RETURN]); f.flow.dispose();
});

test('a phase the page does not know shows the stale error and never throws', async () => {
  const f = fixture(); phaseOf(f, 'approved', {variant: 'quarantined', slow: true});
  await f.flow.start();
  assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal((f.flow.getSnapshot().error as {code?: string}).code, 'claim_stale');
  assert.equal(f.flow.getSnapshot().display, undefined); f.flow.dispose();
  const g = fixture(); await g.flow.start(); await g.flow.decide('approve');
  g.completion(async () => undefined as never); phaseOf(g, 'approved', {variant: 'quarantined'});
  await g.flow.complete();
  assert.equal(g.flow.getSnapshot().phase, 'error'); assert.equal((g.flow.getSnapshot().error as {code?: string}).code, 'claim_stale');
  assert.equal(g.flow.getSnapshot().approved, true); assert.equal(g.flow.getSnapshot().busy, false); assert.deepEqual(g.navigations, []);
  // Try again still works once Hosted answers with a phase it knows.
  g.completion(async () => RETURN); phaseOf(g, 'approved', {variant: 'installed'}, true); await g.flow.complete();
  assert.deepEqual(g.navigations, [RETURN]); g.flow.dispose();
});

test('a 429 holds Try again until its Retry-After has passed, never earlier and never by itself retrying', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  f.completion(async () => {throw new ApiError(429, 'rate_limited', 'rate_limited', true, 30);});
  await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal(f.flow.getSnapshot().retryIn, 30); assert.equal(f.lookups(), 2);
  f.completion(async () => RETURN);
  await f.flow.complete(); assert.equal(f.lookups(), 2, 'a press during the wait is not a lookup');
  await f.scheduler.advance(1); assert.equal(f.flow.getSnapshot().retryIn, 30);
  await f.scheduler.advance(28_998); assert.equal(f.flow.getSnapshot().retryIn, 2);
  await f.flow.complete(); assert.equal(f.lookups(), 2); assert.deepEqual(f.navigations, []);
  await f.scheduler.advance(1000); assert.equal(f.flow.getSnapshot().retryIn, 1, 'one millisecond short');
  await f.flow.complete(); assert.equal(f.lookups(), 2);
  await f.scheduler.advance(1); assert.equal(f.flow.getSnapshot().retryIn, undefined);
  assert.equal(f.lookups(), 2, 'nothing retries on its own'); assert.equal(f.scheduler.pending(), 1);
  await f.flow.complete(); assert.equal(f.lookups(), 3); assert.deepEqual(f.navigations, [RETURN]); f.flow.dispose();
});

test('a 429 on the first lookup holds the next one the same way; one without Retry-After holds nothing', async () => {
  const f = fixture(); f.reviewing(async () => {throw new ApiError(429, 'rate_limited', 'rate_limited', true, 5);});
  await f.flow.start(); assert.equal(f.flow.getSnapshot().retryIn, 5);
  await f.flow.start(); assert.equal(f.lookups(), 1);
  await f.scheduler.advance(4999); await f.flow.start(); assert.equal(f.lookups(), 1);
  f.reviewing(async () => {}); await f.scheduler.advance(1); await f.flow.start();
  assert.equal(f.lookups(), 2); assert.equal(f.flow.getSnapshot().retryIn, undefined); assert.equal(f.flow.getSnapshot().phase, 'review'); f.flow.dispose();
  const g = fixture(); g.reviewing(async () => {throw new ApiError(429, 'rate_limited', 'rate_limited', true);});
  await g.flow.start(); assert.equal(g.flow.getSnapshot().retryIn, undefined); assert.equal(g.flow.getSnapshot().phase, 'error'); g.flow.dispose();
});

test('the page ends at the claim expiry on the scheduler, and dispose, expiry and return cancel every wait', async () => {
  const f = fixture(); await f.flow.start(); assert.equal(f.scheduler.pending(), 1);
  await f.scheduler.advance(119_999); assert.equal(f.flow.getSnapshot().phase, 'review');
  await f.scheduler.advance(1); assert.equal(f.flow.getSnapshot().phase, 'expired'); assert.equal(f.disposed(), true); assert.equal(f.scheduler.pending(), 0);
  for (const mode of ['dispose', 'expire', 'return', 'held'] as const) {
    const g = fixture(); await g.flow.start(); await g.flow.decide('approve');
    if (mode === 'held') {g.completion(async () => {throw new ApiError(429, 'rate_limited', 'rate_limited', true, 30);}); await g.flow.complete(); assert.equal(g.scheduler.pending(), 2);}
    if (mode === 'dispose') g.flow.dispose();
    if (mode === 'expire') g.flow.expire();
    if (mode === 'return') {await g.flow.complete(); assert.equal(g.flow.getSnapshot().phase, 'returning');}
    if (mode === 'held') g.flow.dispose();
    const before = g.flow.getSnapshot(); await g.scheduler.advance(200_000);
    assert.deepEqual(g.flow.getSnapshot(), before, mode); g.flow.dispose();
    if (mode !== 'return') assert.equal(g.scheduler.pending(), 0, mode);
  }
});

test('in-flight completion refusal settles with its error and keeps the retry', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  let refuse!: (error: Error) => void;
  f.completion(() => new Promise((_resolve, reject) => {refuse = reject;}));
  const pending = f.flow.complete(); assert.equal(f.flow.getSnapshot().busy, true);
  const error = new Error('still unavailable'); refuse(error); await pending;
  assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal(f.flow.getSnapshot().error, error); assert.equal(f.flow.getSnapshot().busy, false);
  assert.deepEqual(f.navigations, []); assert.equal(f.completeCalls(), 1);
  f.completion(async () => RETURN); await f.flow.complete();
  assert.deepEqual(f.navigations, [RETURN]); f.flow.dispose();
});
