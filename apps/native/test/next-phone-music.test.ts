// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {artistReleaseGroups} from '../../../packages/client-core/src/presentation/library-tabs.ts';
import {defaultI18n} from '../../../packages/i18n/src';


const musicImports = {
  'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
  './use-playing-item': {usePlayingItem: (runtime: any) => runtime.playback?.getSnapshot().playback.itemId},
  './MusicWindow': {MusicScrollView: 'ScrollView', MusicWindow: 'MusicWindow', MusicShelf: 'ScrollView'},
  '../../../kit/selection': {SelectionCircle: 'SelectionCircle'},
};

const stubs = {
  ...musicImports,
  react: {default: {createElement() {}, useRef() { return {current: null}; }, useSyncExternalStore() {}}},
  'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
  '../../../../../../packages/i18n/src': {defaultI18n: {t: () => '', number: () => ''}},
  '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({detail: []})},
  '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt() {}, Artwork() {}, ArtistCircle() {}},
  '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
  '../../../kit/cards': {SquareCard: 'SquareCard'},
  '../../../kit/Icon': {Icon: 'Icon'},
  '../../../kit/states': {StateView: 'StateView'},
  '../../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
  '../../../kit/theme': {useTheme: () => ({})},
  './MusicSectionControls':{MusicSectionControls:'MusicSectionControls',musicNearEnd:()=>true},
  '../../../../../../packages/client-core/src/presentation/library-tabs':{artistReleaseGroups},
  './MusicErrorView': {MusicErrorView: 'MusicErrorView'},
  './MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
};

test('the phone album and artist pages keep the drawn frame values', async () => {
  const {phoneMusicGeometry} = await screenModule(new URL('../src-next/screens/destinations/music/PhoneMusicScreens.tsx', import.meta.url), stubs);
  assert.deepEqual(phoneMusicGeometry, {
    albumTop: 104, albumCover: 232, albumCoverRadius: 12, albumTitle: 30, albumArtist: 16,
    meta: 13, actionHeight: 46, actionRadius: 10, actionGap: 10,
    trackHeight: 54, trackPaddingLeft: 20, trackPaddingRight: 12, trackGap: 14, trackNumberWidth: 18,
    trackTitle: 15.5, trackNumber: 14, trackDuration: 13.5, trackMoreWidth: 34,
    artistTop: 98, artistCircle: 164, artistTitle: 30, sectionLabel: 13.5, albumCard: 132,
  });
});

// BLK-MOB-006/008: the tablet album and artist projections must expose the
// route's own Back in every phase and the existing MusicErrorView recovery at
// the track/release area. These are render sources for the reviewer, not a
// rendered exercise of the device.
import * as React from 'react';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

type Element = {type: unknown; props: Record<string, any>};
function* elementNodes(value: unknown): Generator<Element> {
  if (Array.isArray(value)) {for (const item of value) yield* elementNodes(item); return;}
  if (!value || typeof value !== 'object' || !('props' in value)) return;
  const node = value as Element; yield node;
  const children = node.props?.children;
  for (const child of Array.isArray(children) ? children.flat(Infinity) : children == null ? [] : [children]) yield* elementNodes(child);
}
const typed = (tree: unknown, type: string) => [...elementNodes(tree)].filter(node => node.type === type);
const backButtons = (tree: unknown) => typed(tree, 'Button').filter(node => node.props?.accessibilityLabel === 'Back');

async function tabletMusic(kind: 'album' | 'artist', phase: 'loading' | 'ready' | 'error') {
  let backs = 0;
  const loads: string[] = [];
  const snapshot = {phase, songs: kind === 'artist' && phase === 'ready' ? [{id: 's1', title: 'Song'}] : [],
    albums: kind === 'artist' && phase === 'ready' ? [{id: 'al1', libraryId: 'music', title: 'Album'}] : [],
    similar: [], similarError: undefined, favorite: false, error: phase === 'error' ? new Error('load refused') : undefined,
    albumDetails: undefined, songTotal: 0, songDurationSeconds: undefined, songPage:{phase:'ready',items:[],offset:0,nextCursor:'',canNext:false,canPrevious:false},releases:{phase:'ready',items:[],offset:0,nextCursor:'',canNext:false,canPrevious:false}};
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, load: () => {loads.push('load'); return Promise.resolve();},
    play: async () => {}, toggleFavorite: async () => {}, albumMembership: async () => [], contains: () => true, similarCurrent: () => true};
  const runtime = {artwork: () => undefined, ambient: () => undefined, text: {locale: 'en-US'}, reportError() {}, playback: undefined};
  const theme = {platform: 'tablet', colors: {ink: '#000', raised: '#111', shadow: '#000', text: '#fff', textSecondary: '#aaa',
    accentStrong: '#09f', textTertiary: '#777', line: '#222'}};
  const {PhoneAlbumScreen, PhoneArtistScreen} = await screenModule(new URL('../src-next/screens/destinations/music/PhoneMusicScreens.tsx', import.meta.url), {
    ...musicImports,
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({detail: []})},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', ArtistCircle: 'ArtistCircle'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/cards': {CircleCard: 'CircleCard', SquareCard: 'SquareCard'},
    '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
    '../../../kit/theme': {useTheme: () => theme},
    './MusicSectionControls':{MusicSectionControls:'MusicSectionControls',musicNearEnd:()=>true},
  '../../../../../../packages/client-core/src/presentation/library-tabs':{artistReleaseGroups},
  './MusicErrorView': {MusicErrorView: 'MusicErrorView'},
    './MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
  });
  const Screen = kind === 'album' ? PhoneAlbumScreen : PhoneArtistScreen;
  const entry = kind === 'album' ? {id: 'album-1', libraryId: 'music', kind: 'album', title: 'Album'} : {id: 'artist-1', libraryId: 'music', kind: 'artist', title: 'Artist'};
  let tree: unknown;
  const host = createFabricHookHost(() => {tree = Screen({controller, entry, runtime, actions: {menu() {}}, openEntry() {}, onBack: () => {backs++;},
    ...(kind === 'album' ? {onPlaylist() {}} : {})});});
  await host.mount();
  return {get tree() {return tree;}, host, loads, get backs() {return backs;}};
}

