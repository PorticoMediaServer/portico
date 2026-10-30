import assert from 'node:assert/strict';
import test from 'node:test';
import {SetupCodeService, type SetupCodeJournal} from '../../../packages/client-core/src/setup-code';
import {CredentialSessionService, type CredentialRecord} from '../../../packages/client-core/src/credentials';
import {ApiError, type HostedSession} from '../../../packages/client-core/src';
import {createTVApprovalFinalization} from '../src-next/screens/accounts/tv-approval-finalization';

const issued = (): HostedSession => ({account: {id: 'account', username: 'Viewer', displayName: 'Viewer'}, familyId: 'family', accessToken: 'test-access', refreshToken: 'test-refresh', expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString()});
async function fixture() {
  const grant = issued(), calls: string[] = [];
  let current = true, failJournal = false, failVerification: unknown, finishVerification: (() => Promise<void>) | undefined;
  let journal: SetupCodeJournal<HostedSession> = {version: 1, authority: 'test-tv', revision: 1, retired: [], current: {
    requestId: '11111111-1111-4111-8111-111111111111', authorization: {deviceCode: 'd'.repeat(43), userCode: 'ABCD-EFGH', verificationUri: 'https://account.test/device', verificationUriComplete: 'https://account.test/device#code=ABCD-EFGH', interval: 5, expiresIn: 300, expiresAt: new Date(Date.now() + 300000).toISOString()}, grant, nextPollAt: 0}};
  const service = new SetupCodeService<HostedSession>('test-tv', {
    create: async () => {calls.push('create'); throw new Error('No new consent');}, poll: async () => {calls.push('poll'); throw new Error('No repoll');}, cancel: async () => {calls.push('cancel');},
  }, {change: async update => {const next = update(journal); if (failJournal && !next.current) {failJournal = false; throw new Error('Protected journal unavailable');} journal = next; return journal;}}, async () => '22222222-2222-4222-8222-222222222222');
  await service.start(); const original = service.getSnapshot().grant!;
  let record: CredentialRecord<string> | undefined;
  const credentials = new CredentialSessionService({authorityFromSession: value => value.familyId, refresh: async () => grant, revoke: async family => {calls.push(`revoke:${family}`);}},
    {load: async () => record, save: async value => {record = structuredClone(value);}}, async () => 'test-key');
  await credentials.synchronize();
  const opening = createTVApprovalFinalization({service, grant: original, credentials, current: () => current,
    adopt: (value, live) => {calls.push('admit'); return credentials.authenticateIfSignedOut(async () => {calls.push('verify'); if (finishVerification) await finishVerification(); if (failVerification) throw failVerification; return value;}, {}, live);},
  });
  return {opening, service, credentials, calls, original,
    failVerify(error: unknown) {failVerification = error;}, failComplete() {failJournal = true;},
    waitVerify(work: () => Promise<void>) {finishVerification = work;},
    retire() {current = false; opening.retire(); service.pause();},
    replace() {record = {version: 1, active: {session: {...grant, familyId: 'successor'}, authority: 'successor', context: {}}, revocations: []};},
    falseComplete() {service.complete = async () => false;},
    journal: () => journal,
    close() {opening.retire(); service.pause();},
  };
}

