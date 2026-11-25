// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import * as dockModel from '../src-next/kit/dock/model.ts';
import * as shellModel from '../src-next/shell/model.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {listeningCoverFixture, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

// O-159 starting dock: drawn only until the real dock exists, Stop only, focus on Stop on TV,
// and StartTimedOut offers Try again with preferredFocus when the start deadline expires.

test('O-159 StartingDock: spinner + Stop only (no Play/Pause/progress/scrub) and TV preferredFocus on Stop', async () => {
  for (const isTV of [false, true]) {
    let stopped = 0;
    const theme = createTheme({isTV, width: isTV ? 1920 : 390});
    const {StartingDock} = await screenModule(new URL('../src-next/kit/dock/StartingDock.tsx', import.meta.url), {
      react: {...React, default: React},
      'react-native': {ActivityIndicator: 'ActivityIndicator', Text: 'Text', View: 'View'},
      '../../../../../packages/i18n/src/index': {defaultI18n},
      '../artwork': {Artwork: 'Artwork'},
      '../controls': {Focusable: 'Focusable'},
      '../Icon': {Icon: 'Icon'},
      '../theme': {useTheme: () => theme},
      '../../platform/native-blur': {BlurView: 'BlurView'},
      './visual': {alpha: (c: string) => c, dockStyles: {fill: {}, center: {}, art: {}, text: {}, tvQuiet: {}, phone: {}, tvButton: {}, phoneButton: {}}, dockType: () => ({})},
    });
    let tree: any;
    const host = createFabricHookHost(() => {
      tree = StartingDock({
        title: 'Arrival',
        icon: 'i-film',
        onStop: () => {stopped++;},
        frame: dockModel.dockBarFrame(isTV ? 'tv' : 'phone', undefined, 0, 0),
      });
    });
    try {
      await act(() => host.mount());
      const all = nodes(tree);
      assert.ok(all.some(n => n.type === 'ActivityIndicator'), 'renders ActivityIndicator spinner');
      const focusables = all.filter(n => n.type === 'Focusable');
      assert.equal(focusables.length, 1, 'Stop is the only focusable control');
      assert.equal(focusables[0].props.testID, 'playback-starting-dock.stop');
      assert.equal(focusables[0].props.preferredFocus, isTV, 'Stop takes preferredFocus on TV only');
      focusables[0].props.onPress();
      assert.equal(stopped, 1);

      const icons = all.filter(n => n.type === 'Icon').map(n => n.props.name);
      assert.ok(icons.includes('i-stop'));
      assert.equal(icons.includes('i-pause'), false, 'never shows Pause');
      assert.equal(icons.includes('i-play'), false, 'never shows Play');
      assert.equal(all.some(n => /progress|scrub/i.test(String(n.type)) || /progress|scrub/i.test(String(n.props?.testID ?? ''))), false, 'never shows progress or scrub');
    } finally {
      await host.unmount();
    }
  }
});

