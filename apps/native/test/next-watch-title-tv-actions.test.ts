// @fabric-react
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {tvTitleRegionMap, tvTitleFocusSection} from '../src-next/screens/watch/tv-title-focus.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {cardText, episodeLabel, playbackIdentity, titleFactParts} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
// W-17: OK on a TV tab enters its section through the registry.
const enteredRegions: string[] = [];
let capturedFocus: any = undefined;
const focusRegistry = {subscribe: () => () => {}, getSnapshot: () => 0, capture: () => capturedFocus, focusRegion: (id: string) => {enteredRegions.push(id); return true;}};

const {NativeTitleScreen} = await screenModule(new URL('../src-next/screens/watch/TitleScreen.tsx', import.meta.url), {
  './TVTitleChrome': {TVTitleBackdrop: 'TVTitleBackdrop', TVTitleCompactHeader: 'TVTitleCompactHeader', TVTitleHeroCopy: 'TVTitleHeroCopy'},
  '../../kit/useMotion': {useMotion: () => ({reduce: false})},
  '../../../../../packages/client-core/src/presentation/errors': {presentError},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
  '../../platform/native-blur': {BlurView: 'BlurView'},
  '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: {id: string}) => entry.id},
  './tv-title-focus': {tvTitleRegionMap, tvTitleFocusSection}, '../../shell/tv-focus': {tvFocusIds}, '../../kit/focus': {FocusRegion: 'FocusRegion', useOptionalFocusRegistry: () => focusRegistry},
  react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'Scroll', useWindowDimensions: () => ({width: 1920, height: 1080})},
  '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
  '../../../../../packages/client-core/src/presentation/kit-model': {cardText, episodeLabel, playbackIdentity, titleFactParts}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'}, '../../kit/cards': {EpisodeTile: 'Episode', PosterCard: 'Poster'},
  '../../kit/Icon': {Icon: 'Icon'}, '../../kit/controls': {Button: 'Button', ResumeButton: 'Resume', Chip: 'Chip', Tabs: 'Tabs', Focusable: 'Focusable', Switch: 'Switch'}, '../../kit/layout/Hero': {HeroMetadata: 'Facts'},
  '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable', window: null, folds: []})}, '../../shell/model': {mediumRailMetrics: {width: 84}}, '../../kit/states': {TitleSkeleton: 'Skeleton'}, './TVEpisodeBrowser': {TVEpisodeBrowser: 'TVEpisodeBrowser'}, './SeasonsSheet': {NativeSeasonsSheet: 'SeasonsSheet'}, './ShowSettingsSheet': {NativeShowSettingsSheet: 'ShowSettingsSheet'}, './TitleSeasonBar': {TitleSeasonBar: 'TitleSeasonBar'}, './TitleEpisodePlate': {NativeTitleEpisodePlate: 'EpisodePlate'}, './TitlePeople': {TitlePeople: 'TitlePeople'}, '../../kit/theme': {nativeType: () => ({}), useTheme: () => ({isTV: true, colors: {}, type: {bodyStrong: {}, body: {}, heading: {}, caption: {}}})},
});
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : React.isValidElement(node) ? [node, ...descendants((node as any).props.children)] : [];

