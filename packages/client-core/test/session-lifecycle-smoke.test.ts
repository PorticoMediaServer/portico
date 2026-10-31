import test from 'node:test';
import assert from 'node:assert/strict';
import {SessionLifecycle, type LifecyclePorts} from '../src/session-lifecycle-runtime.ts';
import {createManualScheduler} from '../src/timer.ts';
import {ApiError} from '../src/index.ts';
import type {HeldSignIn} from '../src/session-lifecycle.ts';

const server = {serverId: 'server', name: 'Portico demo', signIn: 'local' as const};
const viewer = (profileId: string, familyId: string) => ({accountId: 'account', profileId, role: 'owner', familyId});
const profiles = [{id: 'main', name: 'Demo', primary: true, pinRequired: true}, {id: 'kids', name: 'Kids', primary: false, pinRequired: false}];
const held = (remembered: boolean): HeldSignIn => ({server, viewer: viewer('main', 'f1'), remembered, profilePinRequired: true});

function ports(over: Partial<LifecyclePorts> = {}): LifecyclePorts & {calls: string[]} {
  const calls: string[] = [];
  return Object.assign({calls,
    readRecords: async () => ({outcome: 'ok' as const, servers: 1, held: held(false)}),
    resetRecords: async () => {calls.push('reset');},
    readHeld: async () => ({servers: 1, held: held(false)}),
    connect: async () => ({profiles, rememberProfiles: true}),
    select: async (input: {profileId: string; remember: boolean}) => {calls.push('select:' + input.profileId); return {viewer: viewer(input.profileId, 'f2'), remembered: input.remember};},
    storeSelect: async () => {}, signOutServer: async () => ({servers: 0}), signOutAccount: async () => ({servers: 0}), forgetServer: async () => ({servers: 0}),
    clearMarker: async () => {calls.push('clearMarker');}, fenceShell: () => {calls.push('fence');}, drainRevocations: () => {calls.push('drain');},
    observeRecords: () => () => {}, classifyConnect: () => undefined,
  }, over);
}
const settle = () => new Promise(resolve => setImmediate(resolve));

test('a remembered profile resumes at launch with no picker; a main profile with a PIN keeps management locked', async () => {
  const life = new SessionLifecycle(ports({readRecords: async () => ({outcome: 'ok', servers: 1, held: held(true)})}));
  life.start(); await settle(); await settle();
  const s = life.getSnapshot().state;
  assert.equal(s.kind, 'ready'); assert.equal(s.kind === 'ready' && s.remembered, true); assert.equal(s.kind === 'ready' && s.manageLocked, true);
  assert.deepEqual(life.authority(), {serverId: 'server', accountId: 'account', profileId: 'main', familyId: 'f1'});
});

test('without a marker the picker shows, and the PIN screen picks with remember', async () => {
  const p = ports();
  const life = new SessionLifecycle(p);
  life.start(); await settle(); await settle();
  assert.equal(life.getSnapshot().state.kind, 'choosingProfile');
  life.actions.pickProfile.start('main'); await settle();
  assert.equal(life.getSnapshot().state.kind, 'enteringPin');
  life.actions.submitPin.start('1234', true); await settle(); await settle();
  const s = life.getSnapshot().state;
  assert.equal(s.kind, 'ready'); assert.equal(s.kind === 'ready' && s.remembered, true);
  assert.ok(p.calls.includes('select:main'));
  assert.equal(life.actions.submitPin.getSnapshot().phase, 'done');
});

test('a wrong PIN keeps the PIN screen with the server floor, and the action fails visibly', async () => {
  const scheduler = createManualScheduler(1000);
  const life = new SessionLifecycle(ports({scheduler, select: async () => {throw new ApiError(403, 'profile_pin_invalid', 'no', false, 30);}}));
  life.start(); await settle(); await settle();
  life.actions.pickProfile.start('main'); await settle();
  life.actions.submitPin.start('0000', false); await settle(); await settle();
  const s = life.getSnapshot().state;
  assert.equal(s.kind, 'enteringPin'); assert.equal(s.kind === 'enteringPin' && s.waitUntil, 1000 + 30_000);
  assert.equal(life.actions.submitPin.getSnapshot().phase, 'failed'); assert.equal(life.getSnapshot().pending, undefined);
  life.dispose();
});

test('a gated sign-in goes to the PIN screen, never to sign-in', async () => {
  const p = ports({readRecords: async () => ({outcome: 'ok', servers: 1, held: held(true)})});
  const life = new SessionLifecycle(p);
  life.start(); await settle(); await settle();
  life.report({type: 'gated'}); await settle();
  const s = life.getSnapshot().state;
  assert.equal(s.kind, 'enteringPin'); assert.equal(s.kind === 'enteringPin' && s.reason, 'reentry');
  assert.ok(p.calls.includes('fence') && p.calls.includes('clearMarker'));
});

test('another tab picking a profile is followed, never signed out', async () => {
  let listener = () => {};
  const p = ports({readRecords: async () => ({outcome: 'ok', servers: 1, held: held(true)}),
    observeRecords: l => {listener = l; return () => {};}, readHeld: async () => ({servers: 1, held: {server, viewer: viewer('kids', 'f9'), remembered: false}})});
  const life = new SessionLifecycle(p);
  life.start(); await settle(); await settle();
  const before = life.getSnapshot().generation;
  listener(); await settle(); await settle();
  const s = life.getSnapshot().state;
  assert.equal(s.kind, 'ready'); assert.equal(s.kind === 'ready' && s.viewer.familyId, 'f9'); assert.equal(s.kind === 'ready' && s.remembered, false);
  assert.ok(life.getSnapshot().generation > before);
  assert.ok(life.history().some(r => r.event === 'envelopeChanged' && r.row === 34));
});
