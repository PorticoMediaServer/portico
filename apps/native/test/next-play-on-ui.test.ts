import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';

const theme = {isTV: false, platform: 'phone', textScale: 1,
  type: {body: {}, bodyStrong: {}, caption: {}, captionStrong: {}},
  colors: {text: '#edf5fa', textSecondary: '#bdccd6', textTertiary: '#96a9b5', ink: '#071018', raised: '#10212d', raisedBright: '#162c3a'}};
const translate = (key: string, values?: Record<string, unknown>) => key + (values ? JSON.stringify(values) : '');
const nativeType = (value: any) => ({fontSize: value.size, fontFamily: 'Manrope', fontWeight: String(value.weight)});
const descendants = (value: any): any[] => Array.isArray(value) ? value.flatMap(descendants) : value && typeof value === 'object'
  ? [value, ...descendants(value.props?.children)] : [];
const find = (tree: any, id: string) => descendants(tree).find(node => node.props?.testID === id);
const text = (value: any): string => Array.isArray(value) ? value.map(text).join('') : value && typeof value === 'object' ? text(value.props?.children) : value == null || value === false ? '' : String(value);
const remote = (over: any = {}) => ({id: 'remote-1', itemId: 'episode-1', title: 'Actual episode', device: 'Living room', positionSeconds: 40,
  durationSeconds: 100, positionKnown: true, playing: true, stopped: false, allows: {pause: true, resume: false, seek: true, stop: true}, ...over});

async function sheet(options: {airplay?: boolean; tablet?: boolean; state?: any} = {}) {
  const hook = hooks();
  let state: any = {open: true, receivers: [], directoryPhase: 'ready', phase: 'idle', sending: false, remoteOpen: false,
    cast: {cast: {available: true, phase: 'off', audioTracks: [], textTracks: []}}, ...options.state};
  const commands: any[][] = [];
  const controller: any = {subscribe: () => () => {}, getSnapshot: () => state,
    close: () => commands.push(['close']), closeRemote: () => commands.push(['closeRemote']), closeCastRemote: () => commands.push(['closeCastRemote']),
    castCommand: (...args: any[]) => commands.push(['cast', ...args]), command: (...args: any[]) => commands.push(['command', ...args]),
    skip: (...args: any[]) => commands.push(['skip', ...args]), pause: (...args: any[]) => commands.push(['pause', ...args]),
    resume: (...args: any[]) => commands.push(['resume', ...args]), stop: (...args: any[]) => commands.push(['stop', ...args]),
    send: (...args: any[]) => commands.push(['send', ...args]), startCast() {}, openCastRemote() {},
    cast: {viewIsCurrent: (control: any) => control === state.cast?.control, selectTrack: (...args: any[]) => commands.push(['track', ...args])}};
  const module = await screenModule(new URL('../src-next/screens/watch/PlayOnSheets.tsx', import.meta.url), {
    react: hook.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: translate}}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../kit/overlays': {Dialog: 'Dialog', BottomSheet: 'BottomSheet', Popover: 'Popover'}, '../../kit/theme': {nativeType, useTheme: () => theme},
    '../../kit/Icon': {Icon: 'Icon'}, '../../platform/device-platform': {hasAirPlay: options.airplay ?? false, isTablet: options.tablet ?? false},
    '../../platform/play-on/AudioRoutePicker': {NativeAirPlayRoutePicker: 'AirPlayPicker'}, './PlayOnRemote': {NativePlayOnRemote: 'Remote'},
  });
  const render = (props: any = {}) => hook.render(() => module.NativePlayOnSheets({controller, artwork: (path?: string) => path ? {uri: path} : undefined,
    ambient: () => undefined, airPlayVideo: true, skipBackSeconds: 15, skipForwardSeconds: 45, onLinkTV: () => commands.push(['link']), ...props}));
  return {render, commands, controller, module, set: (patch: any) => {state = {...state, ...patch};}, get: () => state};
}