test('TV title resume, Start over, watched and Play menu bind actual current title actions and the genuine anchor', async () => {
  const calls: unknown[][] = [], entry = {id: 'inside-out', kind: 'movie', title: 'Inside Out', duration: 5700, progressSeconds: 540};
  let snapshot: any = {retired: false, entry, place: {tab: 'related', anchor: {itemId: null, offset: 0}}, episodes: [], seasons: [],
    detail: {phase: 'ready', data: {item: entry, actions: [
      {id: 'resume', enabled: true, playback: {itemId: entry.id, startSeconds: 540}},
      {id: 'start_over', enabled: true, playback: {itemId: entry.id, startSeconds: 0}},
      {id: 'watched', enabled: true},
    ], personal: {progressSeconds: 540, watched: false}, metadata: {genres: [], ratings: [], credits: [
      {id: 'd1', name: 'Pete Docter', role: 'Director', department: 'directing', provider: 'tmdb'},
      {id: 'c1', name: 'Amy Poehler', role: 'Joy', department: 'acting', provider: 'tmdb'},
      {id: 'c2', name: 'Phyllis Smith', role: 'Sadness', department: 'acting', provider: 'tmdb'}]}}}};
  const controller = {pageLease: () => undefined, getSnapshot: () => snapshot, subscribe: () => () => {}, memory: {getSnapshot: () => snapshot.place}, episode: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {phase: 'idle'}; return () => snapshot;})(), open() {}, close() {}, play: async () => {}, personal: async () => {}, retry() {}}, open: async () => {},
    play: async (...args: unknown[]) => {calls.push(['play', ...args]);}, personal: async (...args: unknown[]) => {calls.push(['personal', ...args]);}};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeTitleScreen({controller, text: {t: defaultI18n.t}, artwork: () => undefined, ambient: () => undefined,
    back: assert.fail, navigate: assert.fail, more: (...args: unknown[]) => {calls.push(['more', ...args]);}, reportError: assert.fail, renderError: assert.fail, aboutLabel: 'About'});});
  await host.mount();
  try {
    const resume = descendants(tree).find(node => node.props.testID === 'watch-title-play');
    assert.equal(resume.type, 'Resume'); assert.equal(resume.props.progress, 540 / 5700);
    assert.equal(resume.props.remaining, defaultI18n.t('next.kit.remaining', {time: durationText(5160, defaultI18n.t)}));
    const anchor = resume.props.ref; anchor.current = {nativeTag: 72}; resume.props.onLongPressAction();
    assert.deepEqual(calls.shift(), ['more', entry, anchor]);
    await descendants(tree).find(node => node.props.focusId === 'watch-title-start-over').props.onPress();
    assert.deepEqual(calls.shift(), ['play', entry, true]);
    assert.ok(descendants(tree).some(node => node.type === 'Text' && node.props.children === defaultI18n.t('title.directedBy', {names: 'Pete Docter'})));
    assert.ok(descendants(tree).some(node => node.type === 'Text' && node.props.children === defaultI18n.t('title.starring', {names: 'Amy Poehler and Phyllis Smith'})));
    const watched = descendants(tree).find(node => node.props.focusId === 'watch-title-watched');
    assert.equal(watched.props.accessibilityLabel, defaultI18n.t('entry.markWatched')); watched.props.onPress();
    assert.deepEqual(calls.shift(), ['personal', {action: 'watched', value: true}]);
    snapshot = {...snapshot, detail: {...snapshot.detail, data: {...snapshot.detail.data, actions: snapshot.detail.data.actions.filter((action: any) => action.id === 'resume')}}};
    await host.rerender();
    assert.equal(descendants(tree).some(node => node.props.focusId === 'watch-title-watched' || node.props.focusId === 'watch-title-start-over'), false);
  } finally {await host.unmount();}
});

