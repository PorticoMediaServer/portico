import assert from 'node:assert/strict';
import test from 'node:test';
import {nativeReadRouteAllowed} from '../src-next/screens/read/platform';
import {NativeShellNavigation, type NativeShellRoute} from '../src-next/shell/routes';

const libraries = [{id: 'book', name: 'Read', kind: 'book'}, {id: 'comic', name: 'Comics', kind: 'comic'}, {id: 'audio', name: 'Listen', kind: 'audiobook'}];
test('TV direct library and title registration cannot admit Read or a removed origin', () => {
  for (const id of ['book', 'comic', 'removed']) {
    const routes: NativeShellRoute[] = [{kind: 'shell', destination: {kind: 'library', libraryId: id}},
      {kind: 'title', entry: {id: 'title', title: 'Unit title', kind: 'book', libraryId: id}}];
    for (const route of routes) {
      assert.equal(nativeReadRouteAllowed(route, libraries, true), false, id);
      assert.equal(nativeReadRouteAllowed(route, libraries, false), true, id);
    }
  }
  assert.equal(nativeReadRouteAllowed({kind: 'title', entry: {id: 'unknown', title: 'Unit title', kind: 'book'}}, libraries, true), false);
});
test('TV retains audiobooks and ordinary destinations without guessing from book item kind', () => {
  assert.equal(nativeReadRouteAllowed({kind: 'title', entry: {id: 'audio-title', title: 'Unit title', kind: 'book', libraryId: 'audio'}}, libraries, true), true);
  assert.equal(nativeReadRouteAllowed({kind: 'shell', destination: {kind: 'home'}}, libraries, true), true);
  assert.equal(nativeReadRouteAllowed({kind: 'account'}, libraries, true), true);
});
test('a retained Read origin cannot reappear under a registered title and can leave normally', () => {
  const title: NativeShellRoute = {kind: 'title', entry: {id: 'audio-title', title: 'Unit title', kind: 'book', libraryId: 'audio'}};
  for (const libraryId of ['book', 'comic', 'removed']) {
    const origin = {kind: 'library' as const, libraryId};
    assert.equal(nativeReadRouteAllowed(title, libraries, true, origin), false);
    assert.equal(nativeReadRouteAllowed({kind: 'shell', destination: {kind: 'libraries'}}, libraries, true, origin), true);
    assert.equal(nativeReadRouteAllowed({kind: 'shell', destination: {kind: 'home'}}, libraries, true, origin), true);
  }
});
test('actual navigation admission rejects direct opens and stale Back before mutating history', () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  let published = libraries;
  const navigation = new NativeShellNavigation(authority, () => authority,
    (route, origin) => nativeReadRouteAllowed(route, published, true, origin));
  const first = navigation.getSnapshot(), retained = navigation.retainedRoutes();
  assert.equal(navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'book'}}, authority), false);
  assert.strictEqual(navigation.getSnapshot(), first); assert.strictEqual(navigation.retainedRoutes(), retained);
  assert.equal(navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'audio'}}, authority), true);
  assert.equal(navigation.open({kind: 'title', entry: {id: 'audio-title', title: 'Unit title', kind: 'book', libraryId: 'audio'}}, authority), true);
  assert.equal(navigation.open({kind: 'account'}, authority), true);
  published = libraries.map(library => library.id === 'audio' ? {...library, kind: 'book'} : library);
  const beforeBack = navigation.getSnapshot(), beforeHistory = navigation.retainedRoutes();
  assert.equal(navigation.back(authority), false);
  assert.strictEqual(navigation.getSnapshot(), beforeBack); assert.strictEqual(navigation.retainedRoutes(), beforeHistory);
  assert.equal(navigation.open({kind: 'shell', destination: {kind: 'libraries'}}, authority), true);
  navigation.dispose();
});

test('a dedicated Read descriptor is never admitted on TV, including an audiobook origin', () => {
  const route: NativeShellRoute = {kind: 'read', itemId: 'actual-item', sourceId: 'actual-source'};
  assert.equal(nativeReadRouteAllowed(route, libraries, true, {kind: 'library', libraryId: 'audio'}), false);
  assert.equal(nativeReadRouteAllowed(route, libraries, false), true);
});
