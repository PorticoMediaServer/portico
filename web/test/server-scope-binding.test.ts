import test from 'node:test';
import assert from 'node:assert/strict';
import {bindServerScope} from '../src/shell/server-scope-binding.ts';
import {createScopedLinkStore, createServerScope} from '../src/platform/server-scope.ts';

const flush = async () => {for (let i = 0; i < 10; i++) await Promise.resolve();};
function world(options: {hosted?: boolean; phase?: string; serverId?: string; saved?: string[]; path?: string; href?: string; address?: string} = {}) {
  const links = createScopedLinkStore();
  const scope = createServerScope(options.hosted ?? true, link => links.remember(link));
  const navigations: unknown[] = [], selects: string[] = [], replaced: string[] = [];
  let resolved = () => {}, changed = () => {};
  const router = {state: {location: {pathname: options.path ?? '/item/x', href: options.href ?? options.path ?? '/item/x'}},
    subscribe: (_event: 'onResolved', listener: () => void) => {resolved = listener; return () => {};},
    navigate: async (value: unknown) => {navigations.push(value);}};
  let snapshot: Record<string, unknown> = {phase: options.phase ?? 'ready'};
  const ready = (serverId: string) => {snapshot = {phase: 'ready', context: {session: () => ({viewer: {serverId}}), savedServers: (options.saved ?? []).map(id => ({id, label: id}))}};};
  if ((options.phase ?? 'ready') === 'ready') ready(options.serverId ?? 'srv-1');
  const bootstrap = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {changed = listener; return () => {};},
    selectServer: async (serverId: string) => {selects.push(serverId);}};
  const address = () => ({pathname: options.address ?? '/s/srv-1/item/x', search: '', hash: '', replace: (href: string) => {replaced.push(href);}});
  return {scope, links, router, navigations, selects, replaced, address, ready, set: (value: Record<string, unknown>) => {snapshot = value;}, changed: () => changed(), resolved: () => resolved(),
    bind: () => bindServerScope(router as never, bootstrap as never, scope, links, address)};
}

test('a link for the current server opens in place: no switch, no navigation, nothing remounts', async () => {
  const w = world();
  w.scope.rewrite.input({url: new URL('https://web.test/s/srv-1/item/x')});
  const off = w.bind();await flush();
  assert.deepEqual(w.selects, []);assert.deepEqual(w.navigations, []);assert.deepEqual(w.replaced, []);
  off();
});

test('a link for another server this browser is signed in to switches there and keeps the place', async () => {
  const w = world({saved: ['srv-2']});
  w.scope.rewrite.input({url: new URL('https://web.test/s/srv-2/item/x')});
  const off = w.bind();await flush();
  assert.deepEqual(w.selects, ['srv-2']);
  w.set({phase: 'restoring'});w.changed();w.ready('srv-2');w.changed();await flush();
  assert.equal(w.scope.current(), 'srv-2');assert.deepEqual(w.navigations, [], 'the route never changed, so nothing navigates');
  assert.equal(w.links.peek(), undefined, 'the kept link is consumed');
  off();
});

test('a link for a server that will not answer comes back and says so', async () => {
  const w = world({saved: ['srv-2']});
  w.scope.rewrite.input({url: new URL('https://web.test/s/srv-2/item/x')});
  const off = w.bind();await flush();
  w.set({phase: 'unreachable'});w.changed();await flush();
  assert.deepEqual(w.selects, ['srv-2', 'srv-1'], 'back to the server it came from');
  assert.deepEqual(w.navigations.at(-1), {to: '/link-unavailable', search: {reason: 'offline'}, replace: true});
  off();
});

test('a Hosted link for a server this browser has not joined goes to the chooser, which opens it if it can', async () => {
  const w = world();
  w.scope.rewrite.input({url: new URL('https://web.test/s/srv-9/library/l')});
  const off = w.bind();await flush();
  assert.deepEqual(w.navigations, [{to: '/servers', search: {link: 'srv-9'}, replace: true}]);
  w.router.state.location = {pathname: '/servers', href: '/servers?link=srv-9'};w.resolved();
  w.router.state.location = {pathname: '/', href: '/'};w.resolved();await flush();
  assert.deepEqual(w.navigations.at(-1), {to: '/link-unavailable', search: {reason: 'access'}, replace: true}, 'left on another server: it cannot open here');
  off();
});

test('a server’s own UI serves only itself: another server’s link says it cannot be opened', async () => {
  const w = world({hosted: false});
  w.scope.rewrite.input({url: new URL('https://own.test/s/srv-7/item/x')});
  const off = w.bind();await flush();
  assert.deepEqual(w.navigations, [{to: '/link-unavailable', search: {reason: 'access'}, replace: true}]);
  off();
});

test('signed out: the link survives sign-in and profile choice and opens on its server', async () => {
  const w = world({phase: 'signed-out', saved: ['srv-2']});
  w.scope.rewrite.input({url: new URL('https://web.test/s/srv-2/item/x?tab=details')});
  const off = w.bind();await flush();
  assert.deepEqual(w.links.peek(), {serverId: 'srv-2', href: '/item/x?tab=details'});
  // Sign-in and the profile choice happened; the person landed on Home of that server.
  w.router.state.location = {pathname: '/', href: '/'};w.ready('srv-2');w.changed();await flush();
  assert.deepEqual(w.navigations.at(-1), {href: '/item/x?tab=details', replace: true});
  assert.equal(w.links.peek(), undefined);
  off();
});

test('an old unscoped Hosted address is replaced in place by its scoped form', async () => {
  const w = world({address: '/item/x'});
  const off = w.bind();await flush();
  assert.deepEqual(w.replaced, ['/s/srv-1/item/x']);assert.deepEqual(w.navigations, []);
  off();
});
