import assert from 'node:assert/strict';
import {test} from 'node:test';
import {OwnerApprovalFlow} from '../src/services/owner-approval-flow.ts';
import type {HostedOwnerClaimReview} from '../src/services/owner-claim.ts';
import type {scopedHostedOwnerClaim} from '../src/services/owner-pending.ts';
import type {ServicesAccount} from '../src/services/session.ts';

function fixture(options: {otherPeopleCount?: number} = {}) {
  let family = 'family', clock = Date.now(), installed = false, status: 'pending' | 'approved' = 'pending';
  const calls: string[] = [], navigations: string[] = [];
  let hold: (step: string) => Promise<void> = async () => {};
  const session = () => ({account: {id: 'account', username: 'actual_user', displayName: 'Actual account'}, familyId: family, accessToken: 'private-token'});
  const account = {service: {getSnapshot: () => ({session: session()}), synchronize: async () => {await hold('synchronize');},
    accessSession: async () => session()}} as unknown as ServicesAccount;
  const review: HostedOwnerClaimReview = Object.freeze({request: {hostedOrigin: 'https://accounts.test', code: 'private-code', operation: 'private-operation', nameHint: 'Server'},
    identity: {accountId: 'account', familyId: 'family'}, serverId: 'actual-server-id', name: 'Actual server', address: 'https://original.test',
    transactionId: 'private-transaction', requestedAt: new Date(clock - 1000).toISOString(), expiresAt: new Date(clock + 60000).toISOString(), status: 'pending', reason: 'switch',
    ...(options.otherPeopleCount === undefined ? {} : {otherPeopleCount: options.otherPeopleCount})});
  const controller: ReturnType<typeof scopedHostedOwnerClaim> = {
    review: async () => {calls.push('review'); await hold('review'); return {...review, status};},
    decide: async decision => {calls.push(decision); await hold(decision); if (decision === 'approve') status = 'approved';},
    complete: async () => {calls.push('complete'); await hold('complete'); return installed ? 'https://original.test/auth/portico-return#transaction=private-transaction' : undefined;},
    cancel: async () => {calls.push('cancel');}, dispose: () => {calls.push('dispose');},
  };
  const flow = new OwnerApprovalFlow(account, controller, location => navigations.push(location), () => clock);
  return {flow, calls, navigations, review, replace: () => {family = 'successor';}, installed: () => {installed = true;},
    advance: () => {clock += 60001;}, intercept: (work: typeof hold) => {hold = work;}};
}

test('the drawn other-people count comes only from the reviewed preview', async () => {
  const absent = fixture(); await absent.flow.start();
  assert.equal('otherPeopleCount' in absent.flow.getSnapshot().display!, false); absent.flow.dispose();
  for (const count of [0, 1, 86, 10000]) {
    const f = fixture({otherPeopleCount: count}); await f.flow.start();
    const display = f.flow.getSnapshot().display!;
    assert.equal(display.otherPeopleCount, count); assert.equal(Object.isFrozen(display), true);
    assert.deepEqual(f.calls, ['review']); assert.deepEqual(f.navigations, []); f.flow.dispose();
  }
});
test('owner review is inert until started and publishes display facts without the private attempt', async () => {
  const f = fixture(); assert.deepEqual(f.calls, []);
  await f.flow.start();
  assert.deepEqual(f.calls, ['review']); assert.deepEqual(f.navigations, []);
  const snapshot = f.flow.getSnapshot(); assert.equal(snapshot.phase, 'review');
  assert.equal(snapshot.display?.accountName, 'Actual account'); assert.equal(Object.isFrozen(snapshot.display), true);
  const publicState = JSON.stringify(snapshot);
  for (const secret of ['private-code', 'private-operation', 'private-transaction', 'private-token', 'familyId']) assert.equal(publicState.includes(secret), false);
  f.flow.dispose();
});

