import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ApiError} from '@core/index.ts';
import {captureHostedSetupClaim, reviewHostedSetupClaim, decideHostedSetupClaim, completeHostedSetupClaim, validateHostedSetupReturn} from '../src/services/setup-claim.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {ServerClaimLookupResponse} from '../../packages/contracts/src/hosted/types.ts';

const origin = 'https://accounts.example', code = 'claim_' + Buffer.alloc(32, 7).toString('base64url');
const href = origin + '/claim#' + new URLSearchParams({code, operation: 'actual_operation', server: 'Actual server'});
const capture = (url = href, expected = origin) => {
  const parsed = new URL(url), page = {href: url, pathname: parsed.pathname, search: parsed.search, hash: parsed.hash};
  let scrubbed = false;
  const result = captureHostedSetupClaim(page, {state: null, replaceState: () => {scrubbed = true; page.hash = '';}}, expected);
  return {result, scrubbed, page};
};
function fixture() {
  let session = {account: {id: 'actual-account', username: 'setup', displayName: 'Setup'}, familyId: 'actual-family', accessToken: 'in-memory-test-access'};
  const now = Date.now();
  let claim: ServerClaimLookupResponse = {code, serverId: 'srv_' + Buffer.alloc(32, 1).toString('base64url'), name: 'Actual server', address: 'https://original.example:32500',
    requestedAt: new Date(now - 1000).toISOString(), expiresAt: new Date(now + 120000).toISOString(), purpose: 'setup', status: 'pending', setupTransactionId: 'opaque_setup_transaction'};
  let location = claim.address + '/auth/portico-return#transaction=' + claim.setupTransactionId;
  const calls: {path: string; method: string; body: unknown; token: string}[] = [];
  let after = (_path: string) => {}, rejectComplete = false;
  const account = {api: {origin, send: async (path: string, method: string, body: unknown, _signal: AbortSignal, token: string) => {
    calls.push({path, method, body, token});
    const reply = path.endsWith('/lookup') ? {...claim} : path.endsWith('/complete') ? {location} : {status: 'decided', code: 'claim_decided'};
    after(path);
    if (rejectComplete && path.endsWith('/complete')) throw new ApiError(401, 'credentials_required', 'credentials_required', false);
    return reply;
  }}, service: {synchronize: async () => {}, accessSession: async () => session, getSnapshot: () => ({session})}} as unknown as ServicesAccount;
  return {account, calls, request: capture().result!.request!, signal: new AbortController().signal,
    patch: (value: Partial<ServerClaimLookupResponse>) => {claim = {...claim, ...value};}, after: (work: typeof after) => {after = work;},
    replace: () => {session = {...session, familyId: 'replacement'};},
    installed: () => {claim = {...claim, status: 'approved', setupInstalled: true};},
    location: (value: string) => {location = value;}, rejectComplete: () => {rejectComplete = true;}};
}

