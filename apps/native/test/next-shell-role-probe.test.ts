import assert from 'node:assert/strict';
import test from 'node:test';
import {createRoleProbe} from '../src-next/shell/role-probe.ts';

// R-15: after a refused privileged read the get_me probe ends the shell only on proof.

const viewer = (role: string, profileId = 'p') => ({accountId: 'a', profileId, serverId: 's', authority: 'local', role});
function fixture(probe: () => Promise<{viewer: ReturnType<typeof viewer>}>) {
  const events: string[] = [];
  const run = createRoleProbe({alive: () => true, viewer: () => viewer('member'), probe, rebuild: () => events.push('rebuild'), end: () => events.push('end')});
  return {run, events};
}
const settle = () => new Promise(resolve => setTimeout(resolve, 5));

test('a transient get_me failure keeps the shell and a later refusal can probe again', async () => {
  let calls = 0, failure: unknown = new TypeError('network down');
  const f = fixture(async () => {calls++; if (failure) throw failure; return {viewer: viewer('member')};});
  f.run(); await settle();
  assert.deepEqual(f.events, [], 'network error: not ended');
  failure = Object.assign(new Error('boom'), {status: 503}); f.run(); await settle();
  assert.deepEqual(f.events, [], '5xx: not ended');
  assert.equal(calls, 2, 'the probe is released after a transient failure');
  failure = Object.assign(new Error('no'), {status: 403}); f.run(); await settle();
  assert.deepEqual(f.events, ['end'], 'a proven 403 ends');
});

test('a proven 401 ends, an unchanged role after a real answer ends, a changed role rebuilds', async () => {
  const denied = fixture(async () => {throw Object.assign(new Error('no'), {status: 401});}); denied.run(); await settle();
  assert.deepEqual(denied.events, ['end']);
  const same = fixture(async () => ({viewer: viewer('member')})); same.run(); await settle();
  assert.deepEqual(same.events, ['end']);
  const changed = fixture(async () => ({viewer: viewer('admin')})); changed.run(); await settle();
  assert.deepEqual(changed.events, ['rebuild']);
  const otherIdentity = fixture(async () => ({viewer: viewer('admin', 'other')})); otherIdentity.run(); await settle();
  assert.deepEqual(otherIdentity.events, ['end']);
});