test('approval is a gesture and unready Return says it is finishing, without polling or a second approval', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.complete(); assert.deepEqual(f.calls, ['review']);
  await f.flow.decide('approve'); assert.equal(f.flow.getSnapshot().phase, 'approved');
  assert.deepEqual(f.calls, ['review', 'approve']); assert.deepEqual(f.navigations, []);
  await f.flow.decide('approve'); await f.flow.complete();
  assert.deepEqual(f.calls, ['review', 'approve', 'complete']); assert.equal(f.flow.getSnapshot().phase, 'finishing');
  f.installed(); await f.flow.complete();
  assert.deepEqual(f.navigations, ['https://original.test/auth/portico-return#transaction=private-transaction']);
  await f.flow.complete(); assert.deepEqual(f.calls, ['review', 'approve', 'complete', 'complete']);
  f.flow.dispose();
});

test('denial returns only the authenticated original origin after canonical decision completes', async () => {
  const f = fixture(); await f.flow.start();
  let release!: () => void;
  f.intercept(step => step === 'deny' ? new Promise<void>(resolve => {release = resolve;}) : Promise.resolve());
  const denied = f.flow.decide('deny'); assert.deepEqual(f.navigations, []);
  await f.flow.decide('approve'); assert.deepEqual(f.calls, ['review', 'deny']);
  release(); await denied;
  assert.deepEqual(f.navigations, ['https://original.test']); assert.equal(f.flow.getSnapshot().phase, 'denied');
  f.flow.dispose();
});

test('family replacement after deny or complete prevents navigation and successor display', async () => {
  for (const step of ['deny', 'complete']) {
    const f = fixture(); await f.flow.start();
    if (step === 'complete') {await f.flow.decide('approve'); f.installed();}
    f.intercept(async current => {if (current === step) f.replace();});
    if (step === 'deny') await f.flow.decide('deny'); else await f.flow.complete();
    assert.deepEqual(f.navigations, []); assert.equal(f.flow.getSnapshot().phase, 'error');
    assert.equal(f.flow.getSnapshot().display, undefined); f.flow.dispose();
  }
});

test('family replacement during final synchronization prevents a denied return', async () => {
  const f = fixture(); await f.flow.start();
  f.intercept(async step => {if (step === 'synchronize') f.replace();});
  await f.flow.decide('deny'); assert.deepEqual(f.navigations, []); assert.equal(f.flow.getSnapshot().phase, 'error');
  f.flow.dispose();
});

test('synchronous view retirement before navigation cannot return a stale family', async () => {
  const f = fixture(); await f.flow.start();
  f.flow.subscribe(() => {if (f.flow.getSnapshot().phase === 'denied') f.replace();});
  await f.flow.decide('deny'); assert.deepEqual(f.navigations, []);
  assert.equal(f.flow.getSnapshot().phase, 'error'); f.flow.dispose();
});

test('dispose suppresses late review publication and preserves the protected journal', async () => {
  const f = fixture(); let release!: () => void;
  f.intercept(step => step === 'review' ? new Promise<void>(resolve => {release = resolve;}) : Promise.resolve());
  const pending = f.flow.start(), last = f.flow.getSnapshot(); f.flow.dispose(); release(); await pending;
  assert.equal(f.flow.getSnapshot(), last); assert.deepEqual(f.navigations, []);
  assert.deepEqual(f.calls, ['review', 'dispose']);
});

test('signed expiry retires decisions and explicit expiry retires pending view effects', async () => {
  const f = fixture(); await f.flow.start(); f.advance(); await f.flow.decide('approve');
  assert.deepEqual(f.calls, ['review']); assert.equal(f.flow.getSnapshot().phase, 'error');
  f.flow.expire(); await f.flow.start(); await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'expired'); assert.deepEqual(f.calls, ['review', 'dispose']);
  f.flow.dispose();
});

test('owner Return before the secure return is ready says so and retries, never a silent no-op', async () => {
  const f = fixture(); await f.flow.start(); await f.flow.decide('approve');
  await f.flow.complete();
  assert.equal(f.flow.getSnapshot().phase, 'finishing'); assert.equal(f.flow.getSnapshot().busy, false); assert.deepEqual(f.navigations, []);
  f.installed(); await f.flow.complete();
  assert.deepEqual(f.navigations, ['https://original.test/auth/portico-return#transaction=private-transaction']);
  f.flow.dispose();
});