test('AirPlay is the first chooser row only when the platform and actual engine permit it', async () => {
  const f = await sheet({airplay: true});
  const tree = f.render(), nodes = descendants(tree);
  assert.ok(find(tree, 'play-on-airplay-picker'));
  assert.ok(nodes.indexOf(find(tree, 'play-on-airplay')) < nodes.indexOf(find(tree, 'play-on-cast')));
  assert.equal(find(f.render({airPlayVideo: false}), 'play-on-airplay'), undefined);
  f.set({sending: true}); assert.equal(find(f.render(), 'play-on-airplay'), undefined);
  f.set({sending: false, cast: {cast: {phase: 'casting', audioTracks: [], textTracks: []}}});
  assert.equal(find(f.render(), 'play-on-airplay'), undefined);
  const android = await sheet({airplay: false});
  assert.equal(find(android.render(), 'play-on-airplay'), undefined);
});

test('the actual native AirPlay view is instantiated on supported platforms only', async () => {
  for (const supported of [true, false]) {
    const requested: string[] = [], hook = hooks();
    const {NativeAirPlayRoutePicker} = await screenModule(new URL('../src-next/platform/play-on/AudioRoutePicker.tsx', import.meta.url), {
      react: hook.react, 'react-native': {requireNativeComponent: (name: string) => {requested.push(name); return name;}},
      '../device-platform': {hasAirPlay: supported},
    });
    const node = NativeAirPlayRoutePicker({accessibilityLabel: 'AirPlay'});
    assert.deepEqual(requested, supported ? ['PorticoAudioRoutePicker'] : []);
    if (supported) {assert.equal(node.type, 'PorticoAudioRoutePicker'); assert.equal(node.props.video, true);}
    else assert.equal(node, null);
  }
});

test('tablet chooser retains the exact opener for the 360 point glass popover; phone retains its sheet', async () => {
  const anchorRef = {current: {measureInWindow() {}}};
  const tablet = await sheet({tablet: true, state: {anchorRef}});
  const popover = find(tablet.render(), 'play-on-sheet');
  assert.equal(popover.type, 'Popover'); assert.equal(popover.props.anchorRef, anchorRef);
  assert.equal(popover.props.width, 360); assert.equal(popover.props.style.borderRadius, 16);
  assert.equal(popover.props.style.backgroundColor, 'rgba(22,44,58,.96)');
  assert.ok(descendants(popover).some(node => node.type === 'ScrollView'));
  popover.props.onOpenChange(false); assert.deepEqual(tablet.commands, [['close']]);
  const phone = await sheet({state: {anchorRef}});
  assert.equal(find(phone.render(), 'play-on-sheet').type, 'Dialog');
});

test('receiver rows keep platform context, grouping and chevrons without middle-dot joins', async () => {
  const receiver = {id: 'tv', displayName: 'Living room', kind: 'portico', platform: 'tvos', presence: 'online'};
  const f = await sheet({tablet: true, state: {receivers: [receiver]}}), tree = f.render();
  assert.ok(find(tree, 'play-on-network'));
  const row = find(tree, 'play-on-tv');
  assert.ok(text(row).includes('next.cast.receiverOn') && text(row).includes('services.platform.tvos')); assert.equal(text(row).includes(' · '), false);
  assert.ok(text(find(tree, 'play-on-link')).includes('next.cast.linkTvHint'));
  assert.ok(text(find(tree, 'play-on-handover')).includes('playOn.stillHereUntilTakeover'));
  assert.ok(descendants(row).some(node => node.type === 'Icon' && node.props.name === 'i-right'));
  assert.equal(row.props.style({focused: false}).minHeight, 52);
  row.props.onPress(); assert.deepEqual(f.commands[0], ['send', receiver]);
  assert.ok(descendants(find(tree, 'play-on-link')).some(node => node.type === 'Icon' && node.props.name === 'i-tv'));
  assert.equal(f.module.playOnPlatformName('__proto__'), undefined); assert.equal(f.module.playOnPlatformName('unexpected'), undefined);
});

