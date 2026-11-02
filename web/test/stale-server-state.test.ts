import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {RouteError} = await import('../../packages/client-core/src/route-identity.ts');
const {SessionRefreshError} = await import('../../packages/client-core/src/server-connections.ts');
const {savedSignInUnusable} = await import('../src/shell/connection-retry.ts');
const {ShellBootstrap} = await import('../src/shell/bootstrap.ts');
const {initializeAccountSignIn} = await import('../src/screens/accounts/sign-in-start.ts');
const {LocalServerSignIn} = await import('../src/kit/accounts/ServerSignIn.tsx');

// A browser that signed in to a server which was later reset or replaced at the same address
// (demo.getportico.tv after a fresh deploy) still holds that sign-in. Launch must reach sign-in.
const saved = {version: 1, serverUrl: 'https://demo.example', session: {sessionFamilyId: 'family-a',
  viewer: {serverId: 'srv_old', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}}};
function launch(restore: () => Promise<unknown>) {
  const cleared: string[] = [];
  const bootstrap = new ShellBootstrap(async () => {throw new Error('unexpected assembly');}, {
    startedAt: Date.now(), restore: restore as never, actions: () => ({switchServer: async () => {}}) as never,
    selected: {read: async () => saved as never, clearIfFamily: async (family: string) => {cleared.push(family);}},
  });
  return {bootstrap, cleared};
}

test('saved sign-ins that cannot open here are classified; transport and contract faults are not', () => {
  assert.equal(savedSignInUnusable(new RouteError('identity_mismatch', 'changed')), 'kept');
  assert.equal(savedSignInUnusable(new RouteError('invalid_saved_connection', 'damaged')), 'kept');
  assert.equal(savedSignInUnusable(new RouteError('access_refused', 'refused')), 'kept');
  assert.equal(savedSignInUnusable(new SessionRefreshError('refresh_refused', 'ended', true)), 'ended');
  assert.equal(savedSignInUnusable(new SessionRefreshError('refresh_unavailable_now', 'busy', false)), undefined);
  for (const value of [new RouteError('route_unavailable', 'away'), new Error('bug'), undefined, 'identity_mismatch']) assert.equal(savedSignInUnusable(value), undefined);
});

test('a saved server whose address now proves another server opens sign-in and keeps the record', async t => {
  const {bootstrap, cleared} = launch(async () => {throw new RouteError('identity_mismatch', 'This address belongs to a different server.');});
  t.after(() => bootstrap.dispose());
  await bootstrap.start();
  assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
  assert.equal(bootstrap.getSnapshot().error, undefined);
  assert.deepEqual(cleared, [], 'the identity page on sign-in offers Forget; launch removes nothing');
});

test('a sign-in the server ended opens sign-in and removes only that family', async t => {
  const {bootstrap, cleared} = launch(async () => {throw new SessionRefreshError('refresh_refused', 'Your sign-in has ended.', true);});
  t.after(() => bootstrap.dispose());
  await bootstrap.start();
  assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
  await Promise.resolve();
  assert.deepEqual(cleared, ['family-a']);
});

test('an unknown failure keeps the honest error page', async t => {
  const failure = new Error('a real bug');
  const {bootstrap, cleared} = launch(async () => {throw failure;});
  t.after(() => bootstrap.dispose());
  await bootstrap.start();
  assert.equal(bootstrap.getSnapshot().phase, 'error');
  assert.equal(bootstrap.getSnapshot().error, failure);
  assert.deepEqual(cleared, []);
});

test('sign-in keeps the saved server identity page instead of inspecting the address again', async () => {
  const calls: unknown[] = [];
  const state = {phase: 'profiles' as const, error: new RouteError('identity_mismatch', 'This address belongs to a different server.')};
  await initializeAccountSignIn({
    getSnapshot: () => state as never,
    finishPorticoReturn: async () => {calls.push('finish');},
    inspectMoveLink: async () => {calls.push('move');},
    loadProfiles: async () => {calls.push('profiles');},
    inspect: async (origin: string) => {calls.push(['inspect', origin]); return undefined;},
  }, {pageOrigin: 'https://demo.example', isCurrent: () => true, selectedOrigin: async () => 'https://demo.example', username: () => {}});
  assert.deepEqual(calls, ['profiles']);
});

test('the sign-in button never draws a zero countdown', () => {
  for (const retryAt of [undefined, Date.now() - 1000, Date.now()]) {
    const markup = renderToStaticMarkup(createElement(LocalServerSignIn, {serverName: 'Server', retryAt, onSignIn() {}}));
    assert.doesNotMatch(markup, /Try again in/, String(retryAt));
    assert.doesNotMatch(markup, /0:00/);
  }
});
