import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

async function page(view: Record<string, unknown>) {
  const calls: string[] = [];
  const {GenreBrowsePage} = await screenModule(new URL('../src-next/pages/touch/search/GenreBrowsePage.tsx', import.meta.url), {
    react: {...React, default: React}, '../../../kit/browse': {BrowseToolbar: 'BrowseToolbar'}, '../../../kit/browse/HomeRowCard': {HomeRowCard: 'HomeRowCard'},
    '../../../kit/frames': {BrowseFrame: 'BrowseFrame'}, '../../../kit/frames/BrowseGrid': {BrowseGrid: 'BrowseGrid'}, '../../../kit/states': {StateView: 'StateView'},
    '../../../screens/library/model': {nativeLibraryEntryKey: (entry: any) => entry.id}, '../../../screens/search/GenreFilters': {GenreFilters: 'GenreFilters'},
    '../../shared/search/useGenreBrowse': {useGenreBrowse: () => view},
  });
  const actions = {open: () => calls.push('open'), play: () => calls.push('play'), menu: () => calls.push('menu')};
  return {tree: GenreBrowsePage({name: 'Drama', runtime: {appearanceCardSizePercent: 100}, actions, onBack: () => calls.push('back')}), calls};
}

const ready = {t: (key: string) => key, ready: true, model: {quick: []}, labels: {}, total: 42, collection: {}, filters: undefined, state: {},
  openFilters() {}, onSort() {}, reshuffle() {}, onQuickChange() {}, elapsed: 0};

test('a genre from Search is the kit browse page: "‹ Search", its name, the toolbar with the count, and the library\'s poster grid', async () => {
  const {tree, calls} = await page(ready);
  const frame = (tree.props.children as any[]).find((node: any) => node?.type === 'BrowseFrame');
  assert.equal(frame.props.header.title, 'Drama'); assert.equal(frame.props.header.back.label, 'web.shell.search');
  frame.props.header.back.onPress(); assert.deepEqual(calls, ['back']);
  assert.equal(frame.props.state, undefined);
  assert.equal(frame.props.toolbar.props.resultTotal, 42);
  const grid = frame.props.grid; assert.equal(grid.type, 'BrowseGrid'); assert.equal(grid.props.shape, 'poster');
  const card = grid.props.renderItem({id: 'e1'}, {onFocus() {}, onBlur() {}}, 120);
  card.props.onOpen(); card.props.onPlay(); assert.deepEqual(calls, ['back', 'open', 'play']);
});

test('until it is ready the page loads in place, and a failed read takes the page', async () => {
  const loading = (await page({...ready, ready: false, model: undefined, collection: undefined})).tree;
  assert.equal((loading.props.children as any[])[0].props.state.kind, 'loading');
  const failure = {icon: 'i-warn', title: 'Something went wrong'};
  assert.equal(((await page({...ready, failure})).tree.props.children as any[])[0].props.state, failure);
});
