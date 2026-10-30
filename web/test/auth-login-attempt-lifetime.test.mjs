import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {AccountAttemptIds, MFARequiredError} from '../../packages/client-core/src/hosted-security.ts';

// FX-161: the blocker's shape against the actual ServicesAuthFlow. A password
// login keeps its request id for an uncertain retry, but Back/Start again from
// an MFA challenge retires that challenge and only that login id, so the next
// explicit submit gets a fresh Hosted intent.

class ApiError extends Error {
  constructor(status, code, message, retryable, retryAfterSeconds) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    if (retryAfterSeconds !== undefined) this.retryAfterSeconds = retryAfterSeconds;
  }
}
class CodedError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

const options = {password: true, recovery: true, registration: true, providers: [], verifiedContactRequired: true, accountSecurity: true};
const session = {account: {id: 'account', username: 'account', displayName: 'Account'}, familyId: 'family', accessToken: 'fixture_access', expiresAt: '2030-01-01T00:00:00Z', refreshExpiresAt: '2030-01-02T00:00:00Z'};
const challenge = id => ({mfaRequired: true, challengeId: 'challenge-' + id, challengeToken: 'private-' + id, expiresAt: '2030-01-01T00:00:00Z'});

async function loadFlow() {
  return componentModule(new URL('../src/services/auth-flow.ts', import.meta.url), {
    react: {default: {}},
    '@core/index.ts': {ApiError},
    '@core/server-messages.ts': {CodedError},
    '@core/hosted-security.ts': {AccountAttemptIds, MFARequiredError},
    '../../../packages/contracts/src/hosted/routes.ts': {routePath: id => '/v1/' + id, routeBody: (_id, body) => body},
    './session': {
      accountIdentity: value => ({accountId: value?.account?.id, familyId: value?.familyId}),
      sameAccount: (a, b) => Boolean(a && b && a.accountId === b.accountId && a.familyId === b.familyId),
      currentAccountWork: async (_account, work) => work({accessToken: 'fixture_access'}),
    },
  });
}

// A local model of the Hosted receipt: the same password request id returns its
// live challenge, and stops doing so once that receipt expires.
function hosted() {
  const calls = [];
  const receipts = new Map();
  let challengeNumber = 0, authenticated = 0, signed = 0, codeFailure;
  const account = {
    api: {origin: 'https://web.getportico.tv'},
    client: {
      options: async () => options,
      registration: async () => null,
      transaction: async () => null,
      start: async (provider, input) => { calls.push({kind: 'provider', provider, requestId: input.requestId}); return {authorizationUrl: 'https://provider.example/authorize'}; },
      login: async (username, password, requestId) => {
        calls.push({kind: 'login', username, password, requestId});
        const receipt = receipts.get(requestId);
        if (receipt) {
          if (receipt.expired) throw new ApiError(409, 'challenge_invalid', 'challenge_invalid', false);
          throw new MFARequiredError(receipt.challenge);
        }
        const value = challenge(++challengeNumber);
        receipts.set(requestId, {challenge: value, expired: false});
        throw new MFARequiredError(value);
      },
      mfa: async (value, code) => {
        calls.push({kind: 'mfa', challengeId: value.challengeId, code});
        if (codeFailure) { const failure = codeFailure; codeFailure = undefined; throw failure; }
        const receipt = [...receipts.values()].find(item => item.challenge.challengeId === value.challengeId);
        if (!receipt || receipt.expired) throw new ApiError(409, 'challenge_invalid', 'challenge_invalid', false);
        return session;
      },
    },
    service: {
      // Count only an accepted result: a failed MFA submission calls the
      // coordinator too, and is not a completed sign-in.
      authenticateIfSignedOut: async work => { const value = await work(new AbortController().signal); authenticated++; return value; },
      getSnapshot: () => ({phase: 'ready', session, pendingRevocations: 0}),
    },
  };
  return {
    account, calls, receipts,
    signed: () => signed,
    authenticated: () => authenticated,
    signIn: () => { signed++; },
    expire: () => { for (const receipt of receipts.values()) receipt.expired = true; },
    failNextCode: failure => { codeFailure = failure; },
  };
}
const logins = fixture => fixture.calls.filter(call => call.kind === 'login');
const challenges = fixture => fixture.calls.filter(call => call.kind === 'mfa');

