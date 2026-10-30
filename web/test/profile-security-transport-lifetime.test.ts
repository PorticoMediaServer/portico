import test from 'node:test';
import assert from 'node:assert/strict';
import type {ProfileAPI} from '@core/profile-management.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {profileSecurityTransport} from '../src/screens/settings/account-profile/security-transport.ts';

// ACC-LEAF-18: the lifetime and allowlist boundaries of profileSecurityTransport,
// authored UNRUN. The transport is the real module and the paths are the generated
// route paths; the only stub is the original ProfileAPI it wraps.

type SecurityCommandId = 'post_direct_password' | 'post_direct_two_factor_enrol' | 'post_direct_two_factor_verify'
  | 'delete_direct_two_factor' | 'post_direct_two_factor_recovery_codes';
const allowlist: readonly (readonly [SecurityCommandId, 'POST' | 'DELETE'])[] = [
  ['post_direct_password', 'POST'],
  ['post_direct_two_factor_enrol', 'POST'],
  ['post_direct_two_factor_verify', 'POST'],
  ['delete_direct_two_factor', 'DELETE'],
  ['post_direct_two_factor_recovery_codes', 'POST'],
];

function harness() {
  let current = true;
  const parent = new AbortController();
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  let held: {promise: Promise<void>; resolve: () => void; reject: (error: unknown) => void} | undefined;
  const original: ProfileAPI = {request: async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    if (held) await held.promise;
    return {ok: true} as T;
  }};
  const transport = profileSecurityTransport(original, () => {if (!current) throw new DOMException('Settings build retired', 'AbortError');},
    input => input ? AbortSignal.any([parent.signal, input]) : parent.signal);
  return {transport, calls,
    retire: () => {current = false; parent.abort();},
    hold: () => {let resolve!: () => void, reject!: (error: unknown) => void;
      const promise = new Promise<void>((settle, refuse) => {resolve = settle; reject = refuse;});
      held = {promise, resolve, reject};},
    resolves: () => {held?.resolve();},
    rejects: (error: unknown) => {held?.reject(error);}};
}

test('every allowlisted route and method is forwarded to the original exactly once', async () => {
  for (const [id, method] of allowlist) {
    const h = harness();
    const path = routePath(id);
    const body = {operationId: 'op-' + id};
    const out = await h.transport.request(path, method, body) as {ok: boolean};
    assert.equal(out.ok, true);
    assert.deepEqual(h.calls, [{path, method, body}]);
  }
});

test('a wrong method, a query variant and a path variant are refused before any IO', async () => {
  const h = harness();
  const password = routePath('post_direct_password');
  for (const attempt of [
    () => h.transport.request(password, 'GET', {}),
    () => h.transport.request(password + '?retry=1', 'POST', {}),
    () => h.transport.request(password + '/', 'POST', {}),
    () => h.transport.request('/v1/direct/not-a-security-command', 'POST', {}),
  ]) {
    await assert.rejects(attempt(), /Not an account security command/);
  }
  assert.equal(h.calls.length, 0);
});

test('an aborted original signal prevents a fresh command from reaching IO', async () => {
  const h = harness();
  const aborted = AbortSignal.abort();
  await assert.rejects(h.transport.request(routePath('post_direct_password'), 'POST', {}, aborted), (error: unknown) => {
    return error instanceof DOMException && error.name === 'AbortError';
  });
  assert.equal(h.calls.length, 0);
});

test('a known original rejection stays that rejection after the route retires, with no replay', async () => {
  const h = harness(), refusal = new Error('The current password is wrong.');
  h.hold();
  const pending = h.transport.request(routePath('post_direct_two_factor_recovery_codes'), 'POST', {password: 'x', code: 'y'});
  // The original command is still in flight; the build retires first, then the
  // command rejects. The rejection must surface unchanged, never as retirement.
  await Promise.resolve();
  h.retire();
  h.rejects(refusal);
  await assert.rejects(pending, (error: unknown) => error === refusal);
  assert.equal(h.calls.length, 1);
  await assert.rejects(h.transport.request(routePath('post_direct_two_factor_recovery_codes'), 'POST', {password: 'x', code: 'y'}),
    (error: unknown) => error instanceof DOMException && error.name === 'AbortError');
  assert.equal(h.calls.length, 1);
});
