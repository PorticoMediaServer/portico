// @fabric-react
// N-PARITY P1 (tv-detail f5, "ranges become groups of 100 episodes"): when an owner sets Ranges or hides seasons, the
// show workspace lists its episodes under groups. Every group must be reachable from the season chooser on phone,
// tablet and TV, as web's group menu does (web/src/screens/detail/Show.tsx), or episodes past the first group are lost.
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';
import {tvEpisodeGroupKey, tvEpisodeRegionMap} from '../src-next/screens/watch/tv-title-focus.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {cardText, episodeLabel} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const groups = [{id: 'range:1', title: 'Episodes 1–100', count: 100}, {id: 'range:101', title: 'Episodes 101–150', count: 50}];

function rangedShow() {
  const paths: URL[] = [];
  const episode = (id: string) => ({id, kind: 'episode', title: id, libraryId: 'library', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id, startMs: 0}});
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); paths.push(url);
    const group = url.searchParams.get('group') ?? 'range:1';
    const entries = group === 'range:101' ? [episode('e101'), episode('e102')] : [episode('e1'), episode('e2')];
    const revision = {catalog: 1, viewer: 1};
    return new Response(JSON.stringify({scope: {serverId: 'server', libraryId: 'library', viewerFence: 'viewer'}, revision,
      show: {id: 'show', libraryId: 'library', title: 'Long show', libraryKind: 'tv', providerMatchStatus: 'matched'}, selected: {seasonId: null, group},
      seasons: [], seasonTotalCount: 0, nextSeasonCursor: '', groups, settings: {ranges: true},
      episodes: {scope: {serverId: 'server', libraryId: 'library', libraryKind: 'tv', viewerFence: 'viewer', view: 'show', entityId: 'show'}, revision,
        query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'}, sections: [{id: group, type: 'list', entries, totalCount: entries.length, nextCursor: ''}]},
    }), {status: 200});
  });
  const controller = new NativeTitleController({api, scope: {serverId: 'server', viewerId: 'viewer'}, entry: {id: 'show', libraryId: 'library', kind: 'show', title: 'Long show'},
    authority, current: () => authority, subscribeCurrent: () => () => {}, requestId: async () => 'request', playback: {start: async () => {}}});
  return {controller, paths};
}

test('a ranged show opens on its first group, and choosing the second reads and shows its episodes', async () => {
  const {controller, paths} = rangedShow();
  try {
    await controller.open();
    const first = controller.getSnapshot();
    assert.equal(first.workspace?.data?.selected.group, 'range:1');
    assert.deepEqual(first.workspace?.data?.groups.map(group => group.id), ['range:1', 'range:101']);
    assert.deepEqual(first.episodes.map(entry => entry.id), ['e1', 'e2']);
    await controller.group('range:101');
    assert.equal(paths.at(-1)?.searchParams.get('group'), 'range:101');
    const second = controller.getSnapshot();
    assert.equal(second.workspace?.data?.selected.group, 'range:101');
    assert.deepEqual(second.episodes.map(entry => entry.id), ['e101', 'e102'], 'episodes past the first hundred are reachable');
    assert.equal(second.place.group, 'range:101', 'the page remembers the group, as it does a season');
    const reads = paths.length;
    await controller.group('range:999');
    assert.equal(paths.length, reads, 'only a group the server named is asked for');
  } finally {controller.dispose();}
});

// ---- Phone and tablet: the season sheet lists the groups after the seasons.
const theme = {isTV: false, platform: 'phone', colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', line: '#222', accent: '#3af', accentStrong: '#4bf'}, type: {}};
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];

test('phone/tablet: the season sheet offers every episode group, marks the open one and selects another', async () => {
  const {NativeSeasonsSheet} = await screenModule(new URL('../src-next/screens/watch/SeasonsSheet.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 400, height: 800})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/theme': {useTheme: () => theme},
  });
  const calls: string[] = [];
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeSeasonsSheet({open: true, onOpenChange: (value: boolean) => calls.push(`open:${value}`), seasons: [], total: 0, selectedId: null,
    onSelect: (id: string) => calls.push(`season:${id}`), groups, selectedGroup: 'range:1', onSelectGroup: (id: string) => calls.push(`group:${id}`),
    hideWatched: false, onHideWatched: () => {}});});
  await host.mount();
  try {
    const rows = descendants(tree).filter(node => node.props.testID?.startsWith('watch-group-'));
    assert.deepEqual(rows.map(row => row.props.testID), ['watch-group-range:1', 'watch-group-range:101']);
    assert.deepEqual(rows.map(row => row.props.accessibilityState.selected), [true, false]);
    assert.ok(rows[0].ref, 'the sheet opens on the open group');
    assert.ok(descendants(rows[1]).some(node => node.type === 'Text' && node.props.children === 'Episodes 101–150'));
    assert.ok(descendants(rows[1]).some(node => node.type === 'Text' && node.props.children === defaultI18n.t('count.episodes', {count: 50})));
    rows[1].props.onPress();
    assert.deepEqual(calls, ['group:range:101', 'open:false']);
  } finally {await host.unmount?.();}
});

