// @fabric-react
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {tvTitleRegionMap, tvTitleFocusSection} from '../src-next/screens/watch/tv-title-focus.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
const {TitleEpisode, TitleRelated} = await screenModule(new URL('../src-next/screens/watch/TitleScreen.tsx', import.meta.url), {
  './TVTitleChrome': {TVTitleBackdrop: 'TVTitleBackdrop', TVTitleCompactHeader: 'TVTitleCompactHeader', TVTitleHeroCopy: 'TVTitleHeroCopy'},
  '../../kit/useMotion': {useMotion: () => ({reduce: false})},
  '../../../../../packages/client-core/src/presentation/errors': {presentError},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
  '../../platform/native-blur': {BlurView: 'BlurView'},
  '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: {id: string}) => entry.id},
  './tv-title-focus': {tvTitleRegionMap, tvTitleFocusSection}, '../../shell/tv-focus': {tvFocusIds}, '../../kit/focus': {FocusRegion: 'FocusRegion', useOptionalFocusRegistry: () => undefined},
  react: {...React, default: React}, 'react-native': {},
  '../../../../../packages/player-core/src': {}, '../../../../../packages/client-core/src/presentation/kit-model/text': {},
  '../../../../../packages/client-core/src/presentation/kit-model': {}, '../../../../../packages/i18n/src': {},
  '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable', window: null, folds: []})}, '../../shell/model': {mediumRailMetrics: {width: 84}}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/cards': {EpisodeTile: 'EpisodeTile', PosterCard: 'PosterCard'},
  '../../kit/Icon': {Icon: 'Icon'}, '../../kit/controls': {Switch: 'Switch'}, '../../kit/layout/Hero': {}, '../../kit/states': {}, './TVEpisodeBrowser': {TVEpisodeBrowser: 'TVEpisodeBrowser'}, './SeasonsSheet': {NativeSeasonsSheet: 'SeasonsSheet'}, './ShowSettingsSheet': {NativeShowSettingsSheet: 'ShowSettingsSheet'}, './TitleEpisodePlate': {NativeTitleEpisodePlate: 'EpisodePlate'}, './TitlePeople': {TitlePeople: 'TitlePeople'}, '../../kit/theme': {},
});
test('each real title card retains its own native ref and unchanged TV/keyboard/a11y callback shape', async () => {
  for (const render of [TitleEpisode, TitleRelated]) {
    const entry = {id: 'real', kind: 'episode', title: 'Real member'}, selected: any[] = [];
    const onNavigate = () => {}, onPlay = () => {};
    const selection = {active: false, selected: false, onEnter() {}, onToggle() {}};
    let tree: any;
    const host = createFabricHookHost(() => {tree = render({entry, title: entry.title, primaryAction: 'play', onPlay, onNavigate, selection, moreExpanded: true, more: (...args: unknown[]) => {selected.push(args);}});});
    await host.mount();
    try {
      const ref = tree.props.menuAnchorRef; ref.current = {nativeTag: 42};
      assert.equal(tree.props.onMore.length, 0); tree.props.onMore();
      assert.equal(selected[0][0], entry); assert.equal(selected[0][1], ref);
      await host.rerender(); assert.equal(tree.props.menuAnchorRef, ref);
      assert.equal(tree.props.selection, selection);
      assert.equal(tree.props.onPlay, onPlay); assert.equal(tree.props.onNavigate, onNavigate); assert.equal(tree.props.moreExpanded, true);
    } finally {await host.unmount();}
  }
});
