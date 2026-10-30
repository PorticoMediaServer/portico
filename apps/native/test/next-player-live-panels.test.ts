import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import {nowNext} from '../../../packages/client-core/src/guide/now-next.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {resolvePlayerPresentation, playerControlsRevealDeadline, playerPostureCrossFadeMs} from '../src-next/platform/layout/player-presentation.ts';
import {nativeWatchTVPresentation} from '../src-next/screens/watch/tv-presentation.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const strings = (value: any): string => Array.isArray(value) ? value.map(strings).join(' ') : value?.props ? strings(value.props.children) : value == null || value === false ? '' : String(value);
const subscribe = () => () => {};
const t = (key: string, values?: Record<string, unknown>) => `${key}${values ? ` ${Object.values(values).join(' ')}` : ''}`;
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'channel', playback: {scope: {...authority, controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', channelKind: 'live-source', sourceId: 'source', channelId: 'channel', channelGeneration: 'generation', generation: 1});
const theme = {isTV: false, textScale: 1, colors: {text: '#edf5fa', textSecondary: '#bdccd6', textTertiary: '#96a9b5', accentStrong: '#9bbdce'}, type: {body: {}, caption: {}, captionStrong: {}}};
const posture = {status: 'unavailable' as const, window: null, folds: []};

for (const [name, window, expectedPane, expectedPicture] of [
  ['landscape phone', {width: 844, height: 390}, {left: 436, top: 12, width: 356, height: 366}, {left: 0, top: 0, width: 844, height: 390}],
  ['tablet', {width: 1180, height: 820}, {left: 800, top: 0, width: 380, height: 820}, {left: 0, top: 0, width: 800, height: 820}],
  ['upright phone', {width: 390, height: 844}, {left: 0, top: 219.375, width: 390, height: 624.625}, {left: 0, top: 0, width: 390, height: 219.375}],
] as const) test(`${name} hosts the mini guide in its real panel beside the one retained picture`, async () => {
  const h = hooks(), source: any = {authority, fence, retired: false, dock: {local: {fence, expanded: true, kind: 'live'}}, playback: {intent: 'playing'}, queue: {}}, controlState: any = {fence, visible: true};
  let guideState: any = {fence, open: false, phase: 'ready', rows: [], busy: false};
  const playback = {subscribe, getSnapshot: () => source}, controls = {subscribe, getSnapshot: () => controlState}, guide = {subscribe, getSnapshot: () => guideState};
  const {NativeWatchHost} = await screenModule(new URL('../src-next/screens/watch/WatchHost.tsx', import.meta.url), {
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, react: {...h.react, useLayoutEffect() {}}, 'react-native': {View: 'View', StatusBar: {}, Dimensions: {get: () => window}, useWindowDimensions: () => window},
    'react-native-reanimated': {default: {View: 'Animated'}, useSharedValue: (value: unknown) => h.react.useRef({value}).current, useAnimatedStyle: (run: () => unknown) => run(), withTiming: (value: unknown) => value, cancelAnimation() {}},
    '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
    '../../platform/layout/player-presentation': {resolvePlayerPresentation, playerControlsRevealDeadline, playerPostureCrossFadeMs}, '../../platform/platformPosture': {usePlatformPosture: () => posture},
    '../../kit/useMotion': {useMotion: () => ({reduce: true})}, '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined}, '../../kit/focus': {useOptionalFocusRegistry: () => undefined}, '../../kit/useScreenReader': {useScreenReader: () => false},
    '../../kit/overlays/TVPanelMotion': {TVPanelTransition: 'Transition', TVPanelSlide: 'Slide', TVPanelScrim: 'Scrim', useTVPanelPresence: () => ({open: false})},
    './WatchChrome': {NativeWatchChrome: 'Chrome'}, './picture-slot': {NativeWatchPicture: 'Picture'}, './HandheldMiniGuide': {NativeHandheldMiniGuide: 'HandheldMiniGuide'},
    './live-guide': {emptyLiveGuide: () => ({}), noLiveGuideSubscription: subscribe}, './tv-presentation': {nativeWatchTVPresentation},
    '../settings-live/group-port': {groupPort: () => undefined, subscribeGroupPort: subscribe},
  });
  const render = (isTV = false) => h.render(() => NativeWatchHost({authority, current: () => authority, playback, controls, outlet: {}, runtime: {text: {t}}, setPresentation() {}, isTV,
    bindings: {liveGuide: guide, chrome: () => ({}), panel: () => 'options-panel'}}));
  const before = nodes(render()).find(node => node.type === 'Picture');assert.ok(before);
  guideState = {...guideState, open: true};const opened = nodes(render()), panel = opened.find(node => node.props.testID === 'watch-panel-pane'), picture = opened.find(node => node.type === 'Picture');
  assert.deepEqual(panel.props.style, {position: 'absolute', ...expectedPane});assert.deepEqual(picture.props.style, {position: 'absolute', ...expectedPicture});
  assert.equal(picture.type, before.type);assert.equal(picture.props.fence, before.props.fence);assert.equal(opened.find(node => node.type === 'HandheldMiniGuide').props.snapshot, guideState);
  controlState.panel = 'audio';const options = nodes(render()).find(node => node.props.testID === 'watch-panel-pane');assert.match(strings(options), /options-panel/);assert.equal(nodes(options).some(node => node.type === 'HandheldMiniGuide'), false);
  controlState.panel = undefined;guideState = {...guideState, fence: {...fence, generation: 2}};assert.equal(nodes(render()).some(node => node.type === 'HandheldMiniGuide'), false, 'a stale guide cannot reserve panel space');
  guideState = {...guideState, fence};assert.equal(nodes(render(true)).some(node => node.type === 'HandheldMiniGuide'), false, 'the TV keeps its existing authored mini guide');
});

test('the handheld guide renders actual now/next rows and rejects retained actions after a changed opening', async () => {
  const h = hooks(), calls: unknown[][] = [], at = Date.now(), window = {width: 844, height: 390};
  const channel = {kind: 'live', id: 'channel', sourceId: 'source', name: 'Summit Sports Extra', number: '202', mark: {text: 'SSE', seed: 'channel'}, tuneAvailable: true};
  const programmes = [{id: 'now', title: 'Mountain Stage', start: at - 60_000, end: at + 60_000}, {id: 'next', title: 'The Locker Room', start: at + 60_000, end: at + 120_000}];
  let snapshot: any = {fence, phase: 'ready', open: true, busy: false, timezone: 'UTC', rows: [{channel, step: 0, programmes}, {channel: {...channel, id: 'unavailable', tuneAvailable: false}, step: 1, programmes: []}]};
  const guide = {getSnapshot: () => snapshot, close: (expected: unknown) => {calls.push(['close', expected]);}, fullGuide: (expected: unknown) => {calls.push(['all', expected]);}, tune: async (row: unknown, expected: unknown) => {calls.push(['tune', row, expected]);}};
  const lifecycle: unknown[][] = [];
  const {NativeHandheldMiniGuide} = await screenModule(new URL('../src-next/screens/watch/HandheldMiniGuide.tsx', import.meta.url), {
    react: h.react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {}, absoluteFillObject: {}}},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../../../../packages/client-core/src/guide/now-next': {nowNext},
    '../../../../../packages/client-core/src/presentation/kit-model/index': {channelTint: (seed: unknown) => seed}, '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: String},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays/OverlayHost': {useOverlayLifecycle: (...args: unknown[]) => lifecycle.push(args)},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': {nativeType: () => ({fontFamily: 'Manrope-Regular'}), useTheme: () => theme}, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: true})},
  });
  const context = {layout: resolvePlayerPresentation({window, posture, docked: false, panelOpen: true}), renderer: {...window}, runtime: {text: {t, locale: 'en-CA'}, artwork: () => undefined, reportError: assert.fail}};
  const render = () => h.render(() => NativeHandheldMiniGuide({context, guide, snapshot}));
  const tree = render(), all = nodes(tree), row = all.find(node => node.props.testID === 'watch-mini-guide-live/source/channel'), unavailable = all.find(node => node.props.testID === 'watch-mini-guide-live/source/unavailable');
  assert.equal(tree.props.accessibilityViewIsModal, true);assert.equal(lifecycle.at(-1)![0], true);assert.equal(lifecycle.at(-1)![6], 'watch-mini-guide');
  assert.match(strings(row), /Mountain Stage/);assert.match(strings(row), /The Locker Room/);assert.match(strings(row), /SSE/);assert.match(strings(row), /202/);assert.match(strings(row), /next.live.watching/);
  assert.equal(row.props.accessibilityState.selected, true);assert.equal(unavailable.props.disabled, true);assert.match(strings(unavailable), /guide.noInformation/);
  const progress = all.find(node => node.props.testID === 'watch-mini-guide-progress-live/source/channel');assert.ok(parseFloat(progress.props.children[0].props.style.width) >= 50 && parseFloat(progress.props.children[0].props.style.width) < 55);
  row.props.onPress();assert.deepEqual(calls.at(-1), ['tune', snapshot.rows[0], snapshot]);
  const full = all.find(node => node.props.testID === 'watch-mini-guide-all');full.props.onPress();assert.deepEqual(calls.at(-1), ['all', snapshot]);
  unavailable.props.onPress();assert.equal(calls.length, 2);
  snapshot = {...snapshot, busy: true};row.props.onPress();full.props.onPress();assert.equal(calls.length, 2, 'a retained row/footer cannot act for a new guide publication');
  const busy = nodes(render());assert.equal(busy.find(node => node.props.testID === row.props.testID).props.disabled, true);assert.equal(busy.find(node => node.props.testID === 'watch-mini-guide-all').props.disabled, true);
});

