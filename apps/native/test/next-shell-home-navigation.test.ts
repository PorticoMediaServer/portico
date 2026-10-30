import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeShellNavigation, channelRoute, titleRoute, nativeDestinationActivation, nativeRailDestination} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';
import type {HomeChannel} from '../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';

const authority: ShellAuthority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};

test('navigation starts on Home with the same destination used by the initial shell route', () => {
  const routes = new NativeShellNavigation(authority, () => authority);
  const initial = routes.getSnapshot();
  assert.deepEqual(initial, {authority, route: {kind: 'shell', destination: {kind: 'home'}}, destination: {kind: 'home'}, revision: 0});
  assert.equal(initial.route.kind, 'shell');
  if (initial.route.kind === 'shell') assert.strictEqual(initial.route.destination, initial.destination);
  assert.equal(routes.back(authority), false);
  assert.strictEqual(routes.getSnapshot(), initial);
});

test('title requests preserve actual aggregate identity and authoritative nested navigation', () => {
  const entry: ContentEntry = {id: 'show', libraryId: 'l', kind: 'show', title: 'show', navigation: {view: 'show', entityId: 'show'}, playback: {itemId: 'episode'}};
  const route = titleRoute(entry);
  assert.equal(route.kind, 'title');
  if (route.kind === 'title') assert.strictEqual(route.entry, entry);
});

test('channel routes keep source and channel identity without manufacturing a catalogue library', () => {
  const item = {source: {id: 'source'}, channel: {id: 'channel', sourceId: 'source'}} as HomeChannel;
  assert.deepEqual(channelRoute(item), {kind: 'channel', sourceId: 'source', channelId: 'channel'});
});

test('a stale viewer cannot navigate, back into, or publish a former private route', () => {
  let current = authority;
  const routes = new NativeShellNavigation(authority, () => current);
  let publications = 0;
  routes.subscribe(() => publications++);
  assert.equal(routes.open({kind: 'profiles', profileId: 'p'}, authority), true);
  current = {...authority, viewerFence: 'revoked'};
  assert.equal(routes.open({kind: 'notifications'}, authority), false);
  assert.equal(routes.back(authority), false);
  assert.equal(publications, 1);
  routes.dispose();
});

