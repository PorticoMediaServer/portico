import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ApiError} from '@core/index.ts';
import type {IdentityOptions} from '@core/hosted-security.ts';
import {ServicesAuthFlow} from '../src/services/auth-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';

// FX133-B: absent-capability initialization failure and the explicit retry,
// authored UNRUN. Real ServicesAuthFlow; only the account port is a fixture.

const options: IdentityOptions = {password: true, recovery: true, registration: true,
  providers: [{id: 'apple', enabled: true, native: false, nativeClientIds: []}], verifiedContactRequired: true, accountSecurity: true};
function port(script: (attempt: number) => unknown) {
  let reads = 0, authenticated = 0;
  const value = {
    client: {options: async () => {reads++; const next = script(reads); if (next instanceof Error) throw next; return next;},
      transaction: async () => null, registration: async () => null, login: async () => ({}), mfa: async () => ({}), request: async () => ({})},
    service: {authenticateIfSignedOut: async (work: (signal: AbortSignal) => Promise<unknown>) => {authenticated++; return work(new AbortController().signal);}},
  } as unknown as ServicesAccount;
  return {value, reads: () => reads, authenticated: () => authenticated};
}

for (const mode of ['signin', 'create', 'forgot'] as const) test('a failed options read in '+mode+' is recoverable only by the explicit retry', async () => {
  const p = port(attempt => attempt === 1 ? new Error('service unavailable') : options);
  const flow = new ServicesAuthFlow(p.value, () => {}, mode);
  await flow.initialize();
  assert.deepEqual({options: flow.getSnapshot().options, failed: flow.getSnapshot().optionsFailed, busy: flow.getSnapshot().busy, mode: flow.getSnapshot().mode},
    {options: undefined, failed: true, busy: false, mode});
  for (let i = 0; i < 8; i++) await Promise.resolve();
  assert.equal(p.reads(), 1);                     // no automatic retry
  await flow.retryInitialization();
  assert.equal(p.reads(), 2);                     // exactly one additional request
  assert.equal(flow.getSnapshot().options?.password, true);
  assert.deepEqual(flow.getSnapshot().options?.providers.filter(provider => provider.enabled).map(provider => provider.id), ['apple']);
  assert.equal(flow.getSnapshot().optionsFailed, false);
  assert.equal(flow.getSnapshot().mode, mode);    // original mode preserved
  flow.dispose();
});

test('a concurrent initialize and retry share one flight and one request', async () => {
  let release!: (value: unknown) => void;
  let second = false;
  const p = port(attempt => {
    if (attempt === 1) return new Error('service unavailable');
    if (!second) { second = true; return new Promise(resolve => {release = resolve;}); }
    return options;
  });
  const flow = new ServicesAuthFlow(p.value, () => {});
  await flow.initialize();
  const first = flow.retryInitialization(), also = flow.initialize(), again = flow.retryInitialization();
  assert.equal(first, also); assert.equal(first, again);
  for (let i = 0; i < 6; i++) await Promise.resolve();
  assert.equal(p.reads(), 2);                       // second flight is in progress
  assert.equal(flow.getSnapshot().optionsFailed, true);
  release(options);
  await Promise.all([first, also, again]);
  assert.equal(p.reads(), 2);                       // single flight, one extra read
  assert.equal(flow.getSnapshot().options?.password, true);
  flow.dispose();
});

test('a successful initialization stays memoized', async () => {
  const p = port(() => options), flow = new ServicesAuthFlow(p.value, () => {});
  await flow.initialize(); await flow.initialize();
  assert.equal(p.reads(), 1);
  flow.dispose();
});

test('a 429 deadline allows exactly one retry at its exact instant, with no sleeps', async () => {
  const realNow = Date.now;
  let now = 1_000_000_000_000;
  Date.now = () => now;
  try {
    const p = port(attempt => attempt === 1 ? new ApiError(429, 'rate_limited', 'rate_limited', true, 5) : options);
    const flow = new ServicesAuthFlow(p.value, () => {});
    await flow.initialize();
    assert.equal(flow.getSnapshot().retryAt, now + 5000);
    await flow.retryInitialization();
    assert.equal(p.reads(), 1);                   // before the deadline
    now += 4999; await flow.retryInitialization();
    assert.equal(p.reads(), 1);
    now += 1;                                     // the exact deadline instant
    await flow.retryInitialization();
    assert.equal(p.reads(), 2);                   // one permitted retry, no waiting
    assert.equal(flow.getSnapshot().options?.password, true);
    flow.dispose();
  } finally { Date.now = realNow; }
});

test('a disposed form retries nothing, and the released original leaves the snapshot unchanged', async () => {
  let release!: (value: unknown) => void;
  const p = port(attempt => attempt === 1 ? new Promise(resolve => {release = resolve;}) as never : options);
  const flow = new ServicesAuthFlow(p.value, () => {});
  const pending = flow.initialize();
  flow.dispose();
  const before = flow.getSnapshot();
  await flow.retryInitialization();
  assert.equal(p.reads(), 1);
  release(options);
  await pending.catch(() => {});
  assert.deepEqual(flow.getSnapshot(), before);
  assert.equal(flow.getSnapshot().options, undefined);
  assert.equal(p.authenticated(), 0);
});
