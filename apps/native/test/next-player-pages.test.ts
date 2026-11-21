import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

type Node = {type: string; props: Record<string, any>};
const react = {default: {Fragment: 'Fragment', createElement: (type: string, props: object, ...children: Node[]): Node => ({type, props: {...props, children}})}};

test('the upright player keeps the host picture and routes Quality, full screen and Minimize', async () => {
  const {PlayerPage} = await screenModule(new URL('../src-next/pages/touch/player/PlayerPage.tsx', import.meta.url), {
    react, '../../../kit/frames': {PlayerChromeFrame: 'PlayerChromeFrame'},
    '../../../kit/player': {PlayerControl: 'PlayerControl', PlayerPill: 'PlayerPill'},
    '../../../../../../packages/i18n/src': {defaultI18n},
  });
  const actions: string[] = [];
  const frame = PlayerPage({picture: 'picture', chromeVisible: true, quality: 'Original', controlsEnabled: true,
    onMinimize: () => actions.push('minimize'), onQuality: () => actions.push('quality'), onFullWindow: () => actions.push('full')}) as Node;
  assert.equal(frame.props.picture, 'picture');
  assert.equal(frame.props.form, 'upright');
  frame.props.trailing.props.children[0].props.onPress();
  frame.props.trailing.props.children[1].props.onPress();
  frame.props.close.props.onPress();
  assert.deepEqual(actions, ['quality', 'full', 'minimize']);
});

test('TV video keeps the full title, remote focus and held-seek content in one frame', async () => {
  const {TVPlayerPage} = await screenModule(new URL('../src-next/pages/tv/player/PlayerPage.tsx', import.meta.url), {
    react, '../../../kit/frames': {PlayerChromeFrame: 'PlayerChromeFrame'}, '../../../kit/player': {PlayerTitle: 'PlayerTitle'},
  });
  const frame = TVPlayerPage({identity: {title: 'Full episode title'}, focus: {transport: {entry: 'watch-toggle'}}, scrubbing: true, hints: 'remote hints'}) as Node;
  assert.equal(frame.props.form, 'tv');
  assert.equal(frame.props.title.props.title, 'Full episode title');
  assert.equal(frame.props.focus.transport.entry, 'watch-toggle');
  assert.equal(frame.props.hints, 'remote hints');
});

test('audio keeps its page scroll, wide panel actions, and TV lyrics focus return', async () => {
  const {frameSpec} = await import('@portico/design');
  const {createTheme} = await import('../src-next/kit/theme');
  let form = 'phone', width = 800, height = 390;
  const theme = () => createTheme({width, isTV: form === 'tv'});
  const {AudioPlayerFrame} = await screenModule(new URL('../src-next/kit/frames/AudioPlayerFrame.tsx', import.meta.url), {
    react: {...react, useState: (value: unknown) => [value, () => {}]},
    'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {}}, useWindowDimensions: () => ({width, height})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '@portico/design': {frameSpec}, '../../../../../packages/i18n/src': {defaultI18n}, '../theme': {useTheme: theme},
    '../controls/Button': {Button: 'Button'}, '../controls/Focusable': {Focusable: 'Focusable'}, '../Icon': {Icon: 'Icon'},
    '../states/SkeletonBlock': {SkeletonBlock: 'Skeleton'}, '../shell/visual': {alpha: String},
    '../focus': {StartFocusScope: 'StartFocusScope', FocusRegion: 'FocusRegion'}, '../player/parts': {PlayerControl: 'PlayerControl'},
    '../player/queue': {AudioChoiceRow: 'AudioChoiceRow'}, './parts': {ArtBox: 'ArtBox'},
    './metrics': {useFrameMetrics: () => ({form, width, gutter: 20, rightGutter: 20, rowTwoLine: 60, bottomClear: 20})},
  });
  const all = (node: any): Node[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children)] : [];
  const ref = {current: null}, close = {icon: 'i-down', label: 'Close', onPress() {}};
  const lyrics = {...close, label: 'Lyrics', focusId: 'lyrics', testID: 'lyrics', anchorRef: ref};
  const queue = {...close, label: 'Up next', testID: 'queue'};
  const render = () => {
    const leaf = AudioPlayerFrame({close, title: 'Song', icon: 'i-music', scrubber: 'Seek', times: {position: '0:00', end: '1:00'},
      lyrics, queue, transport: [undefined, close, close, close, undefined]}).props.children[0];
    return all(leaf.type(leaf.props));
  };
  assert.ok(all(render().find(node => node.type === 'ScrollView')).some(node => node.props.item === lyrics), 'the short phone can scroll to Lyrics');
  form = 'expanded'; width = 1180; height = 800;
  assert.deepEqual(render().filter(node => ['lyrics', 'queue'].includes(node.props.testID)).map(node => node.props.testID), ['lyrics', 'queue']);
  form = 'tv'; width = 1920; height = 1080;
  const action = render().find(node => node.props.testID === 'lyrics');
  assert.equal(action?.props.focusId, 'lyrics');
  assert.equal(action?.props.ref, ref);
});

test('closing TV Lyrics returns focus to its opener without starting at Play again', async () => {
  const host = hooks();
  const {useTVAudioPlayer} = await screenModule(new URL('../src-next/pages/shared/player/useTVAudioPlayer.ts', import.meta.url), {
    react: {...host.react, useId: () => 'now-playing', useLayoutEffect: () => {}},
    '../../../../../../packages/player-core/src': {sameFence: (left: unknown, right: unknown) => left === right},
    '../../../kit/focus': {}, '../../../kit/dock/model': {playbackPosition: () => 12, seekBounds: () => undefined},
    '../../../shell/model': {shellLayout: () => ({})},
    '../../../platform/playback/equalizer': {nativeEqualizer: {getSnapshot: () => ({supported: false})}},
    '../../../platform/overlay-keys': {}, '../../../screens/destinations/music/ListeningSongActions': {},
    './useNowPlayingQueue': {nowPlayingSource: () => undefined},
    '../../../screens/destinations/music/TVQueuePolicy': {useTVQueuePolicy: () => ({})},
    '../../../tv/layout/TVPageMotion': {useTVPageMotion: () => ({open: true, ready: true})}, '../../../kit/navigation/back': {},
  });
  let fence = {};
  const view = () => host.render(() => useTVAudioPlayer({lyrics: 'lyrics',
    occupant: {playback: {fence}, presentation: {identity: {subtitle: []}, capabilities: {}}}}));
  const opening = view();
  assert.equal(opening.startFocus, undefined);
  opening.setPanel('lyrics');
  const lyrics = view();
  assert.equal(lyrics.startFocus, undefined);
  assert.equal(lyrics.close(), true);
  assert.equal(view().startFocus, 'lyrics');
  fence = {};
  assert.equal(view().startFocus, undefined);
});
