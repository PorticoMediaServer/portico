// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {isFirstScan, observeFirstScan} from '../src-next/screens/library/first-scan.ts';

const react = {...React, default: React};
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const text = (node: any): string => Array.isArray(node) ? node.map(text).join('') : typeof node === 'string' ? node : node?.props ? text(node.props.children) : '';
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

// ---- Library, first scan (states.html #15) ----

const job = (id: string, status = 'running') => ({id, status, libraryId: 'films', actions: []});
const managed = (lastScan: unknown) => ({phase: 'ready', data: {library: {id: 'films', lastScan}}});
const history = (...items: any[]) => ({phase: 'ready', data: {items, nextCursor: ''}});

test('a first scan is the owner library whose one complete job is its own queued or running scan', () => {
  const snapshot = (selected: any, jobs: any) => ({selected, jobs}) as any;
  assert.equal(isFirstScan(snapshot(managed(job('a')), history(job('a'))), 'films'), true);
  assert.equal(isFirstScan(snapshot(managed(job('a', 'queued')), history(job('a', 'queued'))), 'films'), true);
  assert.equal(isFirstScan(snapshot(managed(job('a', 'succeeded')), history(job('a', 'succeeded'))), 'films'), false, 'a finished scan is not a first scan');
  assert.equal(isFirstScan(snapshot(managed(job('b')), history(job('a'), job('b'))), 'films'), false, 'earlier history means it was scanned before');
  assert.equal(isFirstScan(snapshot(managed(job('a')), {phase: 'ready', data: {items: [job('a')], nextCursor: 'more'}}), 'films'), false, 'an incomplete history proves nothing');
  assert.equal(isFirstScan(snapshot(managed(job('a')), history(job('a'))), 'other'), false, 'another library');
  assert.equal(isFirstScan(snapshot(managed(null), history()), 'films'), false);
  assert.equal(isFirstScan(snapshot({phase: 'loading', data: null}, history(job('a'))), 'films'), false);
});

test('a failed management read is simply not a first scan, and stopping silences the observer', async () => {
  const seen: boolean[] = [];
  const api = {request: async () => {throw Object.assign(new Error('refused'), {code: 'access_refused'});}} as any;
  const stop = observeFirstScan({api, scope: {serverId: 's', viewerId: 'v'}}, 'films', value => seen.push(value));
  await tick(); await tick();
  assert.ok(seen.every(value => value === false), 'never a first scan');
  stop(); seen.length = 0; await tick();
  assert.deepEqual(seen, []);
});

async function emptyPage(isTV: boolean) {
  const theme = createTheme({width: isTV ? 1920 : 390, isTV}), registry = {};
  const modules = {
    react,
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: theme.isTV ? 1920 : 390, height: 844}),
      StyleSheet: {create: (styles: unknown) => styles, absoluteFill: {}}},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: unknown) => ({value}), withTiming: (value: unknown) => value},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id, number: (value: number) => String(value), has: () => true}},
    '../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: () => ({})},
    '../../../../../packages/client-core/src/presentation/library-tabs': {libraryTabs: () => []},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: () => ''},
    '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt'},
    '../../kit/controls': {Focusable: 'Focusable', Button: 'Button'},
    '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
    '../../kit/browse': {}, '../../kit/overlays': {Menu: 'Menu'},
    '../../kit/browse/presentation': {alpha: (color: string) => color, browseType: () => ({})}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/layout': {WindowedGrid: 'WindowedGrid', AlphabetRail: 'AlphabetRail', Row: 'Row'},
    '../../../../../packages/client-core/src/presentation/card-shape': await import('../../../packages/client-core/src/presentation/card-shape.ts'), './discover-cells': {discoverCell: () => ({})}, '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0},
    '../../kit/states': {StateView: 'StateView'}, '../../kit/theme': {useTheme: () => theme}, '../../kit/useMotion': {useMotion: () => ({})},
    '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})}, '../home/model': {homeCardText: () => ({})},
    '../destinations/focus': {createDestinationEntryFocus: () => ({}), nativeDestinationFocusIds: () => ({rail: 'rail'})},
    '../destinations/useTVPageBack': {useTVPageBack: () => {}}, '../destinations/useEntryFocus': {useDestinationEntryFocus: () => {}},
    './model': {NativeLibraryDestination: 'NativeLibraryDestination', nativeLibraryEntryKey: (entry: any) => entry.id, nativeLibraryCanSave: () => true},
    '@portico/design': {frameSpec: {browse: {listRow: {phone: 76, medium: 76, expanded: 76, tv: 84}}}}, '../../kit/browse/LibraryCard': {LibraryCard: 'LibraryCard'},
    '../../pages/shared/library/useFirstScanning': await screenModule(new URL('../src-next/pages/shared/library/useFirstScanning.ts', import.meta.url), {react}),
    './LibraryToolbar': {LibraryToolbar: 'LibraryToolbar'}, '../../kit/browse/LibraryListRow': {LibraryListRow: 'LibraryListRow'}, './directory': {NativeLibraryDirectory: 'NativeLibraryDirectory'},
    '../../kit/browse/LibraryFilterFields': {useLibraryFrameFilters: () => undefined},
    './LibraryFilters': {LibraryFilters: 'LibraryFilters', libraryFiltersDockWidth: 380}, '../../kit/frames': {BrowseFrame: 'BrowseFrame'}, './applied-filters': {libraryAppliedFilters: () => []}, './library-grid': {libraryTabletGridGeometry: () => ({})},
    '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'}, '../destinations/music/TVMusicDiscover': {TVMusicDiscover: 'TVMusicDiscover'},
    '../read/platform': {hiddenReadingLibraryOnTV: () => false, isReadingLibraryKind: () => false}, '../share/share-actions': {shareLinkActions: () => []},
    '../../kit/browse/LibraryJumpSheet': {}, './jump': {}, '../../kit/cards/TVSparseCardFrame': {}, '../../platform/home-action-diagnostics': {homeActionDiagnostics: () => {}},
  };
  const {LibraryEmpty, useFirstScanning} = await screenModule(new URL('../src-next/screens/library/LibraryScreen.tsx', import.meta.url), modules);
  return {LibraryEmpty, useFirstScanning};
}

