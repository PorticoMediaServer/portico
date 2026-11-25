import test from 'node:test';
import assert from 'node:assert/strict';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

test('setting or removing a PIN on the open profile does not end the shell, and not-on-this-server avatar reads become empty', async () => {
  let pinRequired = false;
  let profileReads = 0;
  let avatarNotFoundHits = 0;
  const app = await standardHomeApp({
    mountUI: false,
    nativeResponse: async (request: any, fixture: any) => {
      if (request.path === '/v1/me') {
        return {
          status: 200,
          body: {
            account: {id: fixture.viewer.accountId, username: 'fixture_member', role: fixture.viewer.role},
            profile: {id: fixture.viewer.profileId, name: 'Fixture profile', isPrimary: true, pinRequired, color: 'mint'},
            role: fixture.viewer.role,
            canManageAccount: true,
          },
        };
      }
      if (request.path === '/v1/profiles') {
        profileReads++;
        // Bootstrap runs: (1) connect readDirectAccount, (2) initial identities.avatars, (3) initial readAccount.
        // Refresh runs:   (4) refresh identities.avatars, (5) refresh readAccount.
        // Return 404 on the identities.avatars calls (2 and 4) to prove `noPictures` treats 404 as [].
        if (profileReads === 2 || profileReads === 4) {
          avatarNotFoundHits++;
          return {status: 404, body: {error: {code: 'not_found', message: 'Not on this server.'}}};
        }
        return {
          status: 200,
          body: {
            items: [{id: fixture.viewer.profileId, name: 'Fixture profile', isPrimary: true, pinRequired, color: 'mint'}],
          },
        };
      }
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let ended = 0;
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    // 1) Initial boot succeeds when identities.avatars hits 404 ("not on this server").
    runtime = await restoreNativeShellRuntime(
      context => createNativeShellBindings(context),
      {onAuthorityEnded() {ended++;}},
    );
    assert.ok(runtime, 'shell opens despite 404 from profile pictures read');
    assert.equal(avatarNotFoundHits, 1, 'initial avatars 404 was tolerated as no pictures');
    assert.equal(runtime.getSnapshot().profile.source, undefined);

    // 2) Setting a PIN on the open profile and refreshing keeps the shell alive (and tolerates 404 avatars).
    pinRequired = true;
    await runtime.refresh();
    assert.equal(ended, 0, 'setting a PIN on the open profile never ends this device\'s shell');
    assert.equal(avatarNotFoundHits, 2, 'refresh avatars 404 was tolerated as no pictures');

    // 3) Removing the PIN on the open profile and refreshing also keeps the shell alive.
    pinRequired = false;
    await runtime.refresh();
    assert.equal(ended, 0, 'removing a PIN on the open profile never ends this device\'s shell');
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});

test('a real server error (500) on profile pictures is not silently swallowed as no pictures', async () => {
  let profileReads = 0;
  const app = await standardHomeApp({
    mountUI: false,
    nativeResponse: async (request: any) => {
      if (request.path === '/v1/profiles') {
        profileReads++;
        if (profileReads === 2) {
          return {status: 500, body: {error: {code: 'internal_error', message: 'Database unavailable.'}}};
        }
      }
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    await assert.rejects(
      async () => {runtime = await restoreNativeShellRuntime(context => createNativeShellBindings(context), {onAuthorityEnded() {}});},
      /Database unavailable|internal_error|500/,
      'non-404 avatar failure still rejects instead of silently returning []',
    );
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});
