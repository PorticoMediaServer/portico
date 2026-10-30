import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeShellRouteRegistry, type NativeRouteFactory} from '../src-next/shell/route-registry';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import type {NativeShellRuntime} from '../src-next/shell/runtime';
import {NativeShellNavigation, type NativeRouteAdmission} from '../src-next/shell/routes';

const runtime = {} as NativeShellRuntime;
const search = {kind: 'shell', destination: {kind: 'search'}} as const;

/** Real navigation plus a bounded context: unrelated private ports must never
 * be touched by the registry, so an accidental dependency fails immediately. */
function fixtureContext(current = () => true, admission?: NativeRouteAdmission): NativeShellBootstrapContext {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const unavailable = (): never => {throw new Error('unexpected_registry_fixture_port');};
  return {
    current, authority: () => authority, navigation: new NativeShellNavigation(authority, () => authority, admission),
    get api() {return unavailable();}, get home() {return unavailable();}, get events() {return unavailable();},
    // This route-only fixture has no active session.
    // Cold overlay admission may read this port; unrelated ports still throw.
    session: () => undefined, subscribeAuthority: unavailable, preferences: unavailable, subscribePreferences: unavailable,
    get appVersion() {return unavailable();}, get serverName() {return unavailable();},
    get playback() {return unavailable();}, get watch() {return unavailable();}, get account() {return unavailable();}, libraries: unavailable,
  };
}

test('screen-owned factories share no instance across restored viewer lifetimes', () => {
  const registry = new NativeShellRouteRegistry(), created: unknown[] = [], disposed: unknown[] = [];
  const first = fixtureContext(), second = fixtureContext();
  registry.registerDestination('search', context => {created.push(context); return {render: () => created.indexOf(context), dispose: () => {disposed.push(context);}};});
  const a = registry.bind(first), b = registry.bind(second);
  assert.deepEqual(created, [], 'a registry import does not create a controller');
  assert.equal(a.render(search, runtime), 0);
  assert.equal(a.render(search, runtime), 0);
  assert.equal(b.render(search, runtime), 1);
  a.dispose(); a.dispose();
  assert.deepEqual(disposed, [first]);
  assert.equal(a.render(search, runtime), null);
  assert.equal(b.render(search, runtime), 1);
  b.dispose(); assert.deepEqual(disposed, [first, second]);
});

test('a destination module coexists with the legacy shell renderer and route modules', () => {
  const registry = new NativeShellRouteRegistry(); let top = 0;
  registry.register('shell', {render: () => 'legacy'});
  registry.registerDestination('search', () => ({render: () => 'search', scrollToTop: () => {top++;}}));
  registry.registerRoute('home-customize', () => ({render: () => 'customize'}));
  const bound = registry.bind(fixtureContext());
  assert.equal(bound.render(search, runtime), 'search');
  assert.equal(bound.render({kind: 'shell', destination: {kind: 'home'}}, runtime), 'legacy');
  assert.equal(bound.render({kind: 'home-customize'}, runtime), 'customize');
  bound.scrollToTop(search); assert.equal(top, 1);
  assert.throws(() => registry.registerRoute('shell', () => ({render: () => null})), /already_registered/);
  assert.throws(() => registry.registerDestination('search', () => ({render: () => null})), /already_registered/);
  bound.dispose();
});

test('unregister retires only the actual module instance and a replacement is created lazily', () => {
  const registry = new NativeShellRouteRegistry(); let disposed = 0;
  const create: NativeRouteFactory = () => ({render: () => 'old', dispose: () => {disposed++;}});
  const unregister = registry.registerDestination('saved', create);
  const bound = registry.bind(fixtureContext());
  const saved = {kind: 'shell', destination: {kind: 'saved'}} as const;
  assert.equal(bound.render(saved, runtime), 'old');
  unregister(); assert.equal(disposed, 1); assert.equal(bound.render(saved, runtime), undefined);
  registry.registerDestination('saved', () => ({render: () => 'new'}));
  unregister(); assert.equal(bound.render(saved, runtime), 'new');
  bound.dispose(); assert.equal(disposed, 1);
});

