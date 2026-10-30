import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {createTheme} from '../src-next/kit/theme';
import {nativeSearchEntryKey, nativeSearchTailKey} from '../src-next/screens/search/model';
import {browseType} from '../src-next/kit/browse/presentation';
import {defaultI18n} from '../../../packages/i18n/src';
import {searchGroupLabel} from '../../../packages/client-core/src/presentation/search-model';
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const child = (node: any) => Array.isArray(node.props.children) ? node.props.children[0] : node.props.children;
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
async function fixture() {
  const host = hooks(), registry = createFocusRegistry(), calls: string[] = [], theme = createTheme({isTV: true, width: 1920});
  let boundary: any;
  const {TVSearchSection} = await screenModule(new URL('../src-next/screens/search/TVSearchSection.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
    '../../kit/focus/page-boundary': {useRowPageBoundary: (...args: any[]) => {boundary = args;}},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/browse/presentation': {browseType}, '../../kit/theme': {useTheme: () => theme}, '../../kit/layout/Row': {Row: 'Row'}, '../../tv/layout/TVLoadedGrid': {TVLoadedGrid: 'TVLoadedGrid'},
    './model': {nativeSearchEntryKey, nativeSearchTailKey}, './SearchGridCard': {SearchGridCard: 'SearchGridCard'},
  });
  const items = Array.from({length: 12}, (_, index) => ({id: `served-${index}`, kind: 'movie', libraryId: 'movies', title: 'Stored title'}));
  const props: any = {group: {id: 'movies', status: 'success', items, totalCount: 53, totalCountExact: true, nextCursor: 'offered-cursor'},
    destination: {next: async (group: string) => {calls.push(`next:${group}`);}, retryGroup: async (group: string) => {calls.push(`retry:${group}`);}},
    runtime: {text: defaultI18n, reportError(error: unknown) {throw error;}, error: () => ({title: 'Served failure'})}, actions: {},
    enabled: true, regionId: 'movies', keyboardRegion: 'keyboard-row', previousRegion: 'top', previousFallback: 'top', nextRegion: 'shows',
    screenStart: {regionId: 'keyboard-row', childId: 'l'}, scrollY: 0, viewportHeight: 1080, contentRoot: {current: null}, width: 1044, layoutRevision: 0};
  const render = () => host.render(() => TVSearchSection(props));
  return {host, registry, calls, props, render, boundary: () => boundary};
}
test('the real section exposes all served cells, actual geometry and declarative grid boundaries', async () => {
  const f = await fixture(); const tree = f.render(), grid = nodes(tree).find(node => node.type === 'TVLoadedGrid');
  assert.deepEqual(tree.props.grid, {columns: 4, count: 13});
  assert.deepEqual(tree.props.edges, {left: 'keyboard-row', up: 'top', down: 'shows'});
  assert.deepEqual(tree.props.neighbors, {next: 'shows', previous: 'top'});
  assert.deepEqual(flatten(tree.props.style), {marginTop: 46});
  assert.deepEqual({width: grid.props.itemWidth, gap: grid.props.gap, rowGap: grid.props.rowGap, height: grid.props.itemHeight}, {width: 196, gap: 28, rowGap: 40, height: 390});
  assert.equal(grid.props.items.length, 13, 'twelve served preview items plus their real cursor, with no four-card slice');
  assert.equal(grid.props.singleRow, undefined);
  const card = grid.props.renderItem(grid.props.items[4], {index: 4, top: 430, onFocus() {}});
  assert.deepEqual(card.props.cell, {index: 4, pending: false});
  f.registry.configure({id: tree.props.id, ...tree.props, children: undefined});
  f.registry.attach({regionId: 'movies', childId: card.props.focusId}, {requestTVFocus() {}}, {}, card.props.cell);
  assert.deepEqual(f.registry.destinations({regionId: 'movies', childId: card.props.focusId}).left, {regionId: 'keyboard-row'});
  grid.props.onGeometry(3, 430); assert.equal(f.render().props.grid.columns, 3, 'the measured column count replaces the initial estimate');
});
test('only the genuine offered tail requests a continuation and the failed tail retries through its original owner', async () => {
  const f = await fixture(); let tree = f.render(), grid = nodes(tree).find(node => node.type === 'TVLoadedGrid');
  child(grid.props.renderItem(grid.props.items[0], {index: 0, top: 0, onFocus() {}})).props.onFocus();
  assert.deepEqual(f.calls, []);
  child(grid.props.renderItem(grid.props.items[8], {index: 8, top: 860, onFocus() {}})).props.onFocus();
  assert.deepEqual(f.calls, ['next:movies']);
  const tail = grid.props.renderItem(grid.props.items[12], {index: 12, top: 1290, onFocus() {}});
  assert.deepEqual(tail.props.cell, {index: 12, pending: true});
  assert.equal(child(tail).props.onPress, undefined);
  assert.equal(nodes(tail).find(node => node.type === 'LoadingPaint').props.ready, false);
  f.props.paging = {loading: false, error: {code: 'server_busy', retryable: true}}; tree = f.render(); grid = nodes(tree).find(node => node.type === 'TVLoadedGrid');
  const failed = grid.props.renderItem(grid.props.items[12], {index: 12, top: 1290, onFocus() {}});
  child(failed).props.onPress(); assert.equal(f.calls.at(-1), 'retry:movies');
  assert.equal(f.boundary()[4].failed, true); assert.equal(f.boundary()[3], failed.props.focusId);
});