test('setup fragment capture scrubs synchronously and refuses legacy arbitrary return or ambiguous fields', () => {
  const f = capture(); assert(f.scrubbed); assert.equal(f.page.hash, ''); assert.equal(f.result?.request?.code, code);
  for (const url of [href + '&return=https%3A%2F%2Fevil.example', href + '&code=' + code, href.replace('operation=actual_operation&', ''),
    href.replace(code, code + '='), href.replace('/claim#', '/claim?other=1#'), href.replace('https://accounts.', 'https://different.')]) {
    const bad = capture(url); assert(bad.scrubbed); assert.equal(bad.result?.request, undefined);
  }
});
test('authenticated lookup provides setup facts and never starts or approves anything', async () => {
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal);
  assert.equal(review.name, 'Actual server'); assert.equal(review.transactionId, 'opaque_setup_transaction');
  assert(Object.isFrozen(review) && Object.isFrozen(review.identity) && Object.isFrozen(review.request));
  assert.deepEqual(f.calls.map(value => [value.path, value.body]), [['/v1/server-claims/lookup', {code}]]);
});
test('setup rejects switch-only owner/count facts',async()=>{
 for(const patch of [{ownerTransactionId:'owner-custody'},{otherPeopleCount:0},{currentOwnerUsername:'existing_owner'},{previousOwnerRole:'member'}]){
  const f=fixture();f.patch(patch);await assert.rejects(reviewHostedSetupClaim(f.account,f.request,f.signal));
 }
});
test('only the reviewed current family can explicitly approve or deny the same claim', async () => {
  for (const decision of ['approve', 'deny'] as const) {
    const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal);
    await decideHostedSetupClaim(f.account, review, decision, f.signal);
    assert.equal(f.calls.length, 3);
    assert.equal(f.calls[2].path, '/v1/server-claims/' + (decision === 'approve' ? 'approve-code' : 'deny'));
    assert.deepEqual(f.calls[2].body, {code});
  }
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal); f.replace();
  await assert.rejects(decideHostedSetupClaim(f.account, review, 'approve', f.signal), {code: 'account_changed'});
  assert.equal(f.calls.length, 1);
});
test('a family replacement during recheck discards facts before a decision', async () => {
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal); f.after(f.replace);
  await assert.rejects(decideHostedSetupClaim(f.account, review, 'approve', f.signal), {code: 'account_changed'});
  assert.equal(f.calls.length, 2); assert(f.calls.every(value => value.path.endsWith('/lookup')));
});
test('setup, unsigned name hints, changed transactions and expired claims never approve', async () => {
  for (const patch of [{purpose: 'switch'}, {setupTransactionId: undefined}, {name: 'Different server'},
    {expiresAt: new Date(Date.now() - 1).toISOString()}]) {
    const f = fixture(); f.patch(patch); await assert.rejects(reviewHostedSetupClaim(f.account, f.request, f.signal)); assert.equal(f.calls.length, 1);
  }
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal); f.patch({setupTransactionId: 'different_transaction'});
  await assert.rejects(decideHostedSetupClaim(f.account, review, 'approve', f.signal)); assert.equal(f.calls.length, 2);
});
test('completion waits for the claim to install, then returns only the original signed address callback', async () => {
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal);
  assert.equal(await completeHostedSetupClaim(f.account, review, f.signal), undefined);
  assert(f.calls.every(value => value.path.endsWith('/lookup')));
  f.installed();
  assert.equal(await completeHostedSetupClaim(f.account, review, f.signal), 'https://original.example:32500/auth/portico-return#transaction=opaque_setup_transaction');
  assert.equal(f.calls.at(-1)?.path, '/v1/server-returns/opaque_setup_transaction/complete');
  assert.deepEqual(f.calls.at(-1)?.body, {});
  assert(f.calls.every(value => !value.path.endsWith('/start') && !value.path.endsWith('/exchange')));
});
test('response redirect and post-completion family changes cannot return a location', async () => {
  const f = fixture(); f.installed(); const review = await reviewHostedSetupClaim(f.account, f.request, f.signal);
  f.patch({setupInstalled: undefined});
  await assert.rejects(completeHostedSetupClaim(f.account, review, f.signal)); assert.equal(f.calls.length, 2);
  const g = fixture(), before = await reviewHostedSetupClaim(g.account, g.request, g.signal); g.installed(); g.location('https://evil.example/auth/portico-return#transaction=opaque_setup_transaction');
  await assert.rejects(completeHostedSetupClaim(g.account, before, g.signal));
  const h = fixture(), original = await reviewHostedSetupClaim(h.account, h.request, h.signal); h.installed();
  h.after(path => {if (path.endsWith('/complete')) h.replace();});
  await assert.rejects(completeHostedSetupClaim(h.account, original, h.signal), {code: 'account_changed'});
});
test('canonical completion refusal propagates once, without reapproval or an ordinary return fallback', async () => {
  const f = fixture(), review = await reviewHostedSetupClaim(f.account, f.request, f.signal); f.installed(); f.rejectComplete();
  await assert.rejects(completeHostedSetupClaim(f.account, review, f.signal), {status: 401});
  assert.equal(f.calls.filter(value => value.path.endsWith('/complete')).length, 1);
  assert(f.calls.every(value => value.path.endsWith('/lookup') || value.path.endsWith('/complete')));
});
test('setup return accepts the narrow original localhost exception, never network HTTP or ordinary code/state', () => {
  const review = {address: 'http://localhost:32500', transactionId: 'opaque_setup_transaction'};
  const location = review.address + '/auth/portico-return#transaction=' + review.transactionId;
  assert.equal(validateHostedSetupReturn(location, review), location);
  for (const invalid of [location + '&code=private', location + '&state=private', location.replace('transaction=', 'setupTransactionId='),
    location.replace('localhost', '127.0.0.1'), location.replace('/auth/', '/auth/../auth/'), location.replace('#', '?return=1#')])
    assert.throws(() => validateHostedSetupReturn(invalid, review));
  assert.throws(() => validateHostedSetupReturn('http://server.example/auth/portico-return#transaction=opaque_setup_transaction', {...review, address: 'http://server.example'}));
});

test('setupInstalled with an approved claim offers Return; absent offers none', async () => {
  const f = fixture(); f.installed();
  assert.equal((await reviewHostedSetupClaim(f.account, f.request, f.signal)).installed, true);
  const g = fixture(); g.patch({status: 'approved'});
  assert.equal((await reviewHostedSetupClaim(g.account, g.request, g.signal)).installed, false);
});
test('setupInstalled with a pending claim, a non-true value or a setup registration is invalid', async () => {
  for (const patch of [{setupInstalled: true}, {status: 'approved', setupInstalled: false}, {status: 'approved', setupInstalled: 'yes'},
    {status: 'approved', returnRegistration: {id: 'ret_' + Buffer.alloc(32, 2).toString('base64url'), origin: 'https://managed.example'}}]) {
    const f = fixture(); f.patch(patch as never); await assert.rejects(reviewHostedSetupClaim(f.account, f.request, f.signal));
  }
});