test('an expired or exhausted challenge restarts as a fresh password intent, and recovery finishes once', async () => {
  const {ServicesAuthFlow} = await loadFlow();
  const f = hosted();
  const flow = new ServicesAuthFlow(f.account, f.signIn, 'signin', () => {});
  await flow.signIn('account', 'password', '');
  assert.equal(flow.getSnapshot().mode, 'factor');
  const [first] = logins(f);
  assert.ok(first);

  f.expire();                                        // the Hosted receipt's five-minute window passes
  await flow.signIn('account', 'password', '123456');
  assert.equal(flow.getSnapshot().mode, 'factor');
  assert.equal(flow.getSnapshot().error?.code, 'challenge_invalid');
  assert.equal(challenges(f).length, 1);

  flow.go('signin');                                 // the drawn Back action
  assert.equal(flow.getSnapshot().mode, 'signin');
  assert.equal(flow.getSnapshot().error, undefined);
  await flow.signIn('account', 'password', '');
  assert.equal(logins(f).length, 2);
  assert.notEqual(logins(f)[1].requestId, logins(f)[0].requestId);
  assert.equal(flow.getSnapshot().mode, 'factor');

  flow.recovery();
  assert.equal(flow.getSnapshot().mode, 'recovery');
  await flow.signIn('account', '', 'one-use-recovery');
  assert.equal(challenges(f).length, 2);
  assert.notEqual(challenges(f)[1].challengeId, challenges(f)[0].challengeId);
  assert.equal(challenges(f)[1].code, 'one-use-recovery');
  assert.equal(f.authenticated(), 1);
  assert.equal(f.signed(), 1);                       // completed exactly once
  // Only the abandoned login key was retired; a later factor submission still
  // addressed the fresh challenge rather than the expired one.
  assert.equal(challenges(f).some(call => call.challengeId === challenges(f)[0].challengeId && call.code === 'one-use-recovery'), false);
  flow.dispose();
});

test('an uncertain password request keeps its request id until it is explicitly abandoned', async () => {
  const {ServicesAuthFlow} = await loadFlow();
  const f = hosted();
  const forward = f.account.client.login;
  let lost = true;
  f.account.client.login = async (...args) => {
    if (lost) { lost = false; f.calls.push({kind: 'login', requestId: args[2]}); throw new TypeError('network'); }
    return forward(...args);
  };
  const flow = new ServicesAuthFlow(f.account, f.signIn, 'signin', () => {});
  await flow.signIn('account', 'password', '');
  assert.ok(flow.getSnapshot().error);
  assert.equal(flow.getSnapshot().mode, 'signin');
  await flow.signIn('account', 'password', '');
  assert.equal(logins(f).length, 2);
  assert.equal(logins(f)[1].requestId, logins(f)[0].requestId);   // identical transport retry
  assert.equal(flow.getSnapshot().mode, 'factor');
  flow.dispose();
});

test('an account-wide cool-off survives the restart and fences the next submit', async () => {
  const {ServicesAuthFlow} = await loadFlow();
  const realNow = Date.now;
  let now = 1_000_000_000_000;
  Date.now = () => now;
  try {
    const f = hosted();
    const flow = new ServicesAuthFlow(f.account, f.signIn, 'signin', () => {});
    await flow.signIn('account', 'password', '');
    f.failNextCode(new ApiError(429, 'rate_limited', 'rate_limited', true, 5));
    await flow.signIn('account', 'password', '123456');
    assert.ok(flow.getSnapshot().retryAt >= now + 5000);

    flow.go('signin');
    assert.ok(flow.getSnapshot().retryAt >= now + 5000);          // not cleared by the restart
    flow.go('forgot');
    assert.ok(flow.getSnapshot().retryAt >= now + 5000);          // nor by a further mode switch
    flow.go('signin');
    assert.ok(flow.getSnapshot().retryAt >= now + 5000);          // the deadline still fences
    await flow.signIn('account', 'password', '');
    assert.equal(logins(f).length, 1);                            // still fenced

    now += 5000;
    await flow.signIn('account', 'password', '');
    assert.equal(logins(f).length, 2);
    assert.notEqual(logins(f)[1].requestId, logins(f)[0].requestId);
    flow.dispose();
  } finally { Date.now = realNow; }
});

test('a disposed form publishes no late challenge and starts no factor submission', async () => {
  const {ServicesAuthFlow} = await loadFlow();
  const f = hosted();
  let release, entered;
  const loginEntered = new Promise(resolve => { entered = resolve; });
  f.account.client.login = () => new Promise((_, reject) => {
    entered();
    release = () => reject(new MFARequiredError(challenge(99)));
  });
  const flow = new ServicesAuthFlow(f.account, f.signIn, 'signin', () => {});
  const pending = flow.signIn('account', 'password', '');
  await loginEntered;                                  // login is genuinely in flight
  const settled = flow.getSnapshot();
  flow.dispose();
  release();
  await pending;
  assert.equal(flow.getSnapshot(), settled);
  assert.equal(flow.getSnapshot().mode, 'signin');
  assert.equal(challenges(f).length, 0);
});

test('abandoning a password login preserves direct provider navigation', async () => {
  const {ServicesAuthFlow} = await loadFlow();
  const f = hosted();
  const destinations = [];
  const flow = new ServicesAuthFlow(f.account, f.signIn, 'signin', url => destinations.push(url));
  const first = flow.provider('apple');
  assert.deepEqual(destinations, ['https://web.getportico.tv/auth/sso/apple/start']); // before awaiting
  await first;
  await flow.provider('apple');
  await flow.signIn('account', 'password', '');        // an MFA challenge
  flow.go('signin');                                  // Back abandons only the login
  await flow.provider('apple');
  const providers = f.calls.filter(call => call.kind === 'provider');
  assert.equal(providers.length, 0); // browser login never uses the JSON start API
  assert.deepEqual(destinations, Array(3).fill('https://web.getportico.tv/auth/sso/apple/start'));

  await flow.signIn('account', 'password', '');
  assert.equal(logins(f).length, 2);
  assert.notEqual(logins(f)[1].requestId, logins(f)[0].requestId);
  flow.dispose();
});