test('Watch uses the same captured context/runtime, and retired authority cannot create or render modules', () => {
  const registry = new NativeShellRouteRegistry(); let current = true, created = 0, disposed = 0;
  const context = fixtureContext(() => current, () => false);
  const unregister = registry.registerWatch(value => {
    assert.equal(value, context); created++;
    return {render: actual => {assert.equal(actual, runtime); return 'watch';}, back: () => true, dispose: () => {disposed++;}};
  });
  assert.throws(() => registry.registerWatch(() => ({render: () => null})), /already_registered/);
  const bound = registry.bind(context);
  current = false; assert.equal(bound.renderWatch(runtime), null); assert.equal(created, 0);
  current = true; assert.equal(bound.renderWatch(runtime), 'watch'); assert.equal(created, 1);
  assert.equal(bound.back(), true);
  current = false; assert.equal(bound.renderWatch(runtime), null); assert.equal(bound.back(), false);
  unregister(); assert.equal(disposed, 1); bound.dispose(); assert.equal(disposed, 1);
});

test('the installed navigation policy rejects a direct render before its factory sees private data', () => {
  const registry = new NativeShellRouteRegistry(); let created = 0;
  registry.registerDestination('library', () => {created++; return {render: () => 'library'};});
  const bound = registry.bind(fixtureContext(() => true, () => false));
  assert.equal(bound.render({kind: 'shell', destination: {kind: 'library', libraryId: 'restricted'}}, runtime), null);
  assert.equal(created, 0); bound.dispose();
});

test('retained route output belongs to the same created module and retires with its authority', () => {
  const registry = new NativeShellRouteRegistry(), retained = ['actual retained view']; let current = true, created = 0;
  registry.registerRoute('home-customize', () => {created++; return {render: () => null, renderRetained: () => retained};});
  const context = fixtureContext(() => current), bound = registry.bind(context);
  assert.deepEqual(bound.renderRetained(runtime), [], 'an unopened module creates no private view');
  bound.render({kind: 'home-customize'}, runtime);
  const output = bound.renderRetained(runtime);
  assert.ok(Array.isArray(output)); assert.equal(output[0], retained);
  assert.equal(created, 1);
  current = false; assert.equal(bound.renderRetained(runtime), null);
  bound.dispose();
});

test('legacy teardown belongs to its registration and never to another viewer binding', () => {
  const registry = new NativeShellRouteRegistry(); let disposed = 0;
  const unregister = registry.register('home-customize', {render: () => 'legacy', dispose: () => {disposed++;}});
  const context = fixtureContext(), first = registry.bind(context), second = registry.bind(context);
  first.render({kind: 'home-customize'}, runtime); first.dispose(); assert.equal(disposed, 0);
  assert.equal(second.render({kind: 'home-customize'}, runtime), 'legacy');
  unregister(); unregister(); assert.equal(disposed, 1);
  second.dispose(); assert.equal(disposed, 1);
});

test('module overlays use one shared factory instance and stop at authority retirement', () => {
  const registry = new NativeShellRouteRegistry(); let current = true, renders = 0;
  const create = () => ({render: () => null, renderOverlays: () => {renders++; return 'captured module overlay';}});
  registry.registerRoute('title', create); registry.registerWatch(create);
  const bound = registry.bind(fixtureContext(() => current));
  assert.deepEqual(bound.renderOverlays(runtime), []);
  bound.renderWatch(runtime);
  bound.render({kind: 'title', entry: {id: 'actual', kind: 'movie', title: 'Actual title'}}, runtime);
  assert.deepEqual(bound.renderOverlays(runtime), ['captured module overlay']);
  assert.equal(renders, 1);
  current = false; assert.equal(bound.renderOverlays(runtime), null);
  bound.dispose();
});