test('All mode is a windowed horizontal row whose offered cursor fills the original native slot', async () => {
  const f = await fixture(); f.props.singleRow = true;
  const tree = f.render(), row = nodes(tree).find(node => node.type === 'Row');
  assert.ok(row, 'the binding Right-to-next-page sequence uses the shared horizontal row');
  assert.equal(nodes(tree).find(node => node.type === 'TVLoadedGrid'), undefined);
  assert.equal(row.props.items.length, 12, 'every actual preview item is retained');
  assert.equal(row.props.focus.id, 'movies'); assert.deepEqual(row.props.focus.edges, {left: 'keyboard-row', up: 'top', down: 'shows'});
  assert.equal(row.props.paging.hasMore, true);
  assert.equal(row.props.paging.waitingId, JSON.stringify(['search-tail', 'movies', 'offered-cursor']));
  const waiting = row.props.renderItem(row.props.paging.waitingItem, {focusId: 'original-waiting-host', testID: 'actual-host', waiting: true, onFocus() {}, onBlur() {}});
  assert.equal(waiting.props.entry, undefined); assert.equal(waiting.props.focusId, 'original-waiting-host');
  waiting.props.onFocus(); assert.equal(f.calls.at(-1), 'next:movies', 'an interrupted pending slot can request the same actual cursor again after return');
  f.props.group = {...f.props.group, nextCursor: ''};
  f.props.paging = {loading: false, error: null, fulfilled: {waitingId: row.props.paging.waitingId, firstItemId: 'real-next-card'}};
  const filled = nodes(f.render()).find(node => node.type === 'Row');
  assert.equal(filled.props.paging.hasMore, false); assert.deepEqual(filled.props.paging.fulfilled, f.props.paging.fulfilled, 'the final response can still fill its pending host');
});

test('the actual card accepts absent data only under its owned loading slot and forwards the retained native address', async () => {
  const host = hooks(), calls: unknown[] = []; let waiting = true;
  const {SearchGridCard} = await screenModule(new URL('../src-next/screens/search/SearchGridCard.tsx', import.meta.url), {
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame', ArtFrame: 'ArtFrame'},
    '../../kit/browse': {SongRow: 'SongRow'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => ({isTV: false})},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({caption: [], detail: []})},
    react: host.react, 'react-native': {View: 'View'}, '../../kit/focus': {useFocusWaiting: () => waiting},
    '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile'},
    '../home/model': {homeCardText: () => ({})}, './model': {nativeSearchEntryKey},
  });
  const props: any = {group: 'movies', width: 196, focusId: 'original-waiting-host', testID: 'retained-native-id',
    runtime: {text: {}, artwork: () => undefined}, actions: {open: (...args: unknown[]) => calls.push(args), menu() {throw new Error('unexpected menu');}}};
  const pending = host.render(() => SearchGridCard(props));
  assert.equal(pending.type, 'PosterCard'); assert.equal(pending.props.focusId, 'original-waiting-host');
  assert.equal(pending.props.onMore, undefined); pending.props.onNavigate(); assert.deepEqual(calls, []);
  waiting = false; const entry = {id: 'real', kind: 'movie', libraryId: 'movies', title: 'Actual served title'};
  const filled = host.render(() => SearchGridCard({...props, entry}));
  assert.equal(filled.type, pending.type); assert.equal(filled.props.focusId, pending.props.focusId);
  filled.props.onNavigate(); assert.deepEqual(calls, [[entry, 'movies']]);
  assert.throws(() => host.render(() => SearchGridCard(props)), /served entry or an owned waiting slot/);
});
