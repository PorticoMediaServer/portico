import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '../../packages/client-core/src/index.ts';
import {CredentialSessionService} from '../../packages/client-core/src/credentials.ts';
import {componentModule} from './helpers/component-harness.mjs';

const {currentAccountWork, currentAccountRead} = await componentModule(new URL('../src/platform/account-work.ts', import.meta.url), {
  '@core/index.ts': {ApiError},
});
const held = () => {let resolve, reject;const promise = new Promise((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};};
const session = (token = 'original', family = 'family', id = 'account') => ({
  accessToken: token, familyId: family, account: {id, username: id, displayName: id},
  expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
});
const refused = () => new ApiError(401, 'authentication_required', 'Sign in again.', false);
async function fixture(refresh = async () => session('renewed')) {
  let record, onLoad;
  const calls = {refresh: [], revoke: []};
  const storage = {load: async () => {await onLoad?.();return structuredClone(record);}, save: async value => {record = structuredClone(value);}};
  const service = new CredentialSessionService({
    authorityFromSession: value => ({familyId: value.familyId}),
    refresh: async (authority, key) => {calls.refresh.push({authority, key});return refresh(authority, key);},
    revoke: async authority => {calls.revoke.push(authority);},
  }, storage);
  await service.adopt(session());
  return {service, calls, holdLoad: callback => {onLoad = callback;}, replaceStored: value => {record.active = {session: value, authority: {familyId: value.familyId}, context: {}};}};
}

test('a revoked original family reaches signed-out through one canonical renewal', async () => {
  const f = await fixture(async () => {throw refused();});let reads = 0;
  await assert.rejects(currentAccountRead(f, async () => {reads++;throw refused();}), {status: 401});
  assert.equal(reads, 1);assert.equal(f.calls.refresh.length, 1);
  assert.equal(f.service.getSnapshot().phase, 'signedOut');
  assert.deepEqual(f.calls.revoke, [{familyId: 'family'}]);
});

test('a safe read retries once with the renewed token; a second 401 does not loop', async () => {
  const f = await fixture();const seen = [];
  assert.equal(await currentAccountRead(f, async value => {seen.push(value.accessToken);if(value.accessToken === 'original')throw refused();return 'read';}), 'read');
  assert.deepEqual(seen, ['original', 'renewed']);assert.equal(f.calls.refresh.length, 1);
  const g = await fixture();let calls = 0;
  await assert.rejects(currentAccountRead(g, async () => {calls++;throw refused();}), {status: 401});
  assert.equal(calls, 2);assert.equal(g.calls.refresh.length, 1);
});

test('a mutation reconciles a definitive 401 but never resubmits the command', async () => {
  const f = await fixture();let writes = 0;const original = refused();
  await assert.rejects(currentAccountWork(f, async () => {writes++;throw original;}), error => error === original);
  assert.equal(writes, 1);assert.equal(f.calls.refresh.length, 1);
  assert.equal(f.service.getSnapshot().session.accessToken, 'renewed');
});

test('network, throttling and permission failures do not renew or end the family', async () => {
  for (const error of [new TypeError('network'), new ApiError(429, 'rate_limited', 'Wait.', true, 5), new ApiError(403, 'not_permitted', 'Not permitted.', false)]) {
    const f = await fixture();
    await assert.rejects(currentAccountRead(f, async () => {throw error;}), actual => actual === error);
    assert.equal(f.calls.refresh.length, 0);assert.equal(f.service.getSnapshot().session.familyId, 'family');
  }
});

test('a late old 401 cannot renew or retire an adopted successor', async () => {
  const f = await fixture(), entered = held(), result = held();
  const old = currentAccountRead(f, async () => {entered.resolve();return result.promise;});
  await entered.promise;await f.service.adopt(session('successor', 'other-family', 'other-account'));
  // The deliberate account switch retires the superseded original family. That
  // is the switch's own lifecycle, not the in-flight read's.
  assert.deepEqual(f.calls.revoke, [{familyId: 'family'}]);
  result.reject(refused());await assert.rejects(old, {code: 'account_changed'});
  assert.equal(f.calls.refresh.length, 0);
  // The late 401 adds no renewal and no further retirement of the successor.
  assert.deepEqual(f.calls.revoke, [{familyId: 'family'}]);
  assert.equal(f.service.getSnapshot().session.familyId, 'other-family');
});

test('a stale original token reuses an already-renewed same family without rotating again', async () => {
  const f = await fixture(), entered = held(), result = held();const seen = [];
  const old = currentAccountRead(f, async value => {
    seen.push(value.accessToken);if(seen.length === 1){entered.resolve();return result.promise;}return 'read';
  });
  await entered.promise;await f.service.accessSession(true);result.reject(refused());
  assert.equal(await old, 'read');assert.deepEqual(seen, ['original', 'renewed']);assert.equal(f.calls.refresh.length, 1);
});

test('concurrent original read failures share canonical renewal', async () => {
  const entered = held(), release = held();const f = await fixture(async () => {entered.resolve();await release.promise;return session('renewed');});
  const read = () => currentAccountRead(f, async value => {if(value.accessToken === 'original')throw refused();return value.accessToken;});
  const a = read(), b = read();await entered.promise;release.resolve();
  assert.deepEqual(await Promise.all([a, b]), ['renewed', 'renewed']);assert.equal(f.calls.refresh.length, 1);
});

test('the exact expectation is captured before a queued storage load and refuses another tab family', async () => {
  const f = await fixture(), entered = held(), release = held();let once = true;
  f.holdLoad(async () => {if(once){once = false;entered.resolve();await release.promise;}});
  const expected = {accountId: 'account', familyId: 'family', accessToken: 'original'};
  const old = f.service.accessSession(true, expected);
  await entered.promise;f.replaceStored(session('successor', 'other-family', 'other-account'));
  Object.assign(expected, {accountId: 'other-account', familyId: 'other-family', accessToken: 'successor'});
  release.resolve();await assert.rejects(old, {code: 'account_session_changed'});
  assert.equal(f.calls.refresh.length, 0);assert.deepEqual(f.calls.revoke, []);
});

test('retiring the original read prevents its post-renewal retry', async () => {
  const entered = held(), release = held();const f = await fixture(async () => {entered.resolve();await release.promise;return session('renewed');});
  const abort = new AbortController();let reads = 0;
  const old = currentAccountRead(f, async () => {reads++;throw refused();}, undefined, abort.signal);
  await entered.promise;abort.abort();release.resolve();await assert.rejects(old, {name: 'AbortError'});
  assert.equal(reads, 1);
});
