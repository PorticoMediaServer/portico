// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import {authorityKey, sameAuthority} from '../src-next/shell/model.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};

test('actual collection presenter sends its current member through the fenced primary playback', async () => {
  const song = {id: 'song', libraryId: 'library', kind: 'song', title: 'Song', playback: {itemId: 'song'}};
  let entries: any[] = [song]; const listeners = new Set<() => void>(), starts: any[] = [];
  class Controller {
    subscribe = (fn: () => void) => {listeners.add(fn); return () => listeners.delete(fn);};
    snapshot = {entries};
    getSnapshot = () => this.snapshot.entries === entries ? this.snapshot : (this.snapshot = {entries}); open = async () => {}; dispose() {};
    contains = (entry: unknown) => entries.includes(entry); resident = () => ({entries, positions: entries.map((_, index) => index)});
  }
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {...React, default: React}, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async () => false},
    '../native-screen-services': {nativeScreenServices: () => ({navigate() {assert.fail('song cannot route to title');}, reportError: assert.fail})},
    '../watch': {nativeWatchEntryMenu: () => undefined},
  });
  const {createNativeCollectionRenderer} = await screenModule(new URL('../src-next/screens/collections/register.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...React, default: React}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}}, '../../shell/model': {authorityKey, sameAuthority},
    'react-native': {View: 'View'}, '../../kit/focus': {useFocusRegistry: () => ({capture: () => undefined, subscribe: () => () => {}, retain: () => () => {}, restore() {}})},
    '../selection/native-selection': {catalogueSelectionSource: (source: unknown) => source, createNativeSelection: () => ({dispose() {}})},
    '../destinations/entry-actions': {createNativeEntryActions}, './controller': {NativeCollectionController: Controller}, './CollectionScreen': {NativeCollectionScreen: 'CollectionScreen'},
  });
  const navigation = new NativeShellNavigation(authority, () => authority);
  const route = {kind: 'collection' as const, libraryId: 'library', collectionId: 'collection', title: 'Collection'};
  navigation.open(route, authority);
  let resolve!: () => void;
  const context = {navigation, authority: () => authority, current: () => true, subscribeAuthority: navigation.subscribe,
    session: () => ({viewer: {...authority, authority: 'local'}}), api: {}, playback: {start: (input: any) => {starts.push(input); return new Promise<void>(done => {resolve = done;});}}};
  // The page is retained (it stays mounted under a title opened from it): its route renders
  // nothing and the retained outlet draws the real host.
  const renderer = createNativeCollectionRenderer(context), runtime = {reportError: assert.fail};
  assert.equal(renderer.render(route, runtime), null);
  let tree: any; const host = createFabricHookHost(() => {
    const outlet = renderer.renderRetained(runtime), pages = outlet.type(outlet.props);
    const page = React.Children.toArray(pages.props.children)[0] as any, view = page.type(page.props);
    assert.equal(page.props.active, true);
    tree = view.props.children.type(view.props.children.props);
  }); await host.mount();
  try {
    tree.props.onOpen(song); assert.equal(starts.length, 1); assert.equal(starts[0].target, song.playback);
    entries = []; for (const notify of listeners) notify();
    assert.equal(starts[0].lifetime.signal.aborted, true);
    tree.props.onOpen(song); assert.equal(starts.length, 1);
  } finally {resolve(); await host.unmount(); navigation.dispose();}
});