test('remote skips and absolute seeks use the receiver and reject a retained different-item handler', async () => {
  const f = await sheet({state: {open: false, remoteOpen: true, remote: remote()}});
  const node = f.render(); assert.equal(node.type, 'Remote');
  assert.equal(node.props.skipBackSeconds, 15); assert.equal(node.props.skipForwardSeconds, 45);
  node.props.skip(-15); node.props.seek(70);
  assert.deepEqual(f.commands, [['skip', -15, 'remote-1'], ['command', 'seek', 70, 'remote-1']]);
  f.set({remote: remote({itemId: 'episode-2'})}); node.props.skip(45); node.props.pause(); node.props.seek(12);
  assert.equal(f.commands.length, 2);
});

test('Cast seek uses the latest receiver clock, clamps its duration and carries its exact view identity', async () => {
  const control = {kind: 'cast-view', attachmentId: 'sdk-1', remote: {}};
  const state: any = {open: false, castRemoteOpen: true, cast: {resumed: {id: 'cast-1'}, control,
    cast: {available: true, phase: 'casting', itemId: 'episode-1', audioTracks: [{id: 4, name: 'English', language: 'en'}], textTracks: [{id: 7, name: 'French', language: 'fr'}]},
    receiver: {itemId: 'episode-1', positionSeconds: 40, durationSeconds: 100, paused: false}}};
  const f = await sheet({state}); const node = f.render();
  f.set({cast: {...state.cast, receiver: {...state.cast.receiver, positionSeconds: 80}}});
  node.props.skip(45); assert.deepEqual(f.commands[0], ['cast', 'seek', 100, control]);
  node.props.skip(-120); assert.deepEqual(f.commands[1], ['cast', 'seek', 0, control]);
  node.props.audio(); const withPanel = f.render();
  find(withPanel.props.panel, 'remote-track-4').props.onPress();
  assert.deepEqual(f.commands[2], ['track', 'audio', 4, control]);
  node.props.subtitles(); find(f.render().props.panel, 'remote-subtitles-off').props.onPress();
  assert.deepEqual(f.commands[3], ['track', 'subtitles', null, control]);
  f.set({cast: {...state.cast, control: {...control, attachmentId: 'sdk-2'}}}); node.props.skip(45);
  assert.equal(f.commands.length, 4);
});

test('an old stopped Cast source cannot hide a later Portico remote', async () => {
  const f = await sheet({state: {open: false, remoteOpen: true, remote: remote(), cast: {
    source: {id: 'old-cast', snapshot: {}}, cast: {phase: 'off', audioTracks: [], textTracks: []}}}});
  assert.equal(f.render().props.remote.id, 'remote-1');
});

async function remoteScreen() {
  const hook = hooks(), effects: (() => void | (() => void))[] = [];
  const module = await screenModule(new URL('../src-next/screens/watch/PlayOnRemote.tsx', import.meta.url), {
    react: {...hook.react, useEffect: (effect: () => void) => effects.push(effect)},
    'react-native': {Modal: 'Modal', ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => any) => fn()},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 50, bottom: 34})},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: (seconds: number) => String(seconds)},
    '../../../../../packages/i18n/src': {defaultI18n: {t: translate}}, '../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/overlays/OverlayHost': {useOverlayLifecycle() {}},
    '../../kit/overlays/surface': {useSurfaceMotion: (open: boolean) => ({motion: {reduce: false}, present: open, progress: {value: 1}})},
    '../../kit/theme': {nativeType, useTheme: () => theme}, '../../platform/native-blur': {BlurView: 'BlurView'}, '../../platform/device-platform': {isTablet: false},
  });
  return {module, hook, effects};
}

