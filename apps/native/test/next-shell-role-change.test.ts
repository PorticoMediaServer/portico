import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

// CC-03: a server-side role change rebuilds role-dependent services instead of ending the
// signed-in session, and a demotion removes privileged controls.
test('a role change on refresh keeps the signed-in Home and rebuilds privileged controls for the new role', async t => {
  const app = await standardHomeApp({accountRole: 'owner', deleteCapability: true});
  t.after(async () => {
    try { await app.unmount(); } finally { app.disposeLoader(); }
  });
  const home = () => app.hosts().some((host: any) => host.props.testID === 'native-home');
  const deleteOffered = async () => {
    await app.more();
    const offered = app.hosts().some((host: any) => host.name === 'Pressable' && host.props.accessibilityLabel === 'Delete…');
    await app.back();
    return offered;
  };
  assert.ok(home()); assert.equal(await deleteOffered(), true, 'the owner is offered the privileged delete');
  app.setAccountRole('member');
  await app.refresh(); await app.settle();
  assert.ok(home(), 'a demotion is not a sign-out: Home is rebuilt for the same viewer');
  assert.equal(await deleteOffered(), false, 'privileged delete is removed after demotion');
  app.setAccountRole('owner');
  await app.refresh(); await app.settle();
  assert.ok(home(), 'a promotion is not a sign-out either');
  assert.equal(await deleteOffered(), true, 'privileged delete returns after promotion');
});