test('Back returns through actual requests and disposal invalidates subsequent callers', () => {
  const routes = new NativeShellNavigation(authority, () => authority);
  routes.open({kind: 'servers'}, authority);
  routes.open({kind: 'account'}, authority);
  assert.equal(routes.back(authority), true);
  assert.deepEqual(routes.getSnapshot().route, {kind: 'servers'});
  assert.equal(routes.back(authority), true);
  assert.deepEqual(routes.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
  assert.equal(routes.back(authority), false);
  routes.dispose();
  assert.equal(routes.open({kind: 'account'}, authority), false);
});

test('retained route objects and their actual mounted origin survive a different shell destination', () => {
  let current = authority;
  const routes = new NativeShellNavigation(authority, () => current);
  routes.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  const downloads = {kind: 'downloads'} as const;
  routes.open(downloads, authority);
  routes.open({kind: 'shell', destination: {kind: 'library', libraryId: 'actual-library'}}, authority);
  assert.equal(routes.retainedRoutes().at(-1), downloads, 'a retained controller keeps its genuine descriptor identity');
  assert.strictEqual(routes.retainedRoutes(), routes.retainedRoutes(), 'reads do not manufacture a new history');
  routes.back(authority);
  assert.equal(routes.getSnapshot().route, downloads);
  assert.deepEqual(routes.getSnapshot().destination, {kind: 'saved'}, 'Back restores the origin recorded with this route');
  current = {...authority, epoch: 'retired'};
  assert.deepEqual(routes.retainedRoutes(), []);
  routes.dispose();
});

test('the installed route policy guards direct navigation and Back before publishing a disallowed origin', () => {
  let allow = true;
  const routes = new NativeShellNavigation(authority, () => authority, (route, origin) => allow ||
    origin.kind !== 'library' && !(route.kind === 'title' && route.entry.libraryId === 'restricted'));
  routes.open({kind: 'shell', destination: {kind: 'library', libraryId: 'restricted'}}, authority);
  const title = titleRoute({kind: 'movie', id: 'actual', title: 'Actual title', libraryId: 'restricted'});
  routes.open(title, authority);
  routes.open({kind: 'shell', destination: {kind: 'home'}}, authority);
  allow = false;
  const snapshot = routes.getSnapshot(), retained = routes.retainedRoutes();
  assert.equal(routes.back(authority), false);
  assert.equal(routes.getSnapshot(), snapshot);
  assert.equal(routes.retainedRoutes(), retained, 'a refused Back does not mutate real history');
  assert.equal(routes.open(title, authority), false);
  assert.equal(routes.open({kind: 'shell', destination: {kind: 'library', libraryId: 'restricted'}}, authority), false);
  assert.equal(routes.getSnapshot(), snapshot);
  routes.dispose();
});

for (const destination of [{kind: 'search'}, {kind: 'saved'}, {kind: 'library', libraryId: 'movies'}] as const) test(`actual ${destination.kind} activation distinguishes entry, hidden title, Back restoration and top`, () => {
  const routes = new NativeShellNavigation(authority, () => authority), activation = nativeDestinationActivation(routes, destination);
  const initial = activation.getSnapshot();
  assert.deepEqual(initial, {visible: false, intent: 'enter', revision: 0});
  assert.strictEqual(activation.getSnapshot(), initial);
  const observed: unknown[] = [], unsubscribe = activation.subscribe(() => observed.push(activation.getSnapshot()));
  assert.equal(routes.open({kind: 'shell', destination}, authority), true);
  assert.deepEqual(activation.getSnapshot(), {visible: true, intent: 'enter', revision: 1});
  const title: ContentEntry = {id: 'item', libraryId: 'movies', kind: 'movie', title: 'Actual title'};
  routes.open(titleRoute(title), authority);
  assert.equal(activation.getSnapshot().visible, false);
  assert.strictEqual(routes.getSnapshot().destination, destination, 'title does not replace the mounted origin');
  assert.equal(routes.top(destination, authority), false, 'hidden page cannot receive a scroll-to-top request');
  routes.back(authority);
  assert.deepEqual(activation.getSnapshot(), {visible: true, intent: 'restore', revision: 3});
  assert.equal(routes.top({kind: 'settings'}, authority), false);
  assert.equal(routes.top(destination, authority), true);
  assert.deepEqual(activation.getSnapshot(), {visible: true, intent: 'top', revision: 4});
  assert.equal(observed.length, 4);
  routes.dispose();
  assert.equal(activation.getSnapshot().visible, false);
  assert.equal(observed.length, 5, 'retirement notifies the retained screen');
  assert.equal(routes.top(destination, authority), false);
  unsubscribe();
});

test('activation never admits a different library or retired viewer, and title rail identity does not rewrite history', () => {
  let current = authority;
  const routes = new NativeShellNavigation(authority, () => current), movies = nativeDestinationActivation(routes, {kind: 'library', libraryId: 'movies'});
  routes.open({kind: 'shell', destination: {kind: 'library', libraryId: 'other'}}, authority);
  assert.equal(movies.getSnapshot().visible, false);
  routes.open(titleRoute({id: 'item', libraryId: 'movies', kind: 'movie', title: 'Actual title'}), authority);
  assert.deepEqual(nativeRailDestination(routes.getSnapshot(), [{id: 'movies'}]), {kind: 'library', libraryId: 'movies'});
  assert.deepEqual(nativeRailDestination(routes.getSnapshot(), []), {kind: 'library', libraryId: 'other'});
  routes.back(authority);
  assert.deepEqual(routes.getSnapshot().route, {kind: 'shell', destination: {kind: 'library', libraryId: 'other'}});
  routes.open({kind: 'shell', destination: {kind: 'library', libraryId: 'movies'}}, authority);
  assert.equal(movies.getSnapshot().visible, true);
  current = {...authority, epoch: 'retired'};
  assert.equal(movies.getSnapshot().visible, false);
  assert.equal(routes.top({kind: 'library', libraryId: 'movies'}, authority), false);
  routes.dispose();
});

test('a book route keeps its audiobook library in the rail', () => {
  const routes = new NativeShellNavigation(authority, () => authority);
  routes.open({kind: 'book', entry: {id: 'book-1', libraryId: 'books', kind: 'book', title: 'The Martian'}}, authority);
  assert.deepEqual(nativeRailDestination(routes.getSnapshot(), [{id: 'books'}]), {kind: 'library', libraryId: 'books'});
  assert.deepEqual(nativeRailDestination(routes.getSnapshot(), []), {kind: 'home'});
  routes.dispose();
});
