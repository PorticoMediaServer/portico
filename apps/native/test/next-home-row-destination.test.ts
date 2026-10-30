import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {NativeShellNavigation, titleRoute, nativeEntryRoute, nativeEntryRouteSupported} from '../src-next/shell/routes';
import {NativeHomeRowController} from '../src-next/screens/home/row-controller';
import {NativeShellRouteRegistry} from '../src-next/shell/route-registry';
import {hiddenReadingLibraryOnTV} from '../src-next/screens/read/platform';
import {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure} from '../src-next/screens/home/entry-lifetime';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-fence'};

const rowModule = (isTV = false) => screenModule(new URL('../src-next/screens/home/SeeAllRoute.tsx', import.meta.url), {...stackSlotFixture, 
    '../../kit/useScreenReader': {useScreenReader: () => false},
    react: {...React, default: React}, '../../../../../packages/client-core/src/recommendation-feedback': {}, '../../../../../packages/i18n/src': {}, '../../kit/overlays': {}, '../../kit/controls': {}, 'react-native': {Platform: {isTV}, View: 'View'}, '../../kit/focus': {},
    './HomeSeeAll': {}, './HomeRowFilters': {},
    './row-controller': {NativeHomeRowController}, '../../platform/server-connections': {}, '../../shell/routes': {titleRoute, nativeEntryRoute, nativeEntryRouteSupported},
    '../../shell/route-registry': {nativeShellRoutes: new NativeShellRouteRegistry()}, '../../shell/model': {}, '../../kit/theme': {},
    '../read/platform': {hiddenReadingLibraryOnTV},
    '../../kit/dock/DockClearance': {useDockClearance: () => 0},
    // Menu lifetime is exercised by next-destination-entry-actions; this fixture owns row retention.
    '../destinations/entry-actions': {createNativeEntryActions: () => ({dispose() {}})},
    '../read/reading-books-open': {openNativeReadingBookEntry: async () => false},
    './entry-lifetime': {nativeHomeEntryLifetime, nativeDispatchedPlayLifetime, reportDispatchedPlayFailure},
  });

test('real row outlet retains its exact controller through a title and cancels only pending navigation work', async () => {
  const navigation = new NativeShellNavigation(authority, () => authority), starts: any[] = [], singles: any[] = [];
  const {createNativeHomeRows} = await rowModule();
  let resolve!: () => void;
  const pending = new Promise<void>(done => {resolve = done;});
  const context = {navigation, current: () => true, authority: () => authority, subscribeAuthority: navigation.subscribe, api: {request() {throw new Error('unexpected eager request');}},
    libraries: () => [{id: 'movies', name: 'Actual Movies', kind: 'movie'}], playback: {
      start: async (input: unknown) => {singles.push(input); await pending;},
      runtime: {startGroup: async (input: unknown) => {starts.push(input); await pending;}}}};
  const rows = createNativeHomeRows(context, (error: unknown) => {throw error;});
  const route = {kind: 'home-see-all' as const, destination: {type: 'row' as const, rowId: 'continue'}};
  try {
    navigation.open(route, authority); rows.reconcile();
    const initial = rows.render({})[0], source = initial.props.source;
    assert.ok(source.controller instanceof NativeHomeRowController);
    assert.equal(source.controller.rowId, 'continue');
    const query = {libraryId: '*', pivot: 'items', query: {variant: 'predicate', field: 'homeRow', operator: 'equals', value: 'continue'}, sort: [{field: 'groupOrder', direction: 'asc'}]};
    source.controller.wholeGroup = () => query;
    initial.props.onPlay(true);
    assert.equal(starts.length, 1); assert.strictEqual(starts[0].query, query); assert.equal(starts[0].shuffle, true);
    assert.equal(starts[0].signal.aborted, false);
    const song = {id: 'song', kind: 'song', title: 'Song', libraryId: 'music', playback: {itemId: 'song'}};
    initial.props.onOpen(song, () => true);
    assert.equal(singles.length, 1); assert.equal(singles[0].entry, song);
    initial.props.onPlayEntry({id: 'movie', kind: 'movie', title: 'Actual Movie', libraryId: 'movies', playback: {itemId: 'movie'}}, () => true);
    assert.equal(singles.length, 2); assert.equal(singles[1].lifetime.isCurrent(), true);
    navigation.open(titleRoute({id: 'movie', kind: 'movie', title: 'Actual Movie', libraryId: 'movies'}), authority); rows.reconcile();
    const hidden = rows.render({})[0];
    assert.strictEqual(hidden.props.source, source); assert.equal(hidden.props.active, false);
    assert.equal(starts[0].signal.aborted, true, 'navigation aborts an unfinished queue preparation');
    assert.equal(singles[1].lifetime.signal.aborted, true, 'ordinary entry creation also retires with its real route');
    hidden.props.onPlay(false); assert.equal(starts.length, 1, 'hidden retained callbacks cannot dispatch');
    navigation.back(authority); rows.reconcile();
    const restored = rows.render({})[0];
    assert.strictEqual(restored.props.source.controller, source.controller); assert.equal(restored.props.active, true);
    assert.equal(singles[1].lifetime.isCurrent(), false, 'Back does not revive the old card gesture');
    assert.equal(restored.props.kindLabel('movies'), 'Actual Movies');
    navigation.back(authority); rows.reconcile();
    assert.deepEqual(rows.render({}), []); assert.equal(source.abort.signal.aborted, true);
    initial.props.onPlay(false); assert.equal(starts.length, 1, 'retired opener cannot recreate private work');
  } finally {resolve(); rows.dispose(); navigation.dispose();}
});

for (const isTV of [false, true]) test(`${isTV ? 'TV' : 'handheld'} SeeAll kind choices use actual platform Read eligibility`, async () => {
  const {createNativeHomeRows} = await rowModule(isTV);
  const navigation = new NativeShellNavigation(authority, () => authority);
  const libraries = ['movie', 'book', 'comic', 'books', 'audiobook'].map(kind => ({id: kind, name: kind, kind}));
  const context = {navigation, current: () => true, authority: () => authority, libraries: () => libraries,
    api: {request() {throw new Error('unexpected eager request');}}};
  const rows = createNativeHomeRows(context, (error: unknown) => {throw error;});
  try {
    navigation.open({kind: 'home-see-all', destination: {type: 'row', rowId: 'actual-row'}}, authority);
    rows.reconcile();
    assert.deepEqual(rows.render({})[0].props.source.controller.kinds.map((kind: {id: string}) => kind.id),
      isTV ? ['movie', 'audiobook'] : ['movie', 'book', 'comic', 'books', 'audiobook']);
  } finally {rows.dispose(); navigation.dispose();}
});
