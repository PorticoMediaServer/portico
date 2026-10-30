import assert from 'node:assert/strict';
import {test} from 'node:test';
import {SetupApprovalFlow} from '../src/services/setup-approval-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {HostedSetupClaimReview} from '../src/services/setup-claim.ts';

function fixture() {
  let session = {account: {id: 'original-account', username: 'owner', displayName: 'Owner'}, familyId: 'original-family', accessToken: 'fixture-access'};
  let now = Date.now(), rejectDecision = false, completeCalls = 0, disposed = false, accountReadsClosed = false;
  const navigations: string[] = [], decisions: string[] = [];
  let completion = async () => 'https://original.example/auth/portico-return#transaction=original-transaction';
  const account = {service: {getSnapshot: () => ({session}), synchronize: async () => {if (accountReadsClosed) throw new Error('account read unavailable');}, accessSession: async () => session}} as unknown as ServicesAccount;
  const review = {identity: {accountId: session.account.id, familyId: session.familyId}, name: 'Original server', address: 'https://original.example',
    requestedAt: new Date(now - 1000).toISOString(), expiresAt: new Date(now + 120000).toISOString(), status: 'pending'} as HostedSetupClaimReview;
  const controller = {review: async () => review, decide: async (decision: string) => {decisions.push(decision); if (rejectDecision) throw new Error('decision unavailable');},
    complete: async () => {completeCalls++; return completion();}, dispose: () => {disposed = true;}} as unknown as ConstructorParameters<typeof SetupApprovalFlow>[1];
  const flow = new SetupApprovalFlow(account, controller, location => navigations.push(location), () => now);
  return {flow, review, navigations, decisions, completion: (work: typeof completion) => {completion = work;},
    rejectDecision: () => {rejectDecision = true;}, replaceFamily: () => {session = {...session, familyId: 'successor-family'};},
    closeAccountReads: () => {accountReadsClosed = true;},
    expireClock: () => {now += 180000;}, completeCalls: () => completeCalls, disposed: () => disposed};
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