test('O-159 StartTimedOut: shows start error title and Try again button with preferredFocus', async () => {
  let retried = 0, closed = 0;
  const {StartTimedOut} = await screenModule(new URL('../src-next/shell/StartTimedOut.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View'},
    '../kit/controls': {Button: 'Button'},
    '../kit/overlays': {Dialog: 'Dialog'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {
    tree = StartTimedOut({
      timedOut: {key: 'timeout-1', close: () => {closed++;}, retry: async () => {retried++;}} as any,
      runtime: {text: defaultI18n, reportError(err: unknown) {throw err;}} as any,
    });
  });
  try {
    await act(() => host.mount());
    assert.equal(tree.type, 'Dialog');
    assert.equal(tree.props.title, defaultI18n.t('web.player.error.startTitle'));
    const buttons = nodes(tree.props.footer).filter(n => n.type === 'Button');
    assert.equal(buttons.length, 2);
    const tryAgain = buttons.find(b => b.props.children === defaultI18n.t('action.tryAgain'));
    const closeBtn = buttons.find(b => b.props.children === defaultI18n.t('action.close'));
    assert.ok(tryAgain, 'renders Try again button');
    assert.equal(tryAgain.props.preferredFocus, true, 'Try again has preferredFocus');
    tryAgain.props.onPress();
    assert.equal(retried, 1);
    closeBtn.props.onPress();
    assert.equal(closed, 1);
  } finally {
    await host.unmount();
  }
});

test('O-159 NativePlaybackLayer: StartingDock appears only until real dock exists, StartTimedOut on deadline, gated by authority & retired', async () => {
  const authority = {serverId: 'srv', accountId: 'acc', profileId: 'prof', epoch: '["fam",false]', viewerFence: 'v1', privacyFence: 'p1'};
  const otherAuthority = {...authority, profileId: 'other'};
  const theme = createTheme({isTV: true, width: 1920});
  const eqSnapshot = {value: undefined};
  const {NativePlaybackLayer} = await screenModule(new URL('../src-next/shell/NativePlaybackLayer.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => any) => fn()},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    '../../../../packages/player-core/src': {sameFence},
    '../../../../packages/client-core/src/audio-effects': {listeningNoticeKey: (k: string) => k},
    '../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => ({id: 'item', kind: 'video', title: 'Film', subtitle: []})},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {usePlayerMorphPublication() {}},
    '../kit/dock/InitialPicturePresentation': {InitialPicturePresentation: 'InitialPicturePresentation'},
    '../kit/dock/initial-picture-model': {initialPictureKey: () => 'init'},
    '../kit/theme': {useTheme: () => theme},
    '../kit/focus': {focusTestID: () => 'playback'},
    './tv-focus': {tvFocusIds: () => ({surface: () => 'playback'})},
    '../kit/dock': {
      DockHost: 'DockHost',
      PlayerMorph: ({children}: any) => typeof children === 'function' ? children({bounds: undefined, docked: false, subtitlesVisible: true, surfaceActive: false}) : null,
      bindNativeDock: () => async () => true,
      usePlayerMorph: () => ({dockX: {value: 0}, dockFade: {value: 1}, dockTravelFade: {value: 1}, gesture: {}}),
    },
    '../kit/dock/model': dockModel,
    '../platform/tv-keys': {useTVKeyEvent() {}},
    '../platform/tv-play-pause': {tvPlayPauseTogglesPlayback: () => false},
    '../platform/tv-play-pause-owner': {useTVPlayPauseOwner: () => () => false},
    '../kit/dock/StartingDock': {StartingDock: 'StartingDock'},
    './StartTimedOut': {StartTimedOut: 'StartTimedOut'},
    '../kit/player/ListeningSurface': {ListeningPageBackground: 'ListeningPageBackground', ListeningPagePreview: 'ListeningPagePreview'},
    '../pages/touch/player/AudioPlayerPage': {AudioPlayerPage: 'AudioPlayerPage'},
    '../pages/tv/player/ListeningRoute': {TVListeningRoute: 'TVListeningRoute'},
    '../kit/player/NowPlayingLyrics': {NowPlayingLyrics: 'NowPlayingLyrics'},
    '../screens/destinations/music/signal-path-model': {musicSignalView: () => undefined},
    '../kit/player/ListeningNotice': {NativeListeningNotice: 'NativeListeningNotice'},
    '../kit/player/PlayOnSheets': {NativePlayOnSheets: 'NativePlayOnSheets'},
    './use-listening-song': {useListeningSong: () => ({song: undefined, pending: false, favorite: false})},
    '../screens/destinations/music/signal-facts': {nativeMusicSignal: () => undefined},
    './listening-presentation': {captureListeningOpening: () => ({key: 1, scope: undefined}), expandedListeningOccupant: () => null, localListeningOccupant: () => null},
    '../platform/playback/equalizer': {nativeEqualizer: {subscribe: () => () => {}, getSnapshot: () => eqSnapshot}},
    './model': shellModel,
    './playback': {nativeShellBackTransition: () => undefined},
    './watch-slot-layout': {watchSlotInHost: () => undefined, watchSlotIsCurrent: () => false},
    './TVStartFailure': {TVStartFailure: 'TVStartFailure'},
    './TouchPlaybackPreparation': {TouchPlaybackPreparation: 'TouchPlaybackPreparation'},
    './TouchStartFailure': {TouchStartFailure: 'TouchStartFailure'},
    './TVPlaybackPreparation': {TVPlaybackPreparation: 'TVPlaybackPreparation'},
    '../kit/navigation/back': {registerBack: () => () => {}},
  });

  const listeners = new Set<() => void>();
  let snapshot: any = {
    authority,
    retired: false,
    dock: {local: null, remote: null},
    playback: {phase: 'idle', duration: 0},
    capabilities: {},
    starting: {key: 'start-1', display: {title: 'Dune', icon: 'i-film'}, stop() {}},
    startTimedOut: undefined,
  };
  const adapter: any = {
    Surface: 'Surface',
    dock: {getSnapshot: () => snapshot.dock, subscribe: () => () => {}},
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  };
  const shell: any = {text: defaultI18n, artwork: () => undefined, ambient: () => undefined, reportError() {}, getSnapshot: () => ({})};
  const publish = (next: any) => {snapshot = next; for (const listener of [...listeners]) listener();};

  let tree: any;
  const host = createFabricHookHost(() => {tree = NativePlaybackLayer({adapter, runtime: shell, authority});});
  try {
    await act(() => host.mount());
    assert.equal(nodes(tree).some(n => n.type === 'StartingDock'), true, 'StartingDock draws when starting with no local dock');
    assert.equal(nodes(tree).some(n => n.type === 'StartTimedOut'), false);

    // Real local dock replaces StartingDock in place.
    await act(() => publish({...snapshot, dock: {local: {kind: 'video', expanded: false, fence: {kind: 'queue', sessionId: 's1', generation: 1, itemId: 'm1', entryId: 'e1', playback: {scope: {}, run: 1}}}, remote: null}}));
    assert.equal(nodes(tree).some(n => n.type === 'StartingDock'), false, 'StartingDock disappears once real local dock exists');

    // Retired snapshot never draws StartingDock or StartTimedOut.
    await act(() => publish({...snapshot, dock: {local: null, remote: null}, retired: true, startTimedOut: {key: 'to-1', close() {}, retry: async () => {}}}));
    assert.equal(nodes(tree).some(n => n.type === 'StartingDock'), false, 'retired snapshot hides StartingDock');
    assert.equal(nodes(tree).some(n => n.type === 'StartTimedOut'), false, 'retired snapshot hides StartTimedOut');

    // Mismatched authority never draws StartingDock or StartTimedOut.
    await act(() => publish({...snapshot, authority: otherAuthority, retired: false, startTimedOut: {key: 'to-2', close() {}, retry: async () => {}}}));
    assert.equal(tree, null, 'other authority renders null');

    // Active matching authority with startTimedOut renders StartTimedOut.
    await act(() => publish({...snapshot, authority, retired: false, starting: undefined, startTimedOut: {key: 'to-3', close() {}, retry: async () => {}}}));
    assert.equal(nodes(tree).some(n => n.type === 'StartTimedOut'), true, 'StartTimedOut renders when timed out on current authority');
  } finally {
    await host.unmount();
  }
});
