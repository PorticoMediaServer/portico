import test from 'node:test';
import assert from 'node:assert/strict';
import {isScopedScreenPath, parseServerLink, parseServerLinkPath, scopedPathname, serverLink} from '../src/server-links.ts';

test('O-162 frozen grammar: a server link names its server and a scoped screen, and nothing else', () => {
  assert.deepEqual(parseServerLinkPath('/s/srv_1/item/abc'), {serverId: 'srv_1', pathname: '/item/abc'});
  assert.deepEqual(parseServerLinkPath('/s/srv_1'), {serverId: 'srv_1', pathname: '/'});
  for (const path of ['/s/srv/sign-in', '/s/srv/account', '/s/srv/servers', '/s/srv/setup', '/s/srv/settings', '/s/../item/x', '/item/x']) assert.equal(parseServerLinkPath(path), undefined, path);
  assert.equal(isScopedScreenPath('/read/b'), true);assert.equal(isScopedScreenPath('/device'), false);
  assert.equal(scopedPathname('/library/l', 'srv_1'), '/s/srv_1/library/l');
});

test('a whole shared link: https on the Portico link hosts only, never credentials or a port', () => {
  assert.deepEqual(parseServerLink('https://web.getportico.tv/s/srv_1/item/abc?tab=details#x'), {serverId: 'srv_1', pathname: '/item/abc', search: '?tab=details', hash: '#x'});
  assert.deepEqual(parseServerLink('https://getportico.tv/s/srv_1/live')?.pathname, '/live');
  for (const href of ['http://web.getportico.tv/s/srv_1/item/a', 'https://evil.test/s/srv_1/item/a', 'https://user:pw@web.getportico.tv/s/srv_1/item/a',
    'https://web.getportico.tv:8443/s/srv_1/item/a', 'https://web.getportico.tv/item/a', 'https://web.getportico.tv/s/srv_1/account', 'not a url'])
    assert.equal(parseServerLink(href), undefined, href);
});

test('the builder writes the one canonical link, and refuses what the parser would refuse', () => {
  assert.equal(serverLink('srv_1', '/item/abc', '?tab=details'), 'https://web.getportico.tv/s/srv_1/item/abc?tab=details');
  assert.equal(serverLink('srv_1', '/'), 'https://web.getportico.tv/s/srv_1');
  assert.equal(serverLink('srv_1', '/settings'), undefined);assert.equal(serverLink('../x', '/item/a'), undefined);
  const built = serverLink('srv_1', '/person/p', 'a=1', 'b')!;
  assert.deepEqual(parseServerLink(built), {serverId: 'srv_1', pathname: '/person/p', search: '?a=1', hash: '#b'}, 'round trip');
});

import {buildInviteLink, buildServerLink, parseInviteLink, parseServerLinkTarget, SERVER_DESTINATIONS, type ServerDestination} from '../src/server-links.ts';

test('every shareable destination round-trips as {serverId, destination, params}', () => {
  const samples: Record<ServerDestination, Record<string, string>> = {home: {}, search: {q: 'dune'}, readingSearch: {q: 'tolkien'}, libraries: {}, library: {libraryId: 'lib_1', pivot: 'books', view: 'browse'},
    live: {sourceId: 'src', group: 'news'}, saved: {resourceId: 'r1'}, item: {itemId: 'i/1'.replace('/', '-')}, homeRow: {rowId: 'continue'}, player: {}, person: {personId: 'p1'},
    read: {itemId: 'b1'}, together: {}, dashboard: {section: 'activity'}, notifications: {}};
  assert.deepEqual(Object.keys(samples).sort(), Object.keys(SERVER_DESTINATIONS).sort(), 'the test covers every destination');
  for (const [destination, params] of Object.entries(samples) as [ServerDestination, Record<string, string>][]) {
    const href = buildServerLink({serverId: 'srv_1', destination, params});
    assert.ok(href?.startsWith('https://web.getportico.tv/s/srv_1'), destination);
    assert.deepEqual(parseServerLinkTarget(href!), {serverId: 'srv_1', destination, params}, destination);
  }
  assert.equal(buildServerLink({serverId: 'srv_1', destination: 'item', params: {itemId: 'abc'}}), 'https://web.getportico.tv/s/srv_1/item/abc');
});

test('no timestamp links, no unsafe or missing path parameter, no other host', () => {
  assert.equal(buildServerLink({serverId: 'srv_1', destination: 'item', params: {itemId: 'abc', t: '120'}}), 'https://web.getportico.tv/s/srv_1/item/abc', 'a t is never written');
  assert.deepEqual(parseServerLinkTarget('https://web.getportico.tv/s/srv_1/item/abc?t=120')?.params, {itemId: 'abc'}, 'nor read');
  assert.equal(buildServerLink({serverId: 'srv_1', destination: 'item', params: {}}), undefined);
  assert.equal(buildServerLink({serverId: '../x', destination: 'home', params: {}}), undefined);
  assert.equal(parseServerLinkTarget('https://evil.test/s/srv_1/item/abc'), undefined);
  assert.equal(parseServerLinkTarget('https://web.getportico.tv/s/srv_1/settings'), undefined);
});

test('invitations keep their existing fragment links, parsed and built by the same module', () => {
  const server = buildInviteLink({kind: 'server-invite', code: 'ABCD-1234', server: 'https://192.168.1.2:32400'})!;
  assert.deepEqual(parseInviteLink(server), {kind: 'server-invite', code: 'ABCD-1234', server: 'https://192.168.1.2:32400'});
  assert.deepEqual(parseInviteLink(buildInviteLink({kind: 'together', code: 'K7Q2'})!), {kind: 'together', code: 'K7Q2'});
  assert.deepEqual(parseInviteLink('https://web.getportico.tv/join#invite=tok_1'), {kind: 'account-invite', invite: 'tok_1'});
  for (const href of ['https://web.getportico.tv/join?code=A&server=B', 'https://web.getportico.tv/join#code=A', 'http://web.getportico.tv/together/join#code=A', 'https://evil.test/join#invite=x'])
    assert.equal(parseInviteLink(href), undefined, href);
});
