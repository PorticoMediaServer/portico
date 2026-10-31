import assert from 'node:assert/strict';
import test from 'node:test';
import {ApiError} from '../../../packages/client-core/src';
import * as cleanup from '../../../packages/client-core/src/cleanup';
import * as runtime from '../../../packages/client-core/src/session-lifecycle-runtime';
import type {LifecyclePorts, SelectInput} from '../../../packages/client-core/src/session-lifecycle-runtime';
import {screenModule} from './helpers/screen-harness.mjs';

// C2b-2: switching profile inside the app goes through the session lifecycle, so a profile with a
// PIN always asks for it, and a refusal leaves the open profile as it was.

const server = {serverId: 's1', name: 'Home', signIn: 'local' as const};
const viewer = (profileId: string, familyId: string) => ({accountId: 'a1', profileId, role: 'member' as const, familyId});
const profiles = [{id: 'kid', name: 'Kid', primary: false, pinRequired: false}, {id: 'teen', name: 'Teen', primary: false, pinRequired: false},
  {id: 'parent', name: 'Parent', primary: true, pinRequired: true}];
const flush = async () => {for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve));};

async function opened(select: (input: SelectInput) => Promise<{viewer: ReturnType<typeof viewer>; remembered: boolean}>) {
  const selects: SelectInput[] = [], fences: string[] = [];
  const held = {server, viewer: viewer('kid', 'f1'), remembered: true, profilePinRequired: false};
  const ports: LifecyclePorts = {
    readRecords: async () => ({outcome: 'ok', servers: 1, held}),
    resetRecords: async () => {}, readHeld: async () => ({servers: 1, held}),
    connect: async () => ({profiles, rememberProfiles: true}),
    select: async input => {selects.push(input); return select(input);},
    storeSelect: async () => {}, signOutServer: async () => ({servers: 0}), signOutAccount: async () => ({servers: 0}),
    forgetServer: async () => ({servers: 0}), clearMarker: async () => {}, fenceShell: () => {fences.push('fence');},
    drainRevocations: () => {}, observeRecords: () => () => {}, classifyConnect: () => undefined,
  };
  const module = await screenModule(new URL('../src-next/platform/native-session-lifecycle.ts', import.meta.url), {
    react: {default: {}}, '../../../../packages/client-core/src/cleanup': cleanup,
    '../../../../packages/client-core/src/session-lifecycle-runtime': runtime,
    './session-lifecycle-native': {createNativeLifecyclePorts: () => ports},
  }) as typeof import('../src-next/platform/native-session-lifecycle');
  const {lifecycle} = module.nativeSessionLifecycle();
  lifecycle.start(); await flush();
  assert.equal(lifecycle.getSnapshot().state.kind, 'ready');
  const open = () => {const state = lifecycle.getSnapshot().state; return state.kind === 'ready' ? state.viewer : undefined;};
  return {switchTo: module.switchNativeProfile, lifecycle, open, selects, fences};
}

test('a profile with a PIN is opened only with its PIN, and the new sign-in is the one opened', async () => {
  const fx = await opened(async input => ({viewer: viewer(input.profileId, 'f2'), remembered: false}));
  await fx.switchTo('f1', 'parent', '1234');
  assert.deepEqual(fx.selects, [{kind: 'select', serverId: 's1', profileId: 'parent', pin: '1234', remember: false}]);
  assert.deepEqual(fx.open(), viewer('parent', 'f2'));
  assert.ok(fx.fences.length >= 1, 'the open shell is retired for the new sign-in');
});

test('without a PIN, a PIN-protected profile is refused and the open profile stays', async () => {
  const fx = await opened(async () => {throw new Error('never selected');});
  await assert.rejects(fx.switchTo('f1', 'parent'), (error: {code?: string}) => error.code === 'profile_pin');
  assert.deepEqual(fx.selects, []);
  assert.deepEqual(fx.open(), viewer('kid', 'f1'));
});

test('a wrong PIN is reported as itself and puts the open profile back', async () => {
  const wrong = new ApiError(403, 'profile_pin_invalid', 'Wrong PIN.');
  const fx = await opened(async () => {throw wrong;});
  await assert.rejects(fx.switchTo('f1', 'parent', '0000'), error => error === wrong);
  assert.deepEqual(fx.open(), viewer('kid', 'f1'));
});

test('a profile with no PIN switches without one', async () => {
  const fx = await opened(async input => ({viewer: viewer(input.profileId, 'f2'), remembered: false}));
  await fx.switchTo('f1', 'teen');
  assert.deepEqual(fx.selects, [{kind: 'select', serverId: 's1', profileId: 'teen', remember: false}]);
  assert.deepEqual(fx.open(), viewer('teen', 'f2'));
});

test('a screen built on an older sign-in is told the profiles changed, and nothing moves', async () => {
  const fx = await opened(async () => {throw new Error('never selected');});
  await assert.rejects(fx.switchTo('f-old', 'parent', '1234'), (error: {code?: string}) => error.code === 'stale');
  assert.deepEqual(fx.selects, []);
  assert.equal(fx.lifecycle.getSnapshot().state.kind, 'ready');
});
