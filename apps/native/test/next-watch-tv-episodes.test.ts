// @fabric-react
import {tvEpisodeRegionMap} from '../src-next/screens/watch/tv-title-focus.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {cardText, episodeLabel} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';

const themed = {useTheme: () => ({isTV: true, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111', line: '#222', accent: '#3af', accentStrong: '#4bf'}, type: {}}), textLines: (count: number) => ({numberOfLines: count, ellipsizeMode: 'tail' as const})};
const {TVEpisodeBrowser, TVTile, FocusBand, seasonListEdges, seasonListHeight} = await screenModule(new URL('../src-next/kit/frames/TVEpisodeBrowser.tsx', import.meta.url), {
  react: {...React, default: React},
  'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
  '../../../../../packages/client-core/src/presentation/kit-model': {cardText, episodeLabel},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
  '../../../../../packages/i18n/src': {defaultI18n},
  '../../screens/watch/tv-title-focus': {tvEpisodeRegionMap}, '../../kit/focus': {FocusRegion: 'FocusRegion'},
  '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: {id: string}) => entry.id},
  '../../kit/controls': {Focusable: 'Focusable', KeyHints: 'KeyHints'}, '../../kit/frames/title-parts': {SeasonListRow: 'SeasonListRow'}, '../../kit/cards': {EpisodeTile: 'EpisodeTile'},
  '../../kit/overlays': {Menu: 'Menu'}, '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number) => ({fontSize: size})},
  '../../kit/theme': themed,
});
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const text = (node: any): string => Array.isArray(node?.props?.children) ? node.props.children.filter((part: any) => typeof part === 'string').join('') : typeof node?.props?.children === 'string' ? node.props.children : '';
const entry = (id: string, number: number, progressSeconds = 0) => ({id, kind: 'episode', title: id === 'e2' ? 'Bit by a Dead Bee' : id, libraryId: 'library', seasonNumber: 2, episodeNumber: number, duration: 2820, progressSeconds, airDate: '2009-03-22'});
const episodes = [entry('e1', 1), entry('e2', 3, 720), entry('e3', 4)];
const seasons = [{id: 's1', showId: 'show', number: 1, title: 'Season 1', episodeCount: 7, watchedCount: 7}, {id: 's2', showId: 'show', number: 2, title: 'Season 2', episodeCount: 13, watchedCount: 2}];
const open = {phase: 'ready', entry: episodes[1], data: {item: {...episodes[1], overview: 'After their escape from Tuco.'}, personal: {progressSeconds: 720, watched: false},
  facts: {contentRating: 'TV-MA'}, metadata: {credits: [{id: 'c1', name: 'Mark Margolis', role: 'Hector Salamanca', department: 'acting', provider: 'tmdb'},
    {id: 'c2', name: 'Terry McDonough', role: 'Director', department: 'directing', provider: 'tmdb'}]}}} as any;

test('TV episodes draw the season side list, the next-up tile and its focus band', async () => {
  const calls: string[] = [];
  let tree: any;
  const host = createFabricHookHost(() => {tree = TVEpisodeBrowser({seasons, selectedSeasonId: 's2', episodes, nextUp: episodes[1], show: {title: 'Breaking Bad'},
    open, text: {t: defaultI18n.t}, artwork: () => undefined, more: (() => {calls.push('more');}) as any,
    onSeasonFocus: () => calls.push('season-focus'), onEpisodeFocus: (value: any) => calls.push(`episode-focus:${value.id}`),
    onSelectSeason: (id: string) => calls.push(`select:${id}`), onPlayEpisode: (value: any) => calls.push(`play:${value.id}`),
    onPlaySeason: (id: string) => calls.push(`play-season:${id}`), onMarkSeason: (id: string, watched: boolean) => calls.push(`mark:${id}:${watched}`),
    onMoreSeasons: () => calls.push('more-seasons'), onMoreEpisodes: () => calls.push('more-episodes'), onRetryEpisode: () => calls.push('retry'),
    renderError: (() => null) as any});});
  await host.mount();
  try {
    const nodes = descendants(tree);
    const rows = nodes.filter(node => node.props.testID?.startsWith('tv-season-'));
    assert.deepEqual(rows.map(row => row.props.testID), ['tv-season-s1', 'tv-season-s2']);
    assert.equal(rows[1].props.current, true, 'the open season is marked');
    assert.equal(rows[0].props.detail, defaultI18n.t('title.seasonProgress', {watched: 7, total: 7}));
    assert.equal(rows[1].props.detail, defaultI18n.t('title.seasonProgress', {watched: 2, total: 13}));
    const shelf = nodes.find(node => node.type === 'TVShelf');
    const tiles = shelf.props.items.map((entry: any, index: number) => shelf.props.renderItem(entry, {index, focusId: entry.id, onFocus() {}, onBlur() {}}));
    assert.equal(tiles.length, 3);
    assert.equal(tiles[1].props.status, defaultI18n.t('player.post.upNext'));
    assert.equal(shelf.props.focus.entry, 'e2');
    assert.equal(shelf.props.focus.edges.left, 'tv.episodes.seasons');
    assert.equal(rows[0].props.nextFocusRight, undefined, 'the shared native guide owns the boundary');
    assert.ok(tiles.every((tile: any) => tile.props.nextFocusLeft === undefined && !tile.props.preferredFocus));
    const band = nodes.find(node => node.type === FocusBand);
    const bandTree = FocusBand(band.props), bandNodes = descendants(bandTree);
    assert.ok(bandNodes.some(node => node.type === 'Text' && node.props.children === 'Bit by a Dead Bee'));
    assert.ok(bandNodes.some(node => node.type === 'Text' && node.props.children === 'S2 E3'));
    assert.ok(bandNodes.some(node => node.type === 'Text' && node.props.children === 'TV-MA'));
    assert.ok(bandNodes.some(node => node.type === 'Text' && text(node).startsWith('Mark Margolis')));
    assert.ok(bandNodes.some(node => node.type === 'Text' && node.props.children === defaultI18n.t('title.directedBy', {names: 'Terry McDonough'})));
    rows[0].props.onFocus(); assert.deepEqual(calls.filter(value => value.startsWith('select:')), ['select:s1'], 'season request is immediate on focus');
    rows[0].props.onPress(); assert.equal(calls.filter(value => value.startsWith('select:')).length, 1, 'OK adds no second request');
    await host.rerender();
    assert.equal(descendants(tree).find(node => node.type === 'TVShelf').props.focus.enabled, false);
    tiles[0].props.onPlay(); assert.equal(calls.some(value => value.startsWith('play:')), false, 'an outgoing season tile cannot play');
    rows[0].props.onLongPress(); await host.rerender();
    const menu = descendants(tree).find(node => node.type === 'Menu');
    assert.deepEqual(menu.props.items.map((item: any) => item.id), ['play-season', 'mark-season']);
    menu.props.items[0].onSelect(); assert.ok(calls.includes('play-season:s1'));
  } finally {await host.unmount();}
});

test('each season has an independent native row identity and next-up is only a first-visit entry', () => {
  const section = {id: 'show.episodes', edges: {up: 'show.actions', down: 'show.related', left: 'rail'}};
  const second = tvEpisodeRegionMap(section, 's2', ['e1', 'e2', 'e3'], 'e2');
  const third = tvEpisodeRegionMap(section, 's3', ['s3e1', 's3e2'], 'e2');
  assert.notEqual(second.row.id, third.row.id);
  assert.equal(second.row.entry, 'e2'); assert.equal(third.row.entry, 's3e1');
  assert.equal(second.seasons.edges.right, second.row.id);
  assert.equal(third.seasons.edges.right, third.row.id);
  assert.equal(second.section.entryRegion, second.row.id);
  assert.equal(second.row.edges.up, 'show.actions'); assert.equal(second.row.edges.down, 'show.related');
});

// Owner, 16 Nov: a long season list (The Simpsons, 25) scrolls in its own window and says so; a short one is untouched.
test('the season list fades only at an edge it really continues past, and keeps six rows and half the next on screen', () => {
  assert.equal(seasonListHeight, 6.5 * 90, 'six 84 pt rows 6 pt apart, and half of the seventh peeking in');
  assert.deepEqual(seasonListEdges(0, 5 * 90, 450), {top: false, bottom: false}, 'five seasons fit whole: no fade (tv-detail frame 5)');
  assert.deepEqual(seasonListEdges(0, 25 * 90, seasonListHeight), {top: false, bottom: true}, 'at the top of 25: the bottom fades');
  assert.deepEqual(seasonListEdges(600, 25 * 90, seasonListHeight), {top: true, bottom: true}, 'scrolled into the middle: both');
  assert.deepEqual(seasonListEdges(25 * 90 - seasonListHeight, 25 * 90, seasonListHeight), {top: true, bottom: false}, 'at the end: only the top');
  assert.deepEqual(seasonListEdges(0, 0, 0), {top: false, bottom: false}, 'before layout: none');
});

test('the kit list edge fade draws ink from clear to its opacity toward the edge, and never takes focus or a press', async () => {
  const {ListEdgeFade} = await screenModule(new URL('../src-next/kit/layout/ListEdgeFade.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, '../theme': themed,
  });
  for (const [edge, length, opacity] of [['bottom', 70, 0.9], ['top', 70, 0.9], ['bottom', 90, 0.94]] as const) {
    const fade = ListEdgeFade({edge, length, opacity});
    assert.deepEqual([fade.props.pointerEvents, fade.props.accessible, fade.props.style.height, fade.props.style[edge]], ['none', false, length, 0]);
    const stops = descendants(fade).filter(node => node.type === 'Stop');
    assert.deepEqual(stops.map(stop => [stop.props.stopColor, stop.props.stopOpacity]), [['#000', 0], ['#000', opacity]], 'ink, clear to the given opacity');
    const gradient = descendants(fade).find(node => node.type === 'LinearGradient');
    assert.equal(gradient.props.y2, edge === 'top' ? '0' : '1', 'darkest at the edge the list continues past');
  }
});
