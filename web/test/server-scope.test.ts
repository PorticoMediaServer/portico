import test from 'node:test';
import assert from 'node:assert/strict';
import {createScopedLinkStore, createServerScope, isScopedScreenPath, parseScopedPath, scopedPathname} from '../src/platform/server-scope.ts';

test('O-161: scoped paths parse to their server and plain route; account and set-up paths never scope', () => {
  assert.deepEqual(parseScopedPath('/s/srv-1/item/abc'), {serverId: 'srv-1', pathname: '/item/abc'});
  assert.deepEqual(parseScopedPath('/s/srv-1'), {serverId: 'srv-1', pathname: '/'});
  assert.deepEqual(parseScopedPath('/s/srv-1/'), {serverId: 'srv-1', pathname: '/'});
  assert.deepEqual(parseScopedPath('/s/srv-1/home/rows/continue'), {serverId: 'srv-1', pathname: '/home/rows/continue'});
  for (const path of ['/s/srv-1/sign-in', '/s/srv-1/servers', '/s/srv-1/account', '/s/srv-1/setup', '/s/srv-1/settings', '/s/../item/x', '/s//item/x', '/item/x', '/s/a%2Fb/item/x'])
    assert.equal(parseScopedPath(path), undefined, path);
  for (const path of ['/', '/item/1', '/library/2', '/live', '/saved', '/person/3', '/read/4', '/player', '/home/rows/r', '/search', '/search/reading', '/libraries', '/dashboard', '/notifications', '/watch-together'])
    assert.equal(isScopedScreenPath(path), true, path);
  for (const path of ['/sign-in', '/servers', '/profiles', '/setup', '/account', '/settings', '/device', '/help', '/about', '/link-unavailable'])
    assert.equal(isScopedScreenPath(path), false, path);
  assert.equal(scopedPathname('/item/1', 'srv-1'), '/s/srv-1/item/1');
  assert.equal(scopedPathname('/', 'srv-1'), '/s/srv-1');
  assert.equal(scopedPathname('/settings', 'srv-1'), '/settings');
});

test('the router rewrite: in strips the server and notes another one; out adds the current server, on Hosted only', () => {
  const kept: unknown[] = [];
  const scope = createServerScope(true, link => kept.push(link));
  const input = (href: string) => scope.rewrite.input({url: new URL(href, 'https://web.test')});
  const output = (href: string) => scope.rewrite.output({url: new URL(href, 'https://web.test')});
  assert.equal(input('/s/srv-2/item/x?tab=details#a')?.pathname, '/item/x');
  assert.equal(scope.requested(), 'srv-2');
  assert.deepEqual(kept, [{serverId: 'srv-2', href: '/item/x?tab=details#a'}], 'kept before anything can redirect');
  assert.equal(output('/item/x'), undefined, 'no prefix before a server is current');
  scope.setCurrent('srv-1');
  assert.equal(output('/item/x?tab=details')?.href, 'https://web.test/s/srv-1/item/x?tab=details');
  assert.equal(output('/settings'), undefined, 'settings stays unscoped');
  scope.setCurrent('srv-2');
  assert.equal(scope.requested(), undefined, 'arriving on the asked server settles it');
  kept.length = 0;input('/s/srv-2/library/l');
  assert.deepEqual(kept, [], 'the current server is never kept as a pending link');
  const local = createServerScope(false);
  local.setCurrent('srv-1');
  assert.equal(local.rewrite.output({url: new URL('/item/x', 'https://own.test')}), undefined, 'a server’s own UI keeps plain paths');
  assert.equal(local.rewrite.input({url: new URL('/s/srv-1/item/x', 'https://own.test')})?.pathname, '/item/x', 'yet it still reads a scoped link');
});

test('a kept link is only ever a valid scoped path, never an arbitrary URL', () => {
  const backing = new Map<string, string>();
  const storage = {getItem: (key: string) => backing.get(key) ?? null, setItem: (key: string, value: string) => void backing.set(key, value), removeItem: (key: string) => void backing.delete(key)};
  const links = createScopedLinkStore(() => storage);
  for (const href of ['https://evil.test/item/x', '//evil.test/item/x', '/sign-in', '/item/x\n', 'item/x']) {links.remember({serverId: 'srv', href});assert.equal(links.peek(), undefined, href);}
  links.remember({serverId: 'srv', href: '/library/l?pivot=books'});
  assert.deepEqual(createScopedLinkStore(() => storage).peek(), {serverId: 'srv', href: '/library/l?pivot=books'}, 'survives a reload in the same tab');
  assert.deepEqual(links.take(), {serverId: 'srv', href: '/library/l?pivot=books'});
  assert.equal(links.peek(), undefined);
  backing.set('portico.web.scoped-link.v1', JSON.stringify({serverId: 'srv', href: 'https://evil.test/'}));
  assert.equal(links.peek(), undefined, 'a tampered value is refused and forgotten');
});

import {createMemoryHistory, createRootRoute, createRoute, createRouter} from '@tanstack/react-router';

test('the real router: a scoped address resolves to the very same route as the plain one, and in-app navigation writes the scoped form', async () => {
  const scope = createServerScope(true);scope.setCurrent('srv-1');
  const root = createRootRoute(), item = createRoute({getParentRoute: () => root, path: '/item/$itemId'}), settings = createRoute({getParentRoute: () => root, path: '/settings'});
  const history = createMemoryHistory({initialEntries: ['/s/srv-1/item/abc']});
  const router = createRouter({routeTree: root.addChildren([item, settings]), history, rewrite: scope.rewrite});
  await router.load();
  assert.equal(router.state.location.pathname, '/item/abc');
  assert.equal(router.state.matches.at(-1)?.routeId, '/item/$itemId');
  assert.equal(router.state.matches.at(-1)?.params.itemId, 'abc');
  // In-app navigation writes the router's public href; under Node the router will not commit, so
  // write it to history as the browser router does, and read it back.
  const next = router.buildLocation({to: '/item/$itemId', params: {itemId: 'def'}});
  assert.equal(next.publicHref, '/s/srv-1/item/def', 'the address bar names the server');
  history.push(next.publicHref);await router.load();
  assert.equal(router.state.location.pathname, '/item/def');assert.equal(router.state.matches.at(-1)?.routeId, '/item/$itemId');
  assert.equal(router.buildLocation({to: '/settings'}).publicHref, '/settings', 'settings stays unscoped');
});

test('O-162: every link the shared builder makes is read back by the Hosted router as the same screen and server', async () => {
  const {SERVER_DESTINATIONS, buildServerLink, destinationLocation, parseServerLinkTarget} = await import('../../packages/client-core/src/server-links.ts');
  const scope = createServerScope(true);
  for (const destination of Object.keys(SERVER_DESTINATIONS) as (keyof typeof SERVER_DESTINATIONS)[]) {
    const spec: {param?: string} = SERVER_DESTINATIONS[destination];
    const params = spec.param ? {[spec.param]: 'id_1'} : {};
    const href = buildServerLink({serverId: 'srv-9', destination, params}, 'https://web.test');
    assert.ok(href, destination);
    const plain = destinationLocation(destination, params)!;
    assert.equal(scope.rewrite.input({url: new URL(href)})?.pathname, plain.pathname, destination);
    assert.equal(scope.requested(), 'srv-9', destination);
    assert.deepEqual(parseServerLinkTarget(buildServerLink({serverId: 'srv-9', destination, params})!), {serverId: 'srv-9', destination, params}, destination);
    assert.equal(parseServerLinkTarget(href), undefined, 'only Portico link hosts are canonical links');
  }
});
