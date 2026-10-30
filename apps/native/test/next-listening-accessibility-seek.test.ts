// @fabric-react
import * as audiobookControls from '../src-next/screens/destinations/music/audiobook-controls.ts';
/** Actual touch component callbacks with controlled hook/native ports. These
 * tests prove command custody and values, not native gesture or AT delivery. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {listeningCoverFixture, liveOverlayPaint, screenModule} from './helpers/screen-harness.mjs';
import {dockFence} from './helpers/dock-fence.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {clockText} from '../../../packages/client-core/src/presentation/kit-model/text';
import {GestureCommitScope} from '../src-next/kit/controls/gesture-commit.ts';
import {commandAllowed, dockCommand, playbackPosition, seekBounds, seekTo} from '../src-next/kit/dock/model.ts';
import type {DockCommand, DockOccupant} from '../src-next/kit/dock/types.ts';
import type {ListeningScrubber} from '../src-next/kit/dock/ListeningCoverBridge.tsx';
import type {NowPlayingScreenProps} from '../src-next/screens/destinations/music/NowPlayingScreen.tsx';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes)
  : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];
type PanFixture = {callbacks: Record<string, (...args: any[]) => void>; config: Record<string, unknown>};

async function fixture(hosted = true) {
  let tree: any, scrubber: ListeningScrubber | undefined, pageActive = true, paintCurrent = true;
  const fence = dockFence('accessible-listening');
  const commands: DockCommand[] = [];
  let occupant: DockOccupant = {
    mode: 'music', playback: {kind: 'music', fence, queue: null, positionSeconds: 150, playing: true, expanded: true},
    presentation: {fence, identity: {id: 'song', kind: 'music', title: 'Song', subtitle: []}, durationSeconds: 300,
      seekWindow: {start: 100, end: 200}, skipBackSeconds: 10, skipForwardSeconds: 30,
      capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false}},
  };
  const theme = {isTV: false, colors: {text: '#fff', textSecondary: '#ddd', textTertiary: '#aaa', ink: '#000', line: '#555'}, layers: {player: 30}};
  const equalizer = {supported: true};
  const permit = {current: () => paintCurrent, subscribe: () => () => {}};
  const pan = () => {
    const value: any = {callbacks: {}, config: {}};
    for (const name of ['enabled', 'minDistance']) value[name] = (setting: unknown) => {value.config[name] = setting; return value;};
    for (const name of ['onBegin', 'onUpdate', 'onEnd', 'onFinalize']) value[name] = (run: (...args: any[]) => void) => {value.callbacks[name] = run; return value;};
    return value;
  };
  const mod = await screenModule(new URL('../src-next/screens/destinations/music/NowPlayingScreen.tsx', import.meta.url), {'./use-audiobook-read': {useAudiobookRead: () => ({reader: undefined, snapshot: {phase: 'retired'}})}, './audiobook-controls': audiobookControls, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})}, 
    react: {...React, default: React},
    'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView', BackHandler: {addEventListener: () => ({remove() {}})}},
    'react-native-gesture-handler': {Gesture: {Pan: pan}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, runOnJS: (run: unknown) => run,
      useSharedValue: (value: unknown) => React.useRef({value}).current, useAnimatedStyle: (read: () => unknown) => read()},
    '../../../platform/playback/equalizer': {nativeEqualizer: {subscribe: () => () => {}, getSnapshot: () => equalizer}},
    '../../../platform/overlay-keys': {registerOverlayKeys: () => () => {}},
    '../../watch/Equalizer': {NativeWatchEqualizer: 'Equalizer'}, './MusicSignal': {MusicSignalLine: 'Signal', MusicSignalSheet: 'SignalSheet'},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': {clockText},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/controls/gesture-commit': {GestureCommitScope},
    '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/theme': {useTheme: () => theme},
    '../../../kit/dock/ListeningCoverBridge': {...listeningCoverFixture,
      useListeningPaintPermit: () => permit, useListeningPageActive: () => pageActive,
      useListeningScrubberPublication: (value: ListeningScrubber | undefined) => {scrubber = value; return hosted;}},
    '../../../kit/browse/presentation': {alpha: (color: string) => color, browseType: () => ({})},
    '../../../kit/dock/model': {commandAllowed, dockCommand, playbackPosition, seekBounds, seekTo},
    './NowPlayingQueue': {NowPlayingQueue: 'Queue'},
    './ListeningSongActions': {ListeningSongActions: 'SongActions', ListeningFavoritePaint: 'Favorite'},
    './TVMusicFocus': {MusicFocusRegion: 'FocusRegion', TVMusicFocus: 'TVFocus', TVMusicSeek: 'TVSeek'},
    './ListeningNotice': {NativeListeningNotice: 'Notice'}, './TVNowPlayingScreen': {TVNowPlayingScreen: 'TVNowPlaying'},
    '../../../kit/overlays/SurfacePaintHost': {RetainedOverlay: 'RetainedOverlay'},
    '../../../kit/dock/PlayerMorphBridge': {usePublishedPlayerMorph: () => undefined},
    '../../../kit/overlays/OverlayPaintContext': liveOverlayPaint,
    './NowPlayingLyrics': {NowPlayingLyrics: 'Lyrics'}, './ListeningPanePaint': {ListeningQueuePaint: 'QueuePaint', ListeningLyricsPaint: 'LyricsPaint'},
    '../../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})},
  });
  const host = createFabricHookHost(() => {
    const props: NowPlayingScreenProps = {occupant, width: 390, height: 844, onAction: command => commands.push(command), onClose() {}};
    // Follow the exported wrapper's actual touch branch without exporting or
    // copying its private implementation just to make a test seam.
    const touch = mod.NowPlayingScreen(props);
    assert.equal(typeof touch.type, 'function');
    tree = touch.type(touch.props);
  });
  await host.mount();
  const live = () => {assert.ok(scrubber, 'the actual component published an enabled scrubber'); return scrubber;};
  return {host, commands, fence, live, get occupant() {return occupant;},
    seekPaint: () => {const node = nodes(tree).find(value => value.type === 'ListeningSeekPaint'); assert.ok(node); return node;},
    async position(seconds: number) {occupant = {...occupant, playback: {...occupant.playback, positionSeconds: seconds}}; await host.rerender();},
    async presentation(change: Partial<DockOccupant['presentation']>) {occupant = {...occupant, presentation: {...occupant.presentation, ...change}}; await host.rerender();},
    async replace() {const next = dockFence('successor'); occupant = {...occupant, playback: {...occupant.playback, kind: 'music', queue: null, fence: next}, presentation: {...occupant.presentation, fence: next}}; await host.rerender(); return next;},
    async active(value: boolean) {pageActive = value; await host.rerender();},
    retirePaint() {paintCurrent = false;},
    async panel(open: boolean) {
      // Compact Now Playing offers Equalizer from its More menu; wide layouts draw a button.
      const control = nodes(tree).find(value => open ? value.props.testID === 'now-playing-equalizer' || value.props.items?.some?.((item: any) => item.id === 'equalizer') : value.type === 'Equalizer');
      assert.ok(control);
      if (!open) control.props.onBack(); else (control.props.onPress ?? control.props.items.find((item: any) => item.id === 'equalizer').onSelect)();
      await host.flush();
    },
    async close() {await host.unmount();},
  };
}

test('the actual listening accessibility callback uses configured skips, seek bounds and fallback wiring', async () => {
  const f = await fixture(false);
  try {
    assert.equal(f.seekPaint().props.fallbackAdjust, f.live().adjust);
    f.seekPaint().props.fallbackAdjust(-1);
    f.seekPaint().props.fallbackAdjust(1);
    assert.deepEqual(f.commands, [{type: 'seek', fence: f.fence, positionSeconds: 140}, {type: 'seek', fence: f.fence, positionSeconds: 180}]);
    await f.position(197); f.live().adjust(1);
    await f.position(103); f.live().adjust(-1);
    assert.deepEqual(f.commands.slice(2), [{type: 'seek', fence: f.fence, positionSeconds: 200}, {type: 'seek', fence: f.fence, positionSeconds: 100}],
      'the real seek command clamps to the retained seek window, not the full duration');
    await f.position(150); await f.presentation({skipForwardSeconds: undefined}); f.live().adjust(1);
    assert.equal(f.commands.at(-1)?.positionSeconds, 160, 'forward falls back to the configured backward step');
    await f.position(160); f.live().adjust(1);
    assert.equal(f.commands.at(-1)?.positionSeconds, 170, 'a new playback observation refreshes the callback position');
  } finally {await f.close();}
});

test('a retained accessibility callback cannot borrow a successor seek scope or changed window', async () => {
  const f = await fixture();
  try {
    const original = f.live().adjust, successor = await f.replace();
    original(1); assert.deepEqual(f.commands, []);
    const previousWindow = f.live().adjust;
    await f.presentation({seekWindow: {start: 120, end: 190}});
    previousWindow(-1); original(-1); assert.deepEqual(f.commands, []);
    f.live().adjust(1);
    assert.deepEqual(f.commands, [{type: 'seek', fence: successor, positionSeconds: 180}]);
  } finally {await f.close();}
});

test('retiring the listening paint rejects an already captured accessibility callback before rerender', async () => {
  const f = await fixture();
  try {
    const retained = f.live().adjust;
    f.retirePaint(); retained(1); retained(-1);
    assert.deepEqual(f.commands, [], 'the actual commit checks the paint permit at dispatch');
  } finally {await f.close();}
});

test('inactive pages, an open panel and unmount retire the original accessibility seek scope', async () => {
  const f = await fixture();
  try {
    const original = f.live().adjust;
    await f.active(false); original(1); assert.deepEqual(f.commands, []);
    await f.active(true); original(1); assert.deepEqual(f.commands, [], 'reactivation cannot revive the old scope');
    const beforePanel = f.live().adjust;
    await f.panel(true); beforePanel(1); assert.deepEqual(f.commands, []);
    await f.panel(false); beforePanel(-1); assert.deepEqual(f.commands, [], 'closing the panel creates its own scope');
    const current = f.live().adjust;
    current(-1); assert.deepEqual(f.commands, [{type: 'seek', fence: f.fence, positionSeconds: 140}]);
    await f.close(); current(1); assert.equal(f.commands.length, 1);
  } finally {await f.close();}
});

test('an armed pan blocks accessibility adjustment without stealing the pan binding', async () => {
  const f = await fixture();
  try {
    const scrubber = f.live(), gesture = scrubber.gesture as unknown as PanFixture;
    scrubber.travel.value = 100;
    gesture.callbacks.onBegin({x: 50});
    assert.equal(scrubber.drag.value, .5, 'the real pan callback armed a valid preview');
    scrubber.adjust(1); scrubber.adjust(-1);
    assert.deepEqual(f.commands, []);
    gesture.callbacks.onEnd({x: 75}, true);
    assert.deepEqual(f.commands, [{type: 'seek', fence: f.fence, positionSeconds: 175}], 'the original pan binding can still commit');
    gesture.callbacks.onFinalize();
    scrubber.adjust(-1);
    assert.deepEqual(f.commands.at(-1), {type: 'seek', fence: f.fence, positionSeconds: 140});
  } finally {await f.close();}
});

test('invalid configured accessibility steps never dispatch a seek', async () => {
  const f = await fixture();
  try {
    for (const step of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      await f.presentation({skipBackSeconds: step, skipForwardSeconds: step});
      f.live().adjust(-1); f.live().adjust(1);
    }
    assert.deepEqual(f.commands, []);
  } finally {await f.close();}
});
