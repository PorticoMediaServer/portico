import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';

const session = {sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile'}};
const held = {server: {serverId: 'server'}, viewer: {accountId: 'account', profileId: 'profile', familyId: 'family'}};

test('a completed sign-in admits only the same durable profile while its form is current', async () => {
  const admitted: unknown[] = [];
  let selected = held, current = true;
  const ports = {readHeld: async () => ({held: selected})};
  const {admitNativeSignIn} = await screenModule(new URL('../src-next/platform/native-session-lifecycle.ts', import.meta.url), {
    react: {default: {}},
    '../../../../packages/client-core/src/cleanup': {},
    '../../../../packages/client-core/src/session-lifecycle-runtime': {SessionLifecycle: class {
      actions = {adoptSignedIn: {start: (...args: unknown[]) => admitted.push(args)}};
    }},
    './session-lifecycle-native': {createNativeLifecyclePorts: () => ports},
  });
  await admitNativeSignIn(session, () => current);
  assert.deepEqual(admitted, [[held, 'viewer']]);
  selected = {...held, viewer: {...held.viewer, familyId: 'newer-sign-in'}};
  await admitNativeSignIn(session, () => current);
  selected = held; current = false;
  await admitNativeSignIn(session, () => current);
  assert.equal(admitted.length, 1, 'a replaced or retired form cannot admit a profile');
});
