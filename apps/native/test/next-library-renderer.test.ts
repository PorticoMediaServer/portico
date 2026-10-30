import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {NativeLibraryDestination} from '../src-next/screens/library/model.ts';
import {NativeLibraryDirectory} from '../src-next/screens/library/directory.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import * as readPlatform from '../src-next/screens/read/platform.ts';
import {nativeDestinationActivation} from '../src-next/shell/routes';
import * as selection from '../../../packages/client-core/src/selection-model';
import * as catalogueQuerySelection from '../../../packages/client-core/src/catalogue-query-selection';
import * as browseWholeSelection from '../../../packages/client-core/src/browse-whole-selection';
import * as libraryTabs from '../../../packages/client-core/src/presentation/library-tabs';
import * as libraryOpening from '../src-next/screens/library/opening';
import * as librarySavedViewIntent from '../src-next/screens/library/saved-view-intent';
import {sameAuthority} from '../src-next/shell/model';

test('the Library descriptor requires the actual authorized ID and never supplies a fake selection port', async () => {
  const platform = {isTV: false};
  const {createNativeLibraryPlaySelection} = await screenModule(new URL('../src-next/screens/library/play-selection.ts', import.meta.url), {
    react: {default: React}, '../../../../../packages/client-core/src/selection-model': selection,
    '../../../../../packages/client-core/src/catalogue-query-selection': catalogueQuerySelection,
    '../../../../../packages/client-core/src/browse-whole-selection': browseWholeSelection,
    '../../shell/routes': {nativeDestinationActivation}, '../destinations/lifetime': {nativeDestinationLifetime},
    '../native-screen-services': {nativeScreenServices: () => {throw new Error('a renderer must not construct an uninjected selection binding');}},
  });
  const {createNativeLibraryRenderer, createNativeLibraryPlaySelection: exportedPlay} = await screenModule(new URL('../src-next/screens/library/index.tsx', import.meta.url), {
    react: {default: React}, 'react-native': {Platform: platform}, './LibraryScreen': {LibraryScreen: 'LibraryScreen'}, './model': {NativeLibraryDestination}, './directory': {NativeLibraryDirectory},
    '../../shell/model': {sameAuthority},
    '../read/platform': readPlatform,
    '../read/ReadingLibrary': {NativeReadingLibraryScreen: 'NativeReadingLibraryScreen'},
    '../../../../../packages/client-core/src/presentation/library-tabs': libraryTabs,
    './opening': libraryOpening, './saved-view-intent': librarySavedViewIntent,
    '../destinations/lifetime': {nativeDestinationLifetime},
    './play-selection': {createNativeLibraryPlaySelection},
    // The TV Library module owns only the F-LIB-52 Save view surface; the page
    // and every action stay the real Library destination beneath it.
    './tv-library': {
      createNativeTVLibraryNamingStore: () => ({subscribe: () => () => {}, getSnapshot: () => undefined, open: () => false, close: () => false, dispose: () => {}}),
      nativeTVLibraryActions: (actions: unknown) => actions,
      nativeTVNamingPanelId: (_authority: unknown, libraryId: string) => `tv.${JSON.stringify(libraryId)}.naming`,
      NativeTVLibrarySurface: 'NativeTVLibrarySurface',
    },
  });
  assert.equal(exportedPlay, createNativeLibraryPlaySelection, 'the barrel exposes the actual headless producer');
  const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'session-epoch', viewerFence: 'home-only-fence'};
  let libraries = [{id: 'real-id', name: 'Movies', kind: 'movie'}], current = true;
  const context = {api: {request: async () => {throw new Error('rendering a descriptor must not start a producer');}}, session: () => ({viewer}), authority: () => authority,
    navigation: {getSnapshot: () => ({route: {kind: 'home' as const}, revision: 0}), retainedRoutes: () => []},
    current: () => current, libraries: () => libraries};
  const activation = {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})};
  const renderer = createNativeLibraryRenderer(context, {activation: () => activation, labels: {}, actions: {}});
  try {
    assert.equal(renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'Movies'}}, {}), null, 'a name cannot substitute for the actual ID');
    const element = renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'real-id'}}, {});
    assert.equal(element.props.destination.getSnapshot().library.id, 'real-id'); assert.equal(element.props.selection, undefined);
    assert.equal(element.props.activation, activation); assert.equal(element.props.owner, true);
    libraries = [{id: 'real-id', name: 'Actual renamed library', kind: 'movie'}];
    const renamed = renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'real-id'}}, {});
    assert.equal(renamed.props.destination.getSnapshot().library.name, 'Actual renamed library');
    assert.notEqual(renamed.props.destination, element.props.destination, 'old header/model callbacks retire when the actual library metadata changes');
    assert.equal(element.props.destination.getSnapshot().collection, undefined);
    // Phone reading libraries have no browse pivot: they open the bounded Reading
    // list and retire any media destination that was selected before them.
    for (const [id, name, kind] of [['books', 'Books', 'book'], ['comics', 'Comics', 'comic']] as const) {
      libraries = [{id, name, kind}];
      const reading = renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: id}}, {});
      assert.equal(reading.type, 'NativeReadingLibraryScreen', `${kind} uses the reading renderer`);
      assert.equal(reading.props.libraryId, id); assert.equal(reading.props.title, name);
      assert.equal(reading.props.context, context); assert.equal(typeof reading.props.onBack, 'function');
    }
    libraries = [{id: 'real-id', name: 'Movies', kind: 'movie'}];
    assert.equal(renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'real-id'}}, {}).type, 'LibraryScreen', 'normal media keeps the browse screen');
    platform.isTV = true;
    for (const kind of ['book', 'comic', 'books']) {
      libraries = [{id: 'reading', name: 'Actual reading library', kind}];
      assert.equal(renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'reading'}}, {}), null, `${kind} cannot enter a TV library body`);
    }
    libraries = [{id: 'audio', name: 'Actual audiobooks', kind: 'audiobook'}];
    // TV composes the one Save view surface around the same actual page.
    const tvAudio = renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'audio'}}, {});
    assert.equal(tvAudio.props.panelId, 'tv."audio".naming');
    assert.equal(tvAudio.props.library.props.destination.getSnapshot().library.id, 'audio');
    libraries = []; assert.equal(renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'real-id'}}, {}), null);
    current = false; assert.equal(renderer.render({kind: 'shell', destination: {kind: 'library', libraryId: 'real-id'}}, {}), null);
  } finally {renderer.dispose();}
});