// ---- TV: the season side list lists the groups after the seasons; focusing one shows its episodes.
test('TV: the side list offers every episode group; focusing one asks for it once and the row waits for its episodes', async () => {
  const tvTheme = {...theme, isTV: true, platform: 'tv'};
  const {TVEpisodeBrowser} = await screenModule(new URL('../src-next/screens/watch/TVEpisodeBrowser.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText, episodeLabel},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
    '../../../../../packages/i18n/src': {defaultI18n},
    './tv-title-focus': {tvEpisodeGroupKey, tvEpisodeRegionMap}, '../../kit/focus': {FocusRegion: 'FocusRegion'},
    '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: {id: string}) => entry.id},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/cards': {EpisodeTile: 'EpisodeTile'},
    '../../kit/overlays': {Menu: 'Menu'}, '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number) => ({fontSize: size})},
    '../../kit/theme': {useTheme: () => tvTheme},
  });
  const entry = (id: string, number: number) => ({id, kind: 'episode', title: id, libraryId: 'library', absoluteNumber: number, duration: 1440});
  const calls: string[] = [];
  let props: any = {seasons: [], selectedSeasonId: null, groups, selectedGroup: 'range:1', episodes: [entry('e1', 1), entry('e2', 2)], show: {title: 'Long show'},
    open: {phase: 'idle'}, text: {t: defaultI18n.t}, artwork: () => undefined, more: () => {},
    onSeasonFocus: () => calls.push('season-focus'), onEpisodeFocus: () => {}, onSelectSeason: (id: string) => calls.push(`season:${id}`),
    onSelectGroup: (id: string) => calls.push(`group:${id}`), onPlayEpisode: () => {}, onPlaySeason: () => {}, onMarkSeason: () => {},
    onMoreSeasons: () => {}, onMoreEpisodes: () => {}, onRetryEpisode: () => {}, renderError: () => null};
  let tree: any;
  const host = createFabricHookHost(() => {tree = TVEpisodeBrowser(props);});
  await host.mount();
  try {
    let nodes = descendants(tree);
    const list = nodes.find(node => node.type === 'FocusRegion' && node.props.id === 'tv.episodes.seasons');
    assert.deepEqual(list.props.order, ['group:range:1', 'group:range:101'], 'the groups are in the list\'s focus order');
    assert.equal(list.props.entry, 'group:range:1', 'D-pad entry lands on the open group');
    assert.equal(nodes.find(node => node.type === 'TVShelf').props.focus.id, 'tv.episodes.season."group:range:1"', 'each group has its own episode row, so its focus memory is its own');
    const rows = nodes.filter(node => node.props.testID?.startsWith('tv-group-'));
    assert.deepEqual(rows.map(node => node.props.testID), ['tv-group-range:1', 'tv-group-range:101']);
    assert.deepEqual(rows.map(node => node.props.accessibilityState.selected), [true, false]);
    rows[1].props.onFocus();
    await host.rerender();
    assert.deepEqual(calls, ['season-focus', 'group:range:101']);
    nodes = descendants(tree);
    assert.deepEqual(nodes.find(node => node.type === 'TVShelf').props.items, [], 'the row waits: it never shows the previous group\'s episodes as this one\'s');
    assert.ok(nodes.some(node => node.type === 'Text' && node.props.children === 'Episodes 101–150'), 'the focus band names the focused group');
    rows[1].props.onPress();
    assert.deepEqual(calls, ['season-focus', 'group:range:101', 'season-focus'], 'OK on the same group asks nothing more');
    props = {...props, requestedGroup: 'range:101'};
    await host.rerender();
    assert.deepEqual(descendants(tree).find(node => node.type === 'TVShelf').props.items, [], 'still loading');
    props = {...props, requestedGroup: undefined, selectedGroup: 'range:101', episodes: [entry('e101', 101)]};
    await host.rerender();
    nodes = descendants(tree);
    assert.deepEqual(nodes.find(node => node.type === 'TVShelf').props.items.map((value: any) => value.id), ['e101'], 'the second group\'s episodes');
    assert.equal(nodes.find(node => node.type === 'TVShelf').props.focus.id, 'tv.episodes.season."group:range:101"');
  } finally {await host.unmount?.();}
});

test('TV: a seasonal show keeps its season rows and keys (no groups, no change)', () => {
  const regions = tvEpisodeRegionMap({id: 'tv.episodes'}, 's2', ['e1'], undefined);
  assert.equal(regions.row.id, 'tv.episodes.season."s2"');
  assert.equal(regions.seasons.entry, 's2');
  assert.equal(tvEpisodeGroupKey('range:101'), 'group:range:101');
});