async function mountEmpty(isTV: boolean, props: Record<string, unknown>) {
  const {LibraryEmpty} = await emptyPage(isTV);
  const home: unknown[] = [];
  let tree: any;
  const host = createFabricHookHost(() => {tree = LibraryEmpty({runtime: {getSnapshot: () => ({authority: {}}), onDestination: (value: unknown) => home.push(value)}, regionId: 'region', upRegionId: 'tabs',
    surface: 'grid', libraryName: 'Films', ...props});});
  await host.mount();
  return {host, home, tree: () => tree};
}
const buttons = (tree: any) => descendants(tree).filter(node => node.type === 'Button');

for (const isTV of [false, true]) {
  test(`${isTV ? 'TV' : 'phone'}: a library on its first scan draws only View activity, and the page entry is that action`, async () => {
    const opened: string[] = [];
    const page = await mountEmpty(isTV, {firstScanning: true, onActivity: () => opened.push('activity')});
    try {
      const state = descendants(page.tree()).find(node => node.props.testID === 'library-empty');
      assert.equal(state.props.title, 'next.states.firstScan');
      assert.equal(state.props.body, 'next.states.firstScanBody');
      const actions = buttons(page.tree());
      assert.deepEqual(actions.map(node => node.props.testID), ['library-empty-view-activity'], 'states.html #15 draws View activity alone: no Browse home');
      assert.equal(text(actions[0]), 'next.states.viewActivity');
      assert.equal(actions[0]!.props.variant, 'glass', 'the frame draws a glass button');
      actions[0]!.props.onPress();
      assert.deepEqual(opened, ['activity']);
      const region = descendants(page.tree()).find(node => node.type === 'FocusRegion');
      assert.equal(region.props.entry, 'view-activity');
      assert.deepEqual(region.props.order, ['view-activity']);
    } finally {await page.host.unmount();}
  });
}

test('a member, or a scan not yet known, keeps the generic empty state with Browse home as the entry', async () => {
  for (const props of [{}, {firstScanning: true}, {firstScanning: false, onActivity: () => {}}]) {
    const page = await mountEmpty(false, props);
    try {
      assert.equal(descendants(page.tree()).find(node => node.props.testID === 'library-empty').props.title, 'library.emptyLibrary');
      assert.deepEqual(buttons(page.tree()).map(node => node.props.testID), ['library-empty-browse-home']);
      assert.equal(descendants(page.tree()).find(node => node.type === 'FocusRegion').props.entry, 'browse-home');
    } finally {await page.host.unmount();}
  }
});

test('the first-scan read is made once while the titles page is empty, follows the answer and is released', async () => {
  const {useFirstScanning} = await emptyPage(false);
  const observed: {id: string; onChange: (value: boolean) => void; stopped: boolean}[] = [];
  const port = {observe: (id: string, onChange: (value: boolean) => void) => {const entry = {id, onChange, stopped: false}; observed.push(entry); return () => {entry.stopped = true;};}, openActivity: () => {}};
  let enabled = true, scanning: boolean | undefined;
  const host = createFabricHookHost(() => {scanning = useFirstScanning(port, 'films', enabled);});
  await host.mount();
  try {
    assert.equal(observed.length, 1);
    assert.equal(observed[0]!.id, 'films');
    assert.equal(scanning, false, 'not a first scan until the read says so');
    observed[0]!.onChange(true); await host.rerender();
    assert.equal(scanning, true);
    enabled = false; await host.rerender();
    assert.equal(scanning, false, 'a genres page, or a page with titles, is never a first scan');
    assert.equal(observed[0]!.stopped, true);
    assert.equal(observed.length, 1, 'nothing is read while disabled');
  } finally {await host.unmount();}
  const member = {current: [] as string[]};
  const hostMember = createFabricHookHost(() => {member.current.push(String(useFirstScanning(undefined, 'films', true)));});
  await hostMember.mount();
  try {assert.deepEqual(member.current, ['false'], 'a member has no port and reads nothing');} finally {await hostMember.unmount();}
});
