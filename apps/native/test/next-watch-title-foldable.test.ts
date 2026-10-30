// @fabric-react
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {tvTitleRegionMap, tvTitleFocusSection} from '../src-next/screens/watch/tv-title-focus.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {cardText, episodeLabel, playbackIdentity, titleFactParts} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';

let width = 890, height = 626;
let posture: any = {status: 'available', window: {width: 890, height: 626}, folds: [{bounds: {left: 445, top: 0, right: 445, bottom: 626}, orientation: 'vertical', state: 'flat', isSeparating: false, occlusion: 'none'}]};
const theme = {isTV: false, platform: 'tablet', textScale: 1, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111', raisedHigh: '#151515', line: '#222', lineStrong: '#333', accent: '#3af', accentStrong: '#4bf'}, type: {bodyStrong: {}, body: {}, heading: {}, caption: {}, captionStrong: {}, meta: {}}};
const {NativeTitleScreen} = await screenModule(new URL('../src-next/screens/watch/TitleScreen.tsx', import.meta.url), {
  './TVTitleChrome': {TVTitleBackdrop: 'TVTitleBackdrop', TVTitleCompactHeader: 'TVTitleCompactHeader', TVTitleHeroCopy: 'TVTitleHeroCopy'},
  '../../kit/useMotion': {useMotion: () => ({reduce: false})},
  '../../../../../packages/client-core/src/presentation/errors': {presentError},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
  '../../platform/native-blur': {BlurView: 'BlurView'},
  '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: {id: string}) => entry.id},
  './tv-title-focus': {tvTitleRegionMap, tvTitleFocusSection}, '../../shell/tv-focus': {tvFocusIds}, '../../kit/focus': {FocusRegion: 'FocusRegion', useOptionalFocusRegistry: () => undefined},
  react: {...React, default: React},
  'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width, height})},
  '../../../../../packages/player-core/src': {sameFence: () => true}, '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
  '../../../../../packages/client-core/src/presentation/kit-model': {cardText, episodeLabel, playbackIdentity, titleFactParts}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'}, '../../kit/cards': {EpisodeTile: 'Episode', PosterCard: 'Poster'},
  '../../kit/Icon': {Icon: 'Icon'}, '../../kit/controls': {Button: 'Button', ResumeButton: 'Resume', Chip: 'Chip', Tabs: 'Tabs', Focusable: 'Focusable', Switch: 'Switch'},
  '../../kit/layout/Hero': {HeroMetadata: 'Facts'}, '../../kit/states': {TitleSkeleton: 'Skeleton'},
  '../../kit/theme': {nativeType: () => ({}), useTheme: () => theme},
  '../../platform/platformPosture': {usePlatformPosture: () => posture}, '../../shell/model': {mediumRailMetrics: {width: 84}},
  './TVEpisodeBrowser': {TVEpisodeBrowser: 'TVEpisodeBrowser'}, './SeasonsSheet': {NativeSeasonsSheet: 'SeasonsSheet'}, './ShowSettingsSheet': {NativeShowSettingsSheet: 'ShowSettingsSheet'},
  './TitleEpisodePlate': {NativeTitleEpisodePlate: 'EpisodePlate'}, './TitlePeople': {TitlePeople: 'TitlePeople'},
});
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const episode = {id: 'e1', kind: 'episode', title: 'When You’re Lost in the Darkness', libraryId: 'lib', seasonNumber: 1, episodeNumber: 1, duration: 3000};
const snapshot = {retired: false, entry: {id: 'show', kind: 'show', title: 'The Last of Us', libraryId: 'lib'}, place: {tab: 'episodes', seasonId: 's1', anchor: {itemId: null, offset: 0}},
  episodes: [episode], seasons: [{id: 's1', showId: 'show', number: 1, title: 'Season 1', episodeCount: 9, watchedCount: 0}],
  detail: {phase: 'idle'},
  workspace: {phase: 'ready', data: {show: {id: 'show', libraryId: 'lib', title: 'The Last of Us', overview: 'A hardened survivor.', credits: [{name: 'Pedro Pascal', role: 'Joel', department: 'acting', ordinal: 0}]},
    selected: {seasonId: 's1'}, groups: [], settings: {hideSeasons: false, ranges: false, absoluteNumbering: false, newestFirst: false}, nextUp: episode,
    episodes: {sections: [{entries: [episode]}]}}}};
const controller = {pageLease: () => undefined, getSnapshot: () => snapshot, subscribe: () => () => {}, memory: {getSnapshot: () => snapshot.place},
  episode: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {phase: 'idle'}; return () => snapshot;})(), open() {}, close() {}, play: async () => {}, personal: async () => {}, retry() {}},
  open: async () => {}, play: async () => {}, personal: async () => {}, season: async () => {}, moreSeasons: async () => {}, moreEpisodes: async () => {}, hideWatched: async () => {}, group: async () => {}};

test('a folded sideways title page divides on the hinge while the upright one stays one column', async () => {
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeTitleScreen({controller, text: {t: defaultI18n.t}, artwork: () => undefined, ambient: () => undefined,
    back: assert.fail, navigate: assert.fail, more: assert.fail, reportError: assert.fail, renderError: assert.fail, aboutLabel: 'About'});
  });
  await host.mount();
  try {
    const nodes = descendants(tree);
    const left = nodes.find(node => node.type === 'ScrollView' && node.props.style?.width === 361);
    assert.ok(left, 'the left page is the rail-adjusted half of the window');
    assert.ok(descendants(left).some(node => node.type === 'TitlePeople'), 'the cast sits on the left page');
    assert.equal(descendants(left).some(node => node.type === 'Tabs'), false, 'the tabs are not on the left page');
    assert.equal(nodes.filter(node => node.type === 'Tabs').length, 1);
    assert.equal(nodes.filter(node => node.type === 'Text' && node.props.children === 'The Last of Us').length, 1);
    width = 626; height = 890;
    posture = {status: 'available', window: {width: 626, height: 890}, folds: [{bounds: {left: 313, top: 0, right: 313, bottom: 890}, orientation: 'vertical', state: 'flat', isSeparating: false, occlusion: 'none'}]};
    await host.rerender();
    assert.equal(descendants(tree).some(node => node.type === 'ScrollView' && node.props.style?.width === 229), false, 'upright medium is one column, not two panes');
    assert.equal(descendants(tree).filter(node => node.type === 'Tabs').length, 1);
  } finally {await host.unmount();}
});