test('phone remote is full screen, uses configured skip labels/deltas and renders only allowed controls', async () => {
  const {module, hook} = await remoteScreen(), calls: any[] = [];
  const data = remote({entry: {kind: 'episode', id: 'episode-1', title: 'Actual episode', show: {name: 'Actual show'}, seasonNumber: 2, episodeNumber: 3},
    next: {itemId: 'episode-2', title: 'Next episode', available: true}});
  const props = {open: true, remote: data, skipBackSeconds: 15, skipForwardSeconds: 45, close() {}, seek() {},
    skip: (seconds: number) => calls.push(seconds), pause: () => calls.push('pause'), resume() {}, startOver() {}, stop() {}};
  const tree = hook.render(() => module.NativePlayOnPhoneRemote(props));
  assert.equal(tree.type, 'Modal'); assert.equal(tree.props.presentationStyle, 'fullScreen');
  assert.equal(descendants(tree).find(node => node.type === 'Artwork').props.width, 342);
  assert.ok(text(tree).includes('Actual show')); assert.ok(text(tree).includes('next.kit.episodeCode'));
  assert.ok(find(tree, 'remote-next-peek')); assert.equal(find(tree, 'remote-audio'), undefined);
  assert.equal(find(tree, 'remote-back').props.accessibilityLabel, 'player.skipBackSeconds{"seconds":15}');
  assert.equal(find(tree, 'remote-forward').props.accessibilityLabel, 'player.skipForwardSeconds{"seconds":45}');
  find(tree, 'remote-back').props.onPress(); find(tree, 'remote-forward').props.onPress(); find(tree, 'remote-pause').props.onPress();
  assert.deepEqual(calls, [-15, 45, 'pause']);
  const refused = hook.render(() => module.NativePlayOnPhoneRemote({...props, remote: remote({allows: {pause: false, resume: false, seek: false, stop: false}})}));
  for (const id of ['remote-back', 'remote-forward', 'remote-pause', 'remote-play', 'remote-stop', 'remote-restart']) assert.equal(find(refused, id), undefined, id);
});

test('remote scrubber commits once on release, clamps and invalidates a cancelled or unmounted gesture', async () => {
  const {module, hook, effects} = await remoteScreen(), positions: number[] = [];
  const props = {remote: remote(), enabled: true, seek: (position: number) => positions.push(position), skipBackSeconds: 15, skipForwardSeconds: 45};
  const tree = hook.render(() => module.NativeRemoteSeek(props));
  const cleanups = effects.map(effect => effect());
  const seek = find(tree, 'remote-seek');
  seek.props.onLayout({nativeEvent: {layout: {width: 200}}}); assert.equal(seek.props.onStartShouldSetResponder(), true);
  seek.props.onResponderGrant({nativeEvent: {locationX: 50}}); seek.props.onResponderMove({nativeEvent: {locationX: 150}});
  assert.deepEqual(positions, []);
  seek.props.onResponderRelease({nativeEvent: {locationX: 240}}); assert.deepEqual(positions, [100]);
  seek.props.onResponderGrant({nativeEvent: {locationX: 50}}); seek.props.onResponderTerminate(); seek.props.onResponderRelease({nativeEvent: {locationX: 100}});
  assert.deepEqual(positions, [100]);
  seek.props.onAccessibilityAction({nativeEvent: {actionName: 'decrement'}}); assert.deepEqual(positions, [100, 25]);
  seek.props.onResponderGrant({nativeEvent: {locationX: 50}}); for (const cleanup of cleanups) if (typeof cleanup === 'function') cleanup();
  seek.props.onResponderRelease({nativeEvent: {locationX: 100}}); assert.deepEqual(positions, [100, 25]);
  const unknown = hook.render(() => module.NativeRemoteSeek({...props, remote: remote({positionKnown: false}), enabled: false}));
  assert.equal(find(unknown, 'remote-seek').props.accessibilityValue, undefined);
  assert.equal(find(unknown, 'remote-seek').props.onStartShouldSetResponder(), false);
});

test('tablet remote preserves its compact layout and also follows configured skip amounts', async () => {
  const {module, hook} = await remoteScreen(), positions: number[] = [];
  const tree = hook.render(() => module.NativePlayOnTabletRemote({open: true, remote: remote(), skipBackSeconds: 12, skipForwardSeconds: 24,
    close() {}, seek() {}, skip: (delta: number) => positions.push(delta), pause() {}, resume() {}, startOver() {}, stop() {}}));
  assert.equal(tree.type, 'BottomSheet'); assert.deepEqual(tree.props.detents, [420]);
  find(tree, 'remote-back').props.onPress(); find(tree, 'remote-forward').props.onPress();
  assert.deepEqual(positions, [-12, 24]);
  assert.equal(find(tree, 'remote-forward').props.accessibilityLabel, 'player.skipForwardSeconds{"seconds":24}');
});
