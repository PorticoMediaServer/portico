import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {createNativeSearchGenreController, nativeSearchRememberHistory, openNativeSearchGenre, openNativeSearchGenres} from '../src-next/screens/search/navigation';

function preferences(value?: boolean, fallback = false) {
  return parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'actual-registry', registry: {revision: 'actual-registry', fields: [
    {key: 'search.rememberHistory', type: 'boolean', default: fallback, group: 'privacy', labelKey: 'preferences.search.rememberHistory', scopes: ['profile-server']},
  ]}, documents: [
    {scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: value === undefined ? {} : {'search.rememberHistory': value}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}},
  ], effective: {'search.rememberHistory': value ?? fallback}, effectiveSource: {'search.rememberHistory': value === undefined ? 'default' : 'profile-server'}, clampedFields: []});
}

test('Search history reads the confirmed effective value or published default without a private fallback', () => {
  let current = true, snapshot = preferences();
  const context = {current: () => current, preferences: () => snapshot};
  assert.equal(nativeSearchRememberHistory(context), false);
  snapshot = preferences(undefined, true); assert.equal(nativeSearchRememberHistory(context), true);
  snapshot = preferences(false, true); assert.equal(nativeSearchRememberHistory(context), false);
  snapshot = {...snapshot, registry: {...snapshot.registry, fields: []}, effective: {}};
  assert.throws(() => nativeSearchRememberHistory(context), /does not publish/);
  current = false; assert.equal(nativeSearchRememberHistory(context), false);
});

test('genre navigation retains its exact source through Title and Back, then retires it permanently when history drops it', () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-only'};
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  const session: LocalSession = {viewer: {...authority, authority: 'local', role: 'member'}, accessToken: '', expiresAt: '2026-10-25T00:00:00Z',
    authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'fixture-family', tokenGeneration: '1'};
  const unavailable = (): never => {throw new Error('unexpected_headless_genre_port');};
  const context: NativeShellBootstrapContext = {
    current: () => true, session: () => session, authority: () => authority, navigation,
    api: new HttpLocalApi('https://fixture.invalid', '', async () => unavailable()),
    subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    get home() {return unavailable();}, get events() {return unavailable();}, preferences: unavailable, subscribePreferences: unavailable,
    get appVersion() {return unavailable();}, get serverName() {return unavailable();}, get playback() {return unavailable();},
    get watch() {return unavailable();}, get account() {return unavailable();}, libraries: unavailable,
  };
  navigation.open({kind: 'shell', destination: {kind: 'search'}}, authority);
  const genre = {id: 'stored/value', name: 'Stored metadata label', count: 7, coverPaths: []};
  assert.equal(openNativeSearchGenre(context, genre), true);
  const route = navigation.getSnapshot().route; assert.equal(route.kind, 'search-genre');
  if (route.kind !== 'search-genre') throw new Error('expected actual genre route');
  assert.equal(route.value, genre.id); assert.equal(route.label, genre.name); assert.equal(Object.isFrozen(route), true);
  const controller = createNativeSearchGenreController(context, route);
  const active = controller.activation.getSnapshot(); assert.equal(active.visible, true); assert.equal(controller.activation.getSnapshot(), active);
  controller.source.remember(0, 17.5);
  navigation.open({kind: 'title', entry: {id: 'actual-film', kind: 'movie', libraryId: 'actual-library', title: 'Actual title'}}, authority);
  assert.equal(controller.activation.getSnapshot().visible, false); assert.equal(controller.source.getMemory().offset, 17.5);
  navigation.back(authority); assert.equal(navigation.getSnapshot().route, route);
  assert.equal(controller.activation.getSnapshot().visible, true); assert.equal(controller.activation.getSnapshot().intent, 'restore');
  navigation.back(authority); assert.equal(controller.source.getMemory().offset, 0); assert.equal(listeners.size, 0);
  navigation.open(route, authority); assert.equal(controller.activation.getSnapshot().visible, false, 'an old captured owner cannot be revived');
  assert.equal(openNativeSearchGenres(context), true); assert.equal(navigation.getSnapshot().route.kind, 'search-genres');
  controller.dispose(); navigation.dispose();
});