test('mobile live More opens the current real menu, details and series sheet with their original lifetime', async () => {
  const h = hooks(), calls: unknown[][] = [];let current = true;
  let sheet: any = {id: 1, kind: 'more', offer: {channel: {kind: 'live', recordAvailable: true, name: 'Summit Sports', favorite: false}, programme: {title: 'Mountain Stage'}}, lifetime: {isCurrent: () => current}, port: {recordings: {}}};
  let state: any = {sheet, busy: false};
  const actions = {subscribe, getSnapshot: () => state, close: (value: unknown) => calls.push(['close', value]), series: (value: unknown) => calls.push(['series', value]), favorite: (value: unknown) => calls.push(['favorite', value]), details: (value: unknown) => calls.push(['details', value]), seriesCreated: (value: unknown) => calls.push(['created', value])};
  const {NativeLiveActionPanels} = await screenModule(new URL('../src-next/screens/watch/LiveActionPanels.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text'}, '../../../../../packages/client-core/src/guide/legacy-source': {legacyChannel: (value: unknown) => value, legacyProgramme: (value: unknown) => value},
    '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'}, '../../kit/states': {StateView: 'State'}, '../../kit/theme': {useTheme: () => theme},
    '../settings-live/LiveScreen': {ProgrammeDetails: 'ProgrammeDetails'}, '../settings-live/LiveSeriesRule': {NativeSeriesRuleSheet: 'SeriesRuleSheet'},
  });
  const render = () => h.render(() => NativeLiveActionPanels({actions, context: {playback: {subscribe, getSnapshot: () => ({})}}, runtime: {text: {t}, reportError: assert.fail}}));
  const menu = render();assert.equal(menu.type, 'Menu');assert.equal(menu.props.description, 'Mountain Stage');assert.deepEqual(menu.props.items.map((item: any) => item.id), ['series', 'favorite', 'details']);
  menu.props.items[0].onSelect();menu.props.items[1].onSelect();menu.props.items[2].onSelect();assert.deepEqual(calls.map(call => call[0]), ['series', 'favorite', 'details']);assert.ok(calls.every(call => call[1] === sheet));
  current = false;menu.props.items[0].onSelect();menu.props.items[1].onSelect();menu.props.items[2].onSelect();menu.props.onOpenChange(false);assert.equal(calls.length, 3);assert.equal(render(), null);
  current = true;sheet = {...sheet, id: 2, kind: 'details'};state = {sheet, busy: false};const details = render();assert.equal(details.type, 'Dialog');assert.equal(details.props.title, 'Mountain Stage');
  sheet = {...sheet, id: 3, kind: 'series', defaults: {paddingStart: 60}};state = {sheet, busy: false};const series = render();assert.equal(series.type, 'SeriesRuleSheet');assert.equal(series.props.lifetime, sheet.lifetime);assert.equal(series.props.controller, sheet.port.recordings);assert.equal(series.props.defaults, sheet.defaults);
  sheet = {...sheet, id: 4, kind: 'more', offer: {...sheet.offer, channel: {...sheet.offer.channel, kind: 'library', recordAvailable: false}}};state = {sheet, busy: true};assert.deepEqual(render().props.items.map((item: any) => [item.id, item.disabled]), [['favorite', true], ['details', true]]);
});