test('TV Down from the hero buttons goes straight to the first row; the sticky header strip has the tab stops while browsing (W-17)', async () => {
  const entry = {id: 'inside-out', kind: 'movie', title: 'Inside Out'};
  const snapshot = {retired: false, entry, place: {tab: 'related', anchor: {itemId: null, offset: 0}}, episodes: [], seasons: [],
    detail: {phase: 'ready', data: {item: entry, actions: [{id: 'play', enabled: true, playback: {itemId: entry.id, startSeconds: 0}}], personal: {progressSeconds: 0},
      metadata: {genres: [], ratings: [], credits: []},
      related: {rows: [{id: 'row-1', heading: 'More like Inside Out', entries: [{id: 'first', kind: 'movie', title: 'First film'}, {id: 'second', kind: 'movie', title: 'Second film'}]}]}}}};
  const controller = {pageLease: () => undefined, getSnapshot: () => snapshot, subscribe: () => () => {}, memory: {getSnapshot: () => snapshot.place}, episode: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {phase: 'idle'}; return () => snapshot;})(), open() {}, close() {}, play: async () => {}, personal: async () => {}, retry() {}}, open: async () => {}, play: async () => {}};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeTitleScreen({controller, text: {t: defaultI18n.t}, artwork: () => undefined, ambient: () => undefined,
    back: assert.fail, navigate: assert.fail, more: assert.fail, reportError: assert.fail, renderError: assert.fail, aboutLabel: 'About'});});
  await host.mount();
  try {
    const play = () => descendants(tree).find(node => node.props.focusId === 'watch-title-play');
    const shelf = () => descendants(tree).find(node => node.type === 'TVShelf');
    const posters = () => shelf().props.items.map((entry: any, index: number) => shelf().props.renderItem(entry, {focusId: entry.id, index, onFocus() {}, onBlur() {}}));
    assert.equal(play().props.nextFocusDown, undefined, 'no screen-owned native tags');
    const regions = descendants(tree).filter(node => typeof node.props.id === 'string');
    const actions = regions.find(node => node.props.id.endsWith('.actions'));
    const row = {props: shelf().props.focus};
    assert.equal(actions.props.edges.down, row.props.id);
    assert.equal(row.props.edges.up, actions.props.id);
    assert.equal(actions.props.entry, 'watch-title-play');
    assert.deepEqual(posters().map((node: React.ReactElement<{focusId: string}>) => node.props.focusId), ['first', 'second']);
    // W-17 (lead ruling: follow tv-detail): Down from the hero buttons goes straight into the first row, so the strip
    // under the hero is a visible marker with no stops; the sticky header's copy has the tab stops while browsing.
    const strips = () => descendants(tree).filter(node => node.props.testID === 'watch-title-tabs');
    assert.deepEqual(strips().map(node => node.props.passive), [true, true], 'on the hero, neither strip takes focus: no stop between Play and the first row');
    capturedFocus = {regionId: row.props.id, childId: 'first'};
    await host.rerender(); await host.rerender();
    const [hero, header] = strips();
    assert.equal(hero.props.passive, true, 'the folded hero strip stays a marker');
    assert.equal(header.props.passive, false, 'browsing: the sticky header strip has the tab stops Up from the first row reaches');
    enteredRegions.length = 0; header.props.onValueChange('related');
    assert.deepEqual(enteredRegions, [row.props.id], 'OK on More like this enters its first row');
    capturedFocus = undefined;
    assert.ok(descendants(tree).some(node => node.type === 'Text' && node.props.children === 'About'), 'Details stays mounted below related rows');

  } finally {await host.unmount();}
});

test('a TV title with nothing to play still opens on a control: More takes the preferred focus', async () => {
  const entry = {id: 'brass-lantern', kind: 'movie', title: 'Brass Lantern'};
  const snapshot = {retired: false, entry, place: {tab: 'related', anchor: {itemId: null, offset: 0}}, episodes: [], seasons: [],
    detail: {phase: 'ready', data: {item: entry, actions: [], personal: {progressSeconds: 0}, metadata: {genres: [], ratings: [], credits: []}}}};
  const controller = {open: async () => {}, pageLease: () => undefined, getSnapshot: () => snapshot, subscribe: () => () => {}, memory: {getSnapshot: () => snapshot.place}, episode: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {phase: 'idle'}; return () => snapshot;})(), open() {}, close() {}, play: async () => {}}};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeTitleScreen({controller, text: {t: defaultI18n.t}, artwork: () => undefined, ambient: () => undefined,
    back: assert.fail, navigate: assert.fail, more: assert.fail, reportError: assert.fail, renderError: assert.fail, aboutLabel: 'About'});});
  await host.mount();
  try {
    const nodes = descendants(tree);
    assert.equal(nodes.some(node => node.props.focusId === 'watch-title-play'), false);
    assert.equal(nodes.find(node => node.props.focusId === 'watch-title-more').props.preferredFocus, true);
  } finally {await host.unmount();}
});