test('an explicit retry verifies and adopts the SAME approved grant after a temporary failure without new consent', async () => {
  const f = await fixture(); try {
    f.failVerify(new ApiError(503, 'unavailable', 'Test service unavailable', true));
    assert.equal(await f.opening.run(), 'failed'); assert.equal(f.service.getSnapshot().grant, f.original);
    f.failVerify(undefined); assert.equal(await f.opening.run(), 'complete');
    assert.equal(f.credentials.getSnapshot().session?.familyId, f.original.familyId);
    assert.equal(f.journal().current, undefined); assert.deepEqual(f.calls, ['admit', 'verify', 'admit', 'verify']);
  } finally {f.close();}
});
test('journal failure after canonical adoption retries ONLY completion and cannot cancel the adopted family', async () => {
  const f = await fixture(); try {
    f.failComplete(); assert.equal(await f.opening.run(), 'failed'); assert.equal(f.opening.getSnapshot().adopted, true);
    assert.equal(f.opening.canCancel(), false); assert.equal(f.credentials.getSnapshot().session?.familyId, 'family');
    assert.equal(await f.opening.run(), 'complete'); assert.deepEqual(f.calls, ['admit', 'verify']);
  } finally {f.close();}
});
test('duplicate retry is synchronous one-flight and original retirement refuses late canonical adoption', async () => {
  const f = await fixture(); let release!: () => void;
  try {
    f.waitVerify(() => new Promise<void>(resolve => {release = resolve;}));
    const first = f.opening.run(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(await f.opening.run(), 'busy'); assert.equal(f.opening.canCancel(), false);
    f.retire(); release(); assert.equal(await first, 'retired');
    assert.equal(f.credentials.getSnapshot().session, undefined); assert.equal(f.journal().current?.grant, f.original);
    await new Promise(resolve => setImmediate(resolve));
    assert.ok(f.calls.includes('revoke:family')); assert.equal(f.calls.filter(value => value === 'admit').length, 1);
  } finally {f.close();}
});
test('a replaced canonical family cannot be adopted, journal-completed or cancelled by the original retry', async () => {
  const f = await fixture(); try {
    f.failComplete(); await f.opening.run(); f.replace();
    assert.equal(await f.opening.run(), 'retired'); assert.equal(f.opening.canCancel(), false);
    assert.equal(f.credentials.getSnapshot().session?.familyId, 'successor'); assert.ok(f.journal().current);
    assert.deepEqual(f.calls, ['admit', 'verify']);
  } finally {f.close();}
});
test('complete=false retires the opening without a successful completion result', async () => {
  const f = await fixture(); try {f.falseComplete(); assert.equal(await f.opening.run(), 'retired'); assert.equal(f.opening.canCancel(), false);} finally {f.close();}
});
test('definitive invalid grant requires replacement and cannot be explicitly readmitted', async () => {
  const f = await fixture(); try {
    f.failVerify(new ApiError(401, 'invalid_grant', 'Expired grant', false)); assert.equal(await f.opening.run(), 'terminal');
    f.failVerify(undefined); assert.equal(await f.opening.run(), 'terminal'); assert.deepEqual(f.calls, ['admit', 'verify']);
  } finally {f.close();}
});
test('429 requires the offered deadline before an explicit retry can enter credential admission', async t => {
  t.mock.timers.enable({apis: ['Date']});
  const f = await fixture(); try {
    f.failVerify(new ApiError(429, 'rate_limited', 'Wait', true, 10)); assert.equal(await f.opening.run(), 'failed');
    f.failVerify(undefined); await f.opening.run(); assert.deepEqual(f.calls, ['admit', 'verify']);
    t.mock.timers.tick(10000); assert.equal(await f.opening.run(), 'complete');
  } finally {f.close();}
});

test('retirement before a synchronized read settles refuses guarded service/credential getters', async () => {
  const f = await fixture(); let release!: () => void, reads = 0;
  try {
    f.credentials.synchronize = () => new Promise<void>(resolve => {release = resolve;});
    f.credentials.getSnapshot = () => {reads++; throw new Error('retired credential getter');};
    const pending = f.opening.run(); await new Promise(resolve => setImmediate(resolve));
    f.retire(); release(); assert.equal(await pending, 'retired');
    f.service.isCurrent = () => {reads++; throw new Error('retired service getter');};
    assert.equal(await f.opening.run(), 'retired'); assert.equal(reads, 0);
  } finally {f.close();}
});

test('uncertain canonical readback refuses cancellation and retries only the original journal when storage recovers', async () => {
  const f = await fixture(), synchronize = f.credentials.synchronize.bind(f.credentials); let reads = 0;
  try {
    f.credentials.synchronize = async () => {if (++reads > 1 && reads < 4) throw new Error('Protected readback unavailable'); await synchronize();};
    assert.equal(await f.opening.run(), 'failed'); assert.equal(f.opening.getSnapshot().adopted, false);
    assert.equal(f.opening.canCancel(), false); assert.deepEqual(f.calls, ['admit', 'verify']);
    assert.equal(await f.opening.run(), 'complete'); assert.deepEqual(f.calls, ['admit', 'verify']);
  } finally {f.close();}
});