for (const kind of ['album', 'artist'] as const) {
  for (const phase of ['loading', 'error', 'ready'] as const)
  test(`tablet ${kind} keeps one route Back while ${phase} and only ${phase === 'error' ? 'fails' : 'does not fail'} into the recovery view`, async () => {
    const view = await tabletMusic(kind, phase);
    const backs = backButtons(view.tree);
    assert.equal(backs.length, 1, 'exactly one accessible tablet Back');
    assert.equal(backs[0].props.testID, kind === 'album' ? 'native-tablet-album-back' : 'native-tablet-artist-back');
    assert.equal(backs[0].props.icon, 'i-left');
    assert.equal(backs[0].props.accessibilityLabel, 'Back');
    backs[0].props.onPress();
    assert.equal(view.backs, 1, 'the supplied original onBack is invoked once');
    const errors = typed(view.tree, 'MusicErrorView');
    if (phase === 'error') {
      assert.equal(errors.length, 1, 'the error phase mounts the existing MusicErrorView');
      errors[0].props.onRetry();
      assert.deepEqual(view.loads, ['load'], 'Retry asks the guarded controller to load again, once');
    } else assert.equal(errors.length, 0, 'a non-error phase does not draw the recovery view');
    await view.host.unmount();
  });
}

test('phone keeps its single header Back and no tablet floating control', async () => {
  const view = await tabletMusic('album', 'error');
  // The phone body is a separate projection; render it with the phone platform.
  const {PhoneAlbumScreen} = await screenModule(new URL('../src-next/screens/destinations/music/PhoneMusicScreens.tsx', import.meta.url), {
    ...musicImports,
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({detail: []})},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', ArtistCircle: 'ArtistCircle'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/cards': {CircleCard: 'CircleCard', SquareCard: 'SquareCard'},
    '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({platform: 'phone', colors: {ink: '#000', raised: '#111', shadow: '#000', text: '#fff', textSecondary: '#aaa',
      accentStrong: '#09f', textTertiary: '#777', line: '#222'}})},
    './MusicSectionControls':{MusicSectionControls:'MusicSectionControls',musicNearEnd:()=>true},
  '../../../../../../packages/client-core/src/presentation/library-tabs':{artistReleaseGroups},
  './MusicErrorView': {MusicErrorView: 'MusicErrorView'},
    './MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
  });
  let backs = 0, tree: unknown;
  const controller = {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {phase: 'ready', songs: [], albums: [], similar: [], favorite: false, error: undefined,songPage:{phase:'ready',items:[],offset:0,nextCursor:'',canNext:false,canPrevious:false},releases:{phase:'ready',items:[],offset:0,nextCursor:'',canNext:false,canPrevious:false}}; return () => snapshot;})(),
    load: () => Promise.resolve(), play: async () => {}, toggleFavorite: async () => {}};
  const host = createFabricHookHost(() => {tree = PhoneAlbumScreen({controller, entry: {id: 'a', libraryId: 'music', kind: 'album', title: 'Album'},
    runtime: {artwork: () => undefined, ambient: () => undefined, text: {locale: 'en-US'}, reportError() {}}, actions: {menu() {}}, openEntry() {},
    onBack: () => {backs++;}});});
  await host.mount();
  // PhoneHeader is a real child component, so the shallow element walk cannot
  // see its Button leaf; mount that actual child with the same supported hooks.
  let headerHost: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    const header = [...elementNodes(tree)].find(node => typeof node.type === 'function' && (node.type as Function).name === 'PhoneHeader');
    assert.ok(header, 'the phone body returns the real PhoneHeader');
    let headerTree: unknown;
    headerHost = createFabricHookHost(() => {headerTree = (header!.type as Function)(header!.props);});
    await headerHost.mount();
    assert.equal(backButtons(headerTree).length, 1, 'the phone header owns the only Back');
    assert.equal(typed(tree, 'Button').some(node => String(node.props.testID ?? '').startsWith('native-tablet-')), false);
    backButtons(headerTree)[0].props.onPress();
    assert.equal(backs, 1);
  } finally {
    // Release both hosts even when an assertion above fails.
    await headerHost?.unmount();
    await host.unmount();
    await view.host.unmount();
  }
});
