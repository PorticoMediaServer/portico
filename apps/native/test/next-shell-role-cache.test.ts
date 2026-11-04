import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';

// Client data step 6: owner and admin documents live in the viewer's one cache, so the role is part
// of that cache's scope, and a role change empties it at once. This does not wait for the shell to
// restart: here the restart is never carried out, and the owner's documents are gone anyway.
test('privacy: a role change empties admin documents from the viewer\'s cache, without waiting for the shell to restart', async () => {
  const app = await standardHomeApp({mountUI: false, accountRole: 'owner'});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {viewerQueriesFor} = await import('../src-next/shell/freshness');
  let context: NativeShellBootstrapContext | undefined, renewed = 0;
  const ended: unknown[] = [];
  const runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);},
    {onAuthorityEnded(error) {ended.push(error);}, onSessionRenewed() {renewed++;}});
  try {
    const serverId = context!.session()!.viewer.serverId;
    const owner = viewerQueriesFor(context!.api, {serverId});
    assert.match(owner.scope.viewerId, /"owner"/, 'the role is part of the cache\'s scope');
    const activity = ['admin', 'activity'];
    owner.client.setQueryData(activity, {jobs: ['scan of Films']});

    app.setAccountRole('member');
    // The refresh round sees the new role (its account read is refused for the old one) and asks the server who this is now.
    await runtime!.refresh().catch(() => undefined);
    for (let i = 0; i < 50 && !renewed; i++) await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(renewed, 1, 'the shell asked to be rebuilt for the new role');
    assert.equal(ended.length, 0, 'a demotion is not a sign-out');
    assert.equal(owner.isDisposed(), true, 'the owner\'s cache ended when the role changed');
    assert.equal(owner.client.getQueryData(activity), undefined, 'nothing read as an owner is kept');
    assert.equal(owner.client.getQueryCache().getAll().length, 0);
    await assert.rejects(owner.api.request('/v1/admin/activity'), {name: 'AbortError'}, 'and it reads nothing more');
    assert.throws(() => viewerQueriesFor(context!.api, {serverId}), {name: 'AbortError'}, 'a screen still mounted finds the ended cache, never a live one');
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});
