import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '../../packages/client-core/src/index.ts';
import {CredentialSessionService} from '../../packages/client-core/src/credentials.ts';
import {routePath, routeBody} from '../../packages/contracts/src/hosted/routes.ts';
import {componentModule} from './helpers/component-harness.mjs';

const fragments = await componentModule(new URL('../src/services/fragments.ts', import.meta.url), {});
const {verifyEmail} = await componentModule(new URL('../src/services/link-effects.ts', import.meta.url), {
  '@core/index.ts': {ApiError}, './fragments': fragments,
  './session': {accountIdentity: session => ({accountId: session.account.id, familyId: session.familyId})},
  '../../../packages/contracts/src/hosted/routes.ts': {routePath, routeBody},
});
const token = Buffer.from(JSON.stringify(['1', 'email_change', 'challenge', '482917'])).toString('base64url');
const held = () => {let resolve;const promise = new Promise(done => {resolve = done;});return {promise, resolve};};
const session = (account = 'affected', familyId = 'original') => ({
  account: {id: account, username: account, displayName: account}, familyId, accessToken: 'access-' + familyId,
  expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
});
async function fixture(original, response = async () => ({kind: 'email_change', status: 'email_changed', accountId: 'affected'})) {
  let record;const revoked = [], requests = [];
  const service = new CredentialSessionService({
    authorityFromSession: value => ({familyId: value.familyId}),
    refresh: async () => assert.fail('email link must not renew credentials'),
    revoke: async authority => {revoked.push(authority.familyId);},
  }, {load: async () => structuredClone(record), save: async value => {record = structuredClone(value);}});
  await service.synchronize();if(original)await service.adopt(original);
  const account = {service, client: {request: async (...args) => {requests.push(args);return response();}}};
  return {account, service, revoked, requests};
}

test('a mailed link uses only the public capability for any signed-in or signed-out browser', async () => {
  for (const original of [session(), session('affected', 'second-browser'), session('unrelated', 'foreign-family'), undefined]) {
    const f = await fixture(original);
    assert.equal(await verifyEmail(f.account, token, new AbortController().signal), 'changed');
    assert.equal(f.requests.length, 1);const [path, method, body, , bearer] = f.requests[0];
    assert.equal(path, routePath('email_link_confirm'));assert.equal(method, 'POST');assert.deepEqual(body, {token});assert.equal(bearer, undefined);
    if(original?.account.id === 'affected') {
      assert.equal(f.service.getSnapshot().session, undefined);assert.deepEqual(f.revoked, [original.familyId]);
    } else {
      assert.equal(f.service.getSnapshot().session?.familyId, original?.familyId);assert.deepEqual(f.revoked, []);
    }
  }
});

test('an exact committed replay preserves a new valid family of the affected account', async () => {
  const f = await fixture(session('affected', 'fresh-after-change'), async () => ({kind: 'email_change', status: 'already_confirmed', accountId: 'affected'}));
  assert.equal(await verifyEmail(f.account, token, new AbortController().signal), 'changed');
  assert.equal(f.service.getSnapshot().session.familyId, 'fresh-after-change');assert.deepEqual(f.revoked, []);
});

test('a held original result cannot sign out a newly adopted successor', async () => {
  const entered = held(), answer = held();const f = await fixture(session(), () => {entered.resolve();return answer.promise;});
  const pending = verifyEmail(f.account, token, new AbortController().signal);await entered.promise;
  await f.service.adopt(session('affected', 'successor-family'));
  // The switch itself retires the superseded original family …
  assert.deepEqual(f.revoked, ['original']);
  answer.resolve({kind: 'email_change', status: 'email_changed', accountId: 'affected'});
  assert.equal(await pending, 'changed');assert.equal(f.service.getSnapshot().session.familyId, 'successor-family');
  // … and the held original result adds no retirement, so the successor keeps its place.
  assert.deepEqual(f.revoked, ['original']);
});

test('wrong, expired or malformed proof results never retire a local family', async () => {
  for (const result of [
    {kind: 'registration', status: 'confirmed', accountId: 'affected'},
    {kind: 'email_change', status: 'email_changed'},
    {kind: 'email_change', status: 'confirmed', accountId: 'affected'},
    {kind: 'email_change', status: 'email_changed', accountId: 'invalid/account'},
    new ApiError(400, 'challenge_invalid', 'Invalid proof.', false),
  ]) {
    const f = await fixture(session(), async () => {if(result instanceof Error)throw result;return result;});
    await assert.rejects(verifyEmail(f.account, token, new AbortController().signal));
    assert.equal(f.service.getSnapshot().session.familyId, 'original');assert.deepEqual(f.revoked, []);
  }
});

test('a known committed change retires its original family after view cancellation without publishing success', async () => {
  const entered = held(), answer = held(), abort = new AbortController();
  const f = await fixture(session(), () => {entered.resolve();return answer.promise;});
  const pending = verifyEmail(f.account, token, abort.signal);await entered.promise;abort.abort();
  answer.resolve({kind: 'email_change', status: 'email_changed', accountId: 'affected'});
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(f.service.getSnapshot().session, undefined);assert.deepEqual(f.revoked, ['original']);
});
