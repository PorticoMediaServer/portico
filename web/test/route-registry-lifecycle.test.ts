import test from 'node:test';
import assert from 'node:assert/strict';
import {bindWebAccountEntry, isAccountEntryPath} from '../src/route-registry-lifecycle.ts';
import {createEntryDestinationStore, localDeviceDestination, profileSettingsDestination, type WebEntryDestination} from '../src/route-registry-destination.ts';
import {ShellBootstrap, type ShellAssembler, type ShellBootstrapSnapshot} from '../src/shell/bootstrap.ts';
import type {SelectedViewerRecord} from '@core/viewer-selection.ts';
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
function navigation(path: string) {
  type Location = {pathname: string; search?: Record<string, unknown>};
  type Navigation = {fromLocation: Location; toLocation: Location};
  let before: ((event: Navigation) => void) | undefined, resolved: (() => void) | undefined;
  const calls: unknown[] = [];
  const go = (to: string, search?: Record<string, unknown>) => {const fromLocation = router.state.location, toLocation = {pathname: to, search};before?.({fromLocation, toLocation});router.state.location = toLocation;resolved?.();};
  const router = {state: {location: {pathname: path} as Location},
    subscribe(name: 'onBeforeNavigate' | 'onResolved', listener: ((event: Navigation) => void) | (() => void)) {
      if (name === 'onBeforeNavigate') {before = listener as (event: Navigation) => void;return () => {before = undefined;};}
      resolved = listener as () => void;return () => {resolved = undefined;};
    },
    navigate: async (target: WebEntryDestination & {replace: boolean}) => {calls.push(target);go(target.to, target.search);},
  };
  return {router, calls, go};
}
const unavailable: ShellAssembler = async () => {throw new Error('Unexpected assembly');};
test('entry destinations require exact paths and do not restore private state on initial entry', () => {
  for (const path of ['/setup', '/sign-in', '/profiles', '/servers', '/auth/portico-return']) {
    let reads = 0;const bootstrap = new ShellBootstrap(unavailable, {selected: {read: async () => {reads++;return null;}}, restore: async () => undefined});
    const nav = navigation(path), close = bindWebAccountEntry(nav.router, bootstrap);
    assert.equal(reads, 0);assert.deepEqual(bootstrap.getSnapshot(), {phase: 'signed-out', entry: true});close();
  }
  assert.equal(isAccountEntryPath('/servers/other'), false);assert.equal(isAccountEntryPath('/account'), false);
});
test('entering profiles fences an outstanding restore; leaving waits then reads the actual new selection', async () => {
  let reads = 0, restores = 0;const gate = deferred<SelectedViewerRecord | null>();
  const bootstrap = new ShellBootstrap(unavailable, {selected: {read: async () => ++reads === 1 ? gate.promise : null}, restore: async () => {restores++;return undefined;}});
  const nav = navigation('/'), close = bindWebAccountEntry(nav.router, bootstrap);
  nav.go('/profiles');assert.equal(bootstrap.getSnapshot().entry, true);
  nav.go('/');assert.equal(reads, 1);
  gate.resolve({session: {sessionFamilyId: 'retired'}} as SelectedViewerRecord);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads, 2);assert.equal(restores, 0);assert.equal(bootstrap.getSnapshot().phase, 'signed-out');close();
});
test('disposing while account exit waits cannot resurrect a private restore', async () => {
  let reads = 0;const gate = deferred<SelectedViewerRecord | null>();
  const bootstrap = new ShellBootstrap(unavailable, {selected: {read: async () => {reads++;return gate.promise;}}, restore: async () => undefined});
  const nav = navigation('/'), close = bindWebAccountEntry(nav.router, bootstrap);
  nav.go('/servers');nav.go('/');close();gate.resolve(null);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads, 1);assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
  nav.go('/profiles');assert.equal(bootstrap.getSnapshot().entry, undefined);
});


