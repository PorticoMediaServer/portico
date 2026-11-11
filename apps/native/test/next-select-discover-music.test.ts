import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

// W-20: DESIGN puts Select in every card's hold-OK menu. The library's Discover
// shelves and the TV music album/artist rows had no selection model to offer, so
// these owners supply one over exactly the entries each screen draws.
const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', role: 'member' as const};
const entry = (id: string, kind = 'movie') => ({id, kind, title: id, libraryId: 'library'} as never);
const libraryState = (queryRevision = 1) => ({library: {id: 'library', name: 'Movies', kind: 'movie'}, tab: {id: 'discover'}, phase: 'ready', queryRevision,
  discover: {phase: 'ready', projection: {scope: {viewerFence: 'viewer-fence'}, revision: {catalog: 7, viewer: 3}}, appended: [
    {id: 'recent', heading: {key: 'recent'}, entries: [entry('one'), entry('two')]},
    {id: 'empty', heading: {key: 'empty'}, entries: []},
    {id: 'continue', heading: {key: 'continue'}, entries: [entry('three', 'show')]},
  ]}}) as never;
const musicState = () => ({albums: [{id: 'album-one'}, {id: 'album-two'}], similar: [entry('similar-album', 'album'), entry('similar-artist', 'artist')]}) as never;
const port = () => ({subscribe: () => () => {}, getSnapshot: () => 0, start() {}, clear: () => true, register: () => () => {}, render: () => null, dispose() {}, item: () => undefined});
const modules = async () => ({
  discover: await screenModule(new URL('../src-next/screens/library/discover-selection.ts', import.meta.url), {react: {default: React, createElement: React.createElement}, '../selection/native-selection': {createNativeSelection: () => port()}}),
  music: await screenModule(new URL('../src-next/screens/destinations/music/selection.tsx', import.meta.url),
    {react: {createElement: React.createElement, Fragment: React.Fragment}, '../../selection/native-selection': {createNativeSelection: () => port()}}),
});

test('a Discover page that is not loaded, or one that left the route, offers no Select at all', async () => {
  const {discover} = await modules();
  const loading = {...(libraryState() as any), discover: {phase: 'loading', appended: []}};
  const source = discover.discoverSelectionSource({subscribe: () => () => {}, getSnapshot: () => loading}, () => true, () => viewer);
  assert.equal(source.getSnapshot().context, undefined);
  const retired = discover.discoverSelectionSource({subscribe: () => () => {}, getSnapshot: () => libraryState()}, () => false, () => viewer);
  assert.equal(retired.getSnapshot().context, undefined, 'a page that left the route cannot join a selection');
});

test('the music page routes a row card to the page row selection and a track row to its own query selection', async () => {
  const created: {options: unknown; item: (entry: unknown, group?: string) => unknown}[] = [];
  const selectionStub = {createNativeSelection: (_context: unknown, _source: unknown, options: unknown) => {
    const item = (row: unknown, group?: string) => ({active: true, selected: false, onToggle() {}, onEnter() {}, tag: `${String((options as {area?: string})?.area)}:${group ?? ''}`});
    created.push({options, item});
    return {...port(), item} as never;
  }};
  const music = await screenModule(new URL('../src-next/screens/destinations/music/selection.tsx', import.meta.url),
    {react: {createElement: React.createElement, Fragment: React.Fragment}, '../../selection/native-selection': selectionStub});
  const page = music.createNativeMusicSelection({session: () => ({viewer}), current: () => true},
    {subscribe: () => () => {}, getSnapshot: () => musicState(), selectionPage: () => ({generation: 1, query: 'songs', revision: 'rev', offset: 0}), contains: () => true},
    'route-key', () => true);
  try {
    assert.equal((page.item(entry('one')) as {tag: string}).tag.endsWith(':'), true, 'a row card asks the page row port');
    assert.equal((page.item(entry('one'), 'songs') as {tag: string}).tag.endsWith(':songs'), true, 'a track row keeps its own query port');
    assert.equal(created.length, 3, 'two track queries plus one row owner');
  } finally {page.dispose();}
});
