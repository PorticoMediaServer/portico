import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import * as personalSaved from '../../../packages/client-core/src/personal-saved';
import {NativeHomeSavedViewSummaries} from '../src-next/screens/home/saved-view-summaries';
import {nativeSavedRouteTarget} from '../src-next/screens/saved/navigation.ts';
import {exactDurationSeconds, savedMosaicPaths, savedViewConditionCount, savedVisibility} from '../src-next/screens/saved/presentation.ts';
import {linkNativeAbortSignals} from '../src-next/platform/abort-signals';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';

/** The shell has one registration per kind. The phone hub is this family's;
 * the TV landing is the TV-more owner's, so only the routes remain there. */
async function load(isTV: boolean) {
  const host = hooks(), registrations: [string, string][] = [];
  const directoryCommands = await screenModule(new URL('../src-next/screens/saved/directory-commands.ts', import.meta.url), {
    react: host.react, '../../../../../packages/client-core/src/personal-saved': personalSaved,
    '../destinations/lifetime': {nativeDestinationLifetime},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: () => {}})},
    '../../platform/abort-signals': {linkNativeAbortSignals},
  });
  const module = await screenModule(new URL('../src-next/screens/saved/register.tsx', import.meta.url), {...stackSlotFixture, 
    react: host.react,
    'react-native': {Platform: {isTV}, View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../shell/model': {shellLayout: () => ({left: 0, top: 0, width: 390, height: 844, scale: 1})},
    '../../shell/route-registry': {nativeShellRoutes: {
      registerDestination: (kind: string) => {registrations.push(['destination', kind]);},
      registerRoute: (kind: string) => {registrations.push(['route', kind]);},
    }},
    '../../kit/theme': {useTheme: () => ({isTV})},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true, scope: {serverId: 'server', viewerId: 'viewer'}})},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: () => {}})},
    './state': {createNativeSavedState: () => ({source: {}, offline: {}, actions: {}, activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
      view: () => undefined, dispose: () => {}})},
    './SavedScreen': {SavedScreen: 'SavedScreen'}, './SavedDirectoryScreen': {SavedDirectoryScreen: 'SavedDirectoryScreen'},
    './SavedListScreen': {SavedListScreen: 'SavedListScreen'}, './SavedResourceScreen': {SavedResourceScreen: 'SavedResourceScreen'},
    './navigation': {nativeSavedRouteTarget, openNativeSavedView: () => true},
    '../downloads/view': {createNativeDownloadsView: () => ({render: () => null, dispose: () => {}})},
    './selection': {createNativeSavedSelection: () => ({register: () => () => {}, item: () => undefined, render: () => null, dispose: () => {}})},
    '../home/saved-view-summaries': {NativeHomeSavedViewSummaries},
    './directory-commands': directoryCommands,
    './view-model': {}, '../destinations/entry-actions': {}, './view-summary-owner': {},
  });
  return {module, registrations, host};
}

test('phone registers the Saved destination and both pushed Saved routes once', async () => {
  const {module, registrations} = await load(false);
  assert.deepEqual(registrations, [['destination', 'saved'], ['route', 'saved-view'], ['route', 'saved-destination']]);
  const context = {current: () => true, authority: () => ({serverId: 'server'}), navigation: {retainedRoutes: () => [], getSnapshot: () => ({route: {kind: 'shell', destination: {kind: 'saved'}}})}, libraries: () => []};
  const renderer = module.createNativeSavedModule(context);
  const screen = renderer.render({kind: 'shell', destination: {kind: 'saved'}}, {});
  assert.equal(screen.type, 'SavedScreen');
  assert.equal(screen.props.onThisPhone, 'next.saved.phone.onThisPhone');
  assert.equal(renderer.render({kind: 'shell', destination: {kind: 'home'}}, {}), null);
  renderer.dispose();
});

test('TV registers only the pushed Saved routes, leaving the landing to its owner', async () => {
  const {registrations} = await load(true);
  assert.deepEqual(registrations, [['route', 'saved-view'], ['route', 'saved-destination']]);
});

test('a Home destination becomes the same saved playlist or resource route the hub opens', () => {
  assert.deepEqual(nativeSavedRouteTarget({kind: 'saved-destination', destination: {kind: 'playlist', id: 'stored-playlist'}}), {view: 'playlist', playlistId: 'stored-playlist'});
  assert.deepEqual(nativeSavedRouteTarget({kind: 'saved-destination', destination: {kind: 'collection', id: 'stored-collection'}}), {view: 'resource', resourceId: 'stored-collection'});
  assert.deepEqual(nativeSavedRouteTarget({kind: 'saved-view', view: {view: 'views'}}), {view: 'views'});
});

test('Saved facts keep the server counts, published visibility and exact totals', () => {
  assert.deepEqual(savedMosaicPaths(['a', undefined, 'b', 'c', 'd']), ['a', 'b', 'c', 'd']);
  assert.equal(savedVisibility({visibility: 'server'} as never).id, 'server');
  assert.equal(savedVisibility({visibility: 'private'} as never).id, 'private');
  const film = {kind: 'movie', id: 'a', title: 'A', duration: 120} as const, short = {kind: 'movie', id: 'b', title: 'B', duration: 30} as const;
  assert.equal(exactDurationSeconds([film, short]), 150);
  assert.equal(exactDurationSeconds([film, {kind: 'movie', id: 'c', title: 'C'} as never]), undefined, 'an unknown length never becomes a made-up total');
  assert.equal(exactDurationSeconds([]), undefined);
});

test('a saved view reports the conditions it actually applies, never a guessed badge', () => {
  const query = {variant: 'group', all: [{variant: 'predicate', field: 'genre', operator: 'in', value: ['Action']}, {variant: 'group', any: [{variant: 'predicate', field: 'year', operator: 'between', value: [1990, 1999]}, {variant: 'group', not: {variant: 'predicate', field: 'watched', operator: 'equals', value: true}}]}]};
  assert.equal(savedViewConditionCount({definition: {query}} as never), 3);
  assert.equal(savedViewConditionCount({definition: {query: null}} as never), 0);
  assert.equal(savedViewConditionCount({} as never), 0);
});