function admission() {
  let state: ShellBootstrapSnapshot = {phase: 'signed-out'};
  const listeners = new Set<() => void>();
  const publish = (next: ShellBootstrapSnapshot) => {state = next;for (const listener of listeners) listener();};
  return {publish, getSnapshot: () => state,
    subscribe(listener: () => void) {listeners.add(listener);return () => {listeners.delete(listener);};},
    start: async () => {}, suspendForAccountEntry: () => publish({phase: 'signed-out', entry: true}),
    resumeAfterAccountEntry: async () => {publish({phase: 'restoring'});}, dispose: () => publish({phase: 'signed-out'}),
  };
}
test('the drawn profile alias retains navigation through signed-out and restricted setup until an admitted binding exists', async () => {
  const destination = createEntryDestinationStore(), nav = navigation('/settings/profile'), bootstrap = admission();
  const close = bindWebAccountEntry(nav.router, bootstrap, destination);
  nav.go('/settings', profileSettingsDestination.search);nav.go('/sign-in');
  bootstrap.publish({phase: 'signed-out', entry: true});
  nav.go('/profiles');assert.deepEqual(nav.calls, []);
  nav.go('/');assert.deepEqual(nav.calls, []);
  bootstrap.publish({phase: 'ready'});assert.deepEqual(nav.calls, [], 'a phase label without an admitted binding is insufficient');
  bootstrap.publish({phase: 'ready', binding: {scopeKey: 'captured-admitted-family'} as never});
  await Promise.resolve();assert.deepEqual(nav.calls, [{...profileSettingsDestination, replace: true}]);
  assert.equal(destination.take(), undefined);close();
});
test('retiring the browser root cannot redirect from a late admission notification', () => {
  const destination = createEntryDestinationStore(), nav = navigation('/settings/profile'), bootstrap = admission();
  const close = bindWebAccountEntry(nav.router, bootstrap, destination);nav.go('/sign-in');nav.go('/');close();
  bootstrap.publish({phase: 'ready', binding: {scopeKey: 'late'} as never});assert.deepEqual(nav.calls, []);
});
test('an unrelated startup does not consume a pending destination before an actual account-entry continuation', () => {
  const destination = createEntryDestinationStore();destination.rememberProfileSettings();
  const nav = navigation('/auth/portico-return'), bootstrap = admission();
  const close = bindWebAccountEntry(nav.router, bootstrap, destination);
  bootstrap.publish({phase: 'ready', binding: {scopeKey: 'existing'} as never});
  assert.deepEqual(nav.calls, []);assert.deepEqual(destination.take(), profileSettingsDestination);close();
});

test('the local device URL returns only after normal sign-in creates a ready server binding', async () => {
  const destination = createEntryDestinationStore(), nav = navigation('/device'), bootstrap = admission();
  const close = bindWebAccountEntry(nav.router, bootstrap, destination);
  nav.go('/sign-in');nav.go('/profiles');nav.go('/');
  bootstrap.publish({phase: 'ready'});assert.equal(nav.calls.length, 0);
  bootstrap.publish({phase: 'ready', binding: {scopeKey: 'actual-server-family'} as never});
  await Promise.resolve();assert.deepEqual(nav.calls, [{...localDeviceDestination, replace: true}]);
  assert.equal(destination.take(), undefined);close();
});
test('explicitly leaving local approval cancels its public return destination', () => {
  const destination = createEntryDestinationStore(), nav = navigation('/device'), bootstrap = admission();
  const close = bindWebAccountEntry(nav.router, bootstrap, destination);
  nav.go('/library/ordinary');nav.go('/sign-in');nav.go('/');
  bootstrap.publish({phase: 'ready', binding: {scopeKey: 'family'} as never});
  assert.equal(nav.calls.length, 0);assert.equal(destination.take(), undefined);close();
});
test('a local device intent stores only a fixed destination, never the fragment or a server/account', () => {
  const stored = new Map<string, string>();
  const store = createEntryDestinationStore(() => ({getItem: key => stored.get(key) ?? null, setItem: (key, value) => {stored.set(key, value);}, removeItem: key => {stored.delete(key);}}));
  store.rememberLocalDevice();assert.deepEqual([...stored.values()], ['local-device']);
  store.rememberProfileSettings();store.clearLocalDevice();assert.deepEqual(store.take(), profileSettingsDestination);
  store.rememberLocalDevice();assert.deepEqual(store.take(), localDeviceDestination);assert.equal(stored.size, 0);
});
