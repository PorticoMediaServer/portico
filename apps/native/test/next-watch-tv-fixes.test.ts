import {nativeSubtitleStyleKeys, nativeSubtitleStyleValue} from '../src-next/platform/playback/subtitle-style-choices.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {watchHooks, descendants, flattenStyle} from './helpers/watch-hooks.mjs';
import * as playerCore from '../../../packages/player-core/src/index.ts';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import {createTVSeekRepeat} from '../src-next/platform/tv-seek-repeat.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {nativePostPlayNavigation} from '../src-next/screens/watch/post-play-navigation.ts';
import * as landingStoreModule from '../src-next/screens/watch/landing-store.ts';

const fence = playerCore.playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});
const t = (key: string) => key;
const i18n = {defaultI18n: {t, number: String, locale: 'en-US'}};
const theme = {isTV: true, textScale: 1.2, platform: 'tv', colors: {text: 'white', white: 'white', textSecondary: 'grey', textTertiary: 'muted'}, type: {body: {}, bodyStrong: {}, caption: {}}};
const themed = {useTheme: () => theme, nativeType: (value: any, scale: number) => ({fontSize: value.size * scale, ...(typeof value.lineHeight === 'number' ? {lineHeight: value.lineHeight * scale} : {}), fontFamily: `weight-${value.weight}`})};
const controlsAtoms = {Focusable: 'Focusable', Button: 'Button', TextField: 'TextField', Switch: 'Switch'};
const nativeAtoms = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', ActivityIndicator: 'Spinner', StyleSheet: {absoluteFill: {}}, useWindowDimensions: () => ({width: 1920, height: 1080})};
const svg = {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop', Path: 'Path', Text: 'SvgText'};
const find = (tree: any, id: string) => descendants(tree).find((node: any) => node.props.testID === id || node.props.id === id);
const gesture = () => {const value: any = {}; for (const key of ['enabled', 'minDistance', 'onBegin', 'onUpdate', 'onEnd', 'onFinalize']) value[key] = () => value; return value;};
const animation = (h: ReturnType<typeof watchHooks>) => ({default: {View: 'Animated'}, runOnJS: (f: unknown) => f, useSharedValue: (value: unknown) => h.react.useRef({value}).current,
  useAnimatedStyle: (read: () => unknown) => read(), withTiming: (value: unknown) => value, cancelAnimation() {}, Easing: {linear: (value: number) => value}});

async function timelineFixture(vega = false) {
  const h = watchHooks(), adapter = createTVKeyAdapter({source: vega ? 'vega' : 'native'}), controls = new NativeWatchControls(true);
  let key: (value: any) => void = () => {}, now = 0, serial = 0;
  const timers = new Map<number, {at: number; callback: () => void}>(), seeks: unknown[] = [], previews: (number | undefined)[] = [];
  const clock = {now: () => now, schedule: (callback: () => void, ms: number) => {const id = ++serial; timers.set(id, {at: now + ms, callback}); return id;}, cancel: (id: unknown) => {timers.delete(id as number);}};
  let source: any = {fence, retired: false, capabilities: {seek: true}, dock: {local: {expanded: true}}, playback: {positionSeconds: 100, duration: 1000, intent: 'paused'}, options: {itemId: 'item', markers: [{id: 'intro', type: 'intro', startMs: 10000, endMs: 20000}]}};
  const playback = {getSnapshot: () => source, seek: async (_fence: any, seconds: number) => {seeks.push(seconds);}, toggle: async () => {seeks.push('toggle');}};
  const props: any = {source, controls, playback, compact: false, enabled: true, focused: true, tv: true, skipBackSeconds: 5, skipForwardSeconds: 30, reportError: assert.fail, onPreview: (value: number | undefined) => previews.push(value)};
  controls.source(fence, {expanded: true, autoHide: false, hiddenByPanel: false, paused: true, scrubbing: false, focusHeld: false});
  const {NativeWatchTimeline} = await screenModule(new URL('../src-next/screens/watch/WatchTimeline.tsx', import.meta.url), {
    react: h.react, 'react-native': nativeAtoms, 'react-native-gesture-handler': {Gesture: {Pan: gesture}, GestureDetector: 'Gesture'}, 'react-native-reanimated': animation(h),
    '../../../../../packages/player-core/src': playerCore, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`}, '../../../../../packages/i18n/src': i18n,
    '../../kit/controls': controlsAtoms, '../../kit/theme': themed, './TrickplayStrip': {NativeTrickplayStrip: 'Trickplay'}, './landing-store': landingStoreModule, '../../kit/useMotion': {useMotion: () => ({timing: () => ({})})},
    '../../platform/tv-keys': {useTVKeyEvent: (handler: typeof key) => {key = handler;}}, '../../platform/tv-seek-repeat': {createTVSeekRepeat: (step: any) => createTVSeekRepeat(step, clock)},
  });
  const draw = () => {props.source = source; h.render(() => NativeWatchTimeline(props)); return h.flush();};
  draw();
  return {h, props, controls, seeks, previews, timers, draw, tree: () => h.flush(),
    key: (eventType: string, eventKeyAction: number) => {const event = adapter.read({eventType, eventKeyAction}); if (event) key(event); h.flush();},
    advance: (ms: number) => {const end = now + ms; for (;;) {const due = [...timers.entries()].sort((a, b) => a[1].at - b[1].at)[0]; if (!due || due[1].at > end) break; now = due[1].at; timers.delete(due[0]); due[1].callback(); h.flush();} now = end;},
    replace: () => {source = {...source, fence: playerCore.playbackFence({...fence, generation: 2})}; draw();},
    close: () => {h.unmount(); controls.dispose();},
  };
}

test('native release-only seek enters preview once, keeps a fixed bar, and commits once', async () => {
  const f = await timelineFixture();
  try {
    f.key('right', 1); assert.equal(f.previews.at(-1), 130);
    const tree = f.tree(), seek = find(tree, 'watch-seek');
    assert.equal(seek.props.focusTreatment, 'none');
    assert.equal(tree.props.children.props.style.height, 40);
    assert.equal(descendants(tree).filter((node: any) => node.type === 'Text').length, 0, 'Chrome alone owns marker labels');
    seek.props.onPress(); await Promise.resolve(); assert.deepEqual(f.seeks, [130, 'toggle']);
  } finally {f.close();}
});

test('held seek uses ten-second steps and accelerates, then stops on native release', async () => {
  const f = await timelineFixture();
  try {
    f.key('longRight', 0); assert.equal(f.previews.at(-1), 110);
    f.advance(2000); const atTwo = f.previews.at(-1)!; assert.equal(atTwo, 190);
    f.advance(500); assert.equal(f.previews.at(-1), atTwo + 50);
    f.key('longRight', 1); assert.equal(f.timers.size, 0); const atRelease = f.previews.at(-1); f.advance(1000); assert.equal(f.previews.at(-1), atRelease);
  } finally {f.close();}
});

for (const change of ['blur', 'disabled', 'back', 'panel', 'fence', 'unmount'] as const) test(`held seek is cancelled by ${change}`, async () => {
  const f = await timelineFixture();
  try {
    f.key('longRight', 0); assert.equal(f.timers.size, 1);
    if (change === 'blur') {f.props.focused = false; f.draw();}
    if (change === 'disabled') {f.props.enabled = false; f.draw();}
    if (change === 'back') {f.controls.back(fence); f.h.flush();}
    if (change === 'panel') {f.controls.open('options', fence);}
    if (change === 'fence') f.replace();
    if (change === 'unmount') f.h.unmount();
    const atChange = f.previews.at(-1); f.advance(1000); assert.equal(f.previews.at(-1), atChange); assert.equal(f.timers.size, 0); assert.deepEqual(f.seeks, []);
  } finally {f.close();}
});

test('Vega uses its repeated downs without a second timer or release command', async () => {
  const f = await timelineFixture(true);
  try {f.key('right', 0); f.key('right', 0); f.key('right', 0); assert.equal(f.previews.at(-1), 150); assert.equal(f.timers.size, 0);
    f.key('right', 1); assert.equal(f.previews.at(-1), 150);
  } finally {f.close();}
});

test('held seek stops at the retained-window boundary without committing playback', async () => {
  const f = await timelineFixture();
  try {f.key('longLeft', 0); f.advance(5000); assert.equal(f.previews.at(-1), 0); assert.equal(f.timers.size, 0); assert.deepEqual(f.seeks, []);}
  finally {f.close();}
});

test('Seasons has a stable sheet, selected-season entry, Done/Go identities and opener restoration', async () => {
  const h = watchHooks(), opener = {current: {}}, selected: string[] = [];
  const {NativeSeasonsSheet} = await screenModule(new URL('../src-next/screens/watch/SeasonsSheet.tsx', import.meta.url), {
    react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../kit/controls': controlsAtoms, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/theme': themed,
  });
  const props: any = {open: true, onOpenChange() {}, returnFocusRef: opener, total: 40, seasons: [{id: 'one', number: 1}, {id: 'two', number: 2}], selectedId: 'two', onSelect: (id: string) => selected.push(id), hideWatched: false, onHideWatched() {}};
  try {
    const tree = h.render(() => NativeSeasonsSheet(props));
    assert.equal(tree.props.testID, 'watch-seasons'); assert.equal(tree.props.returnFocusRef, opener);
    assert.equal(tree.props.initialFocusRef, find(tree, 'watch-season-two').props.ref);
    for (const id of ['watch-seasons-done', 'watch-seasons-go', 'watch-season-one', 'watch-season-two']) assert.equal(find(tree, id).props.focusId, id);
    find(tree, 'watch-season-one').props.onPress(); assert.deepEqual(selected, ['one']);
    props.seasons = []; const empty = h.render(); assert.equal(empty.props.initialFocusRef, find(empty, 'watch-seasons-done').props.ref);
  } finally {h.unmount();}
});

test('Episodes enters the playing row, and its season sheet returns to the actual opener', async () => {
  const h = watchHooks();
  const {NativeWatchEpisodes} = await screenModule(new URL('../src-next/screens/watch/EpisodePanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
    react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../kit/controls': controlsAtoms, '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/theme': themed,
    '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true}, './SeasonsSheet': {NativeSeasonsSheet: 'Seasons'},
    '../../kit/cards': {EpisodeTile: 'EpisodeTile'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/client-core/src/presentation/kit-model': {episodeLabel: () => ''}, '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: String},
  });
  const lease = {}, state = {workspace: {phase: 'ready', data: {selected: {seasonId: 'season'}}}, seasons: [{id: 'season', number: 1}], episodes: [{id: 'first', title: 'First'}, {id: 'playing', title: 'Playing'}], place: {hideWatched: false}};
  const controller = {getSnapshot: () => state, subscribe: () => () => {}, open: async () => {}, pageLease: () => lease};
  try {
    const tree = h.render(() => NativeWatchEpisodes({controller, currentId: 'playing', playing: true, text: {}, artwork() {}, onPlay() {}, onClose() {}}));
    // tv-player 1: a band of title-page tiles over the picture, entered on the playing episode.
    assert.equal(tree.props.entry, 'episodes-playing');
    const tiles = descendants(tree).filter((node: any) => node.type === 'EpisodeTile');
    assert.deepEqual(tiles.map((node: any) => node.props.focusId), ['episodes-first', 'episodes-playing']);
    assert.equal(tiles[1].props.width, 384); assert.equal(tiles[1].props.status, i18n.defaultI18n.t('player.miniPlaying'));
    assert.ok(tiles[1].props.menuAnchorRef && !tiles[0].props.menuAnchorRef);
    const opener = find(tree, 'watch-episodes-seasons'); opener.props.onPress();
    const open = h.flush(), sheet = descendants(open).find((node: any) => node.type === 'Seasons');
    assert.equal(sheet.props.returnFocusRef, opener.props.ref); assert.equal(sheet.props.open, true); assert.equal(open.props.enabled, false);
  } finally {h.unmount();}
});

test('Playback Info missing comparison settles on a named, focused Retry and retries explicitly', async () => {
  const h = watchHooks(); let reads = 0;
  const {NativePlaybackInfoPanel} = await screenModule(new URL('../src-next/screens/watch/PlaybackInfoPanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
    react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../../../../packages/player-core/src': playerCore,
    '../../../../../packages/client-core/src/video-track-labels': {videoTrackWords: () => ({})}, '../../../../../packages/client-core/src/presentation/playback-info': {codecName: String},
    '../../kit/controls': controlsAtoms, '../../kit/theme': themed, './playback-info': {lossSentence: () => undefined},
  });
  const context: any = {fence, source: {lease: 'lease', fence}, state: {panel: 'playback-info', fence}, runtime: {}, panelCurrent: () => true};
  const read = async () => {reads++; return undefined;};
  try {
    h.render(() => NativePlaybackInfoPanel({context, read, renderError: assert.fail})); await Promise.resolve(); const tree = h.flush();
    const retry = find(tree, 'watch-info-retry'); assert.equal(retry.props.focusId, 'watch-info-retry'); assert.deepEqual(h.focused, ['watch-info-retry']);
    h.render(); assert.deepEqual(h.focused, ['watch-info-retry'], 'ordinary rerenders do not steal focus');
    retry.props.onPress(); h.flush(); await Promise.resolve(); h.flush(); assert.equal(reads, 2);
  } finally {h.unmount();}
});

test('Playback Info timeout settles on the same focused recovery without a render failure', async context => {
  const h = watchHooks();
  const {NativePlaybackInfoPanel} = await screenModule(new URL('../src-next/screens/watch/PlaybackInfoPanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
    react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../../../../packages/player-core/src': playerCore,
    '../../../../../packages/client-core/src/video-track-labels': {videoTrackWords: () => ({})}, '../../../../../packages/client-core/src/presentation/playback-info': {codecName: String},
    '../../kit/controls': controlsAtoms, '../../kit/theme': themed, './playback-info': {lossSentence: () => undefined},
  });
  const host: any = {fence, source: {lease: 'lease', fence}, state: {panel: 'playback-info', fence}, runtime: {}, panelCurrent: () => true};
  const read = () => new Promise(() => {});
  context.mock.timers.enable({apis: ['setTimeout']});
  try {
    h.render(() => NativePlaybackInfoPanel({context: host, read, renderError: assert.fail}));
    context.mock.timers.tick(14999); assert.equal(find(h.flush(), 'watch-info-retry'), undefined);
    context.mock.timers.tick(1); assert.ok(find(h.flush(), 'watch-info-retry')); assert.deepEqual(h.focused, ['watch-info-retry']);
  } finally {h.unmount();}
});

test('post-play rows stay horizontal, preserve card widths, connect both directions and put rating after actions', async () => {
  for (const compact of [false, true]) {
    const h = watchHooks(), entry: any = {id: 'item', title: 'Movie', duration: 600, year: 2026};
    const data = {item: entry, personal: {rating: null}, related: {rows: [{entries: [{id: 'a', title: 'A'}, {id: 'b', title: 'B'}]}]}};
    const {NativeWatchPostPlay} = await screenModule(new URL('../src-next/screens/watch/PostPlayScreen.tsx', import.meta.url), {
      react: h.react, 'react-native': nativeAtoms, 'react-native-svg': svg, '../../../../../packages/i18n/src': i18n,
      '../../../../../packages/client-core/src/detail': {DetailService: class {getSnapshot = () => ({data}); subscribe = () => () => {}; dispose() {}}},
      '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': controlsAtoms, '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': themed,
    });
    try {
      const tree = h.render(() => NativeWatchPostPlay({entry, compact, current: () => true, artwork() {}, onReplay() {}, onClose() {}, onOpen() {}, reportError: assert.fail}));
      const actions = find(tree, 'watch-post'), row = find(tree, 'watch-post-related');
      assert.equal(actions.props.edges.down, row.props.id); assert.equal(row.props.edges.up, actions.props.id);
      assert.deepEqual(row.props.style, {flexDirection: 'row', gap: compact ? 10 : 28});
      const cards = descendants(row).filter((node: any) => node.type === 'Focusable');
      assert.equal(cards.length, 2); assert.equal(cards[0].props.style.width, compact ? 85 : 184);
      const art = descendants(cards[0]).find((node: any) => node.type === 'View' && node.props.style.overflow === 'hidden'); assert.equal(flattenStyle(art.props.style).borderRadius, compact ? 8 : 14);
      if (!compact) {
        const all = descendants(actions); assert.ok(all.findIndex((node: any) => node.props.testID === 'watch-post-replay') < all.findIndex((node: any) => node.props.testID === 'watch-post-star-1'));
        assert.equal(all.find((node: any) => node.type === 'Text' && node.props.children === 'Movie').props.style.fontSize, 84);
      }
    } finally {h.unmount();}
  }
});

test('TV failure keeps Retry as entry, exposes its real secondary actions and restores Details focus', async () => {
  const h = watchHooks(), calls: string[] = [];
  const {NativePlaybackFailed} = await screenModule(new URL('../src-next/screens/watch/PlaybackFailed.tsx', import.meta.url), {
    react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`},
    '../../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => undefined}, '../../kit/controls': controlsAtoms, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': themed,
    '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays': {SidePanel: 'SidePanel'},
  });
  const failure = {fence, positionSeconds: 12}, props: any = {context: {source: {}}, failure, chromeVisible: true, retry: () => calls.push('retry'), differentVersion: () => calls.push('version'), details: () => h.react.createElement('Facts', {})};
  try {
    let tree = h.render(() => NativePlaybackFailed(props)); assert.equal(find(tree, 'watch-failed').props.entry, 'watch-failed-retry');
    find(tree, 'watch-failed-version').props.onPress(); assert.deepEqual(calls, ['version']);
    const details = find(tree, 'watch-failed-details'); details.props.onPress(); tree = h.flush();
    const panel = find(tree, 'watch-failed-details-panel'); assert.equal(panel.props.open, true); assert.equal(panel.props.returnFocusRef, details.props.ref);
    assert.equal(find(tree, 'watch-failed').props.enabled, false);
    props.details = undefined; tree = h.render(); assert.equal(find(tree, 'watch-failed-details-panel').props.open, false); assert.equal(find(tree, 'watch-failed-details'), undefined);
  } finally {h.unmount();}
});

test('Episodes is contained by the held watch panel and an absent remembered option falls back to a rendered row', async () => {
  for (const panel of ['episodes', 'options']) {
    const h = watchHooks(), registry = createFocusRegistry();
    const {NativeWatchPanels} = await screenModule(new URL('../src-next/screens/watch/WatchPanels.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
      react: h.react, 'react-native': nativeAtoms, '../../../../../packages/i18n/src': i18n, '../../../../../packages/player-core/src': playerCore,
      '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`}, '../../../../../packages/client-core/src/playback-v1/v1-queue-player': {queueVideoKind: () => undefined},
      '../../../../../packages/client-core/src/subtitles': {subtitleReason: () => ''}, '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': controlsAtoms,
      '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => registry}, '../../kit/overlays': {Dialog: 'Dialog'}, '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true},
      '../../kit/overlays/OverlayHost': {useOverlayLifecycle() {}}, '../../kit/overlays/surface': {PanelBackground: 'PanelBackground'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': themed,
      '../../shell/model': {sameAuthority}, './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {nativeWatchTracks: () => ({subtitlesOff: true})}, './panel-model': {nativeWatchPanelRows: () => ({})}, './subtitle-timing': {nativeWatchSubtitleTiming: () => undefined},
    });
    const source: any = {fence, capabilities: {}, queue: {}, playback: {positionSeconds: 0}, effectiveRate: 1};
    const context: any = {source, fence, panelCurrent: () => true, state: {panel, panelFocus: 'sleep', fence, panelOffset: 0}, renderer: {width: 1920, height: 1080}, layout: {}, runtime: {text: {locale: 'en-US'}, getSnapshot: () => ({})}};
    const options = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'loading'}), load: async () => {}, clear() {}};
    try {
      const tree = h.render(() => NativeWatchPanels({context, options, playback: {getSnapshot: () => source}, names: {}, copy: {}, reportError: assert.fail, renderError: assert.fail, episodes: () => h.react.createElement('Episodes', {})}));
      const region = find(tree, 'watch-panel'); assert.equal(region.props.hold, true);
      if (panel === 'episodes') assert.equal(region.props.entryRegion, 'watch-episodes');
      else {
        assert.equal(find(tree, 'watch-option-sleep'), undefined); assert.ok(find(tree, 'watch-option-subtitles').props.ref);
        assert.deepEqual(h.focused, ['watch-option-subtitles']);
      }
    } finally {h.unmount();}
  }
});

async function chromeFixture(live = false) {
  const h = watchHooks(), registry = createFocusRegistry(), controls = new NativeWatchControls(true);
  const localFence = live ? playerCore.playbackFence({kind: 'channel', channelKind: 'live-source', channelGeneration: 'reference', playback: fence.playback, sessionId: 'live-session', channelId: 'channel', sourceId: 'input', generation: 1}) : fence;
  const source: any = {fence: localFence, retired: false, subject: {}, dock: {local: {expanded: true}}, queue: {}, capabilities: {play: true, pause: true, seek: true, subtitleTrack: true},
    playback: {phase: 'ready', intent: 'paused', positionSeconds: 100, duration: 1000, ...(live ? {linear: {name: 'Channel', media: {liveEdgeUs: '200000000'}}} : {})}};
  const liveState = {fence: localFence, offer: {channel: {kind: 'live', number: '1', recordAvailable: true}, programme: {}}};
  const liveActions = {subscribe: () => () => {}, getSnapshot: () => liveState, restoreProof: () => () => true};
  const identity = {title: 'Title', subtitle: [{id: 'episode', role: 'context', text: 'S1 E2'}, {id: 'episode-title', role: 'identity', text: 'Episode name'}]};
  const {NativeWatchChrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), {'../../kit/useScreenReader': {useScreenReader: () => false}, './landing-store': landingStoreModule, 
    react: h.react, 'react-native': nativeAtoms, 'react-native-svg': svg, 'react-native-gesture-handler': {Gesture: {Pan: gesture}, GestureDetector: 'Gesture'}, 'react-native-reanimated': animation(h),
    '../../../../../packages/player-core/src': playerCore, '../../../../../packages/client-core/src/up-next': {upNextCountdown: () => undefined, upNextOffer: () => undefined},
    '../../../../../packages/client-core/src/playback/linear-protocol': {channelSeconds: (microseconds: string) => Number(microseconds) / 1000000},
    '../../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => identity}, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`},
    '../../../../../packages/i18n/src': i18n, '../../kit/controls': controlsAtoms, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
    '../../kit/Icon': {Icon: 'Icon'}, '../settings-live/GroupPill': {NativeTVGroupPill: 'GroupPill'}, '../../kit/theme': themed,
    '../../kit/useMotion': {useMotion: () => ({duration: () => 0, timing: () => ({duration: 0})}), useReducedMotion: () => true}, './controls': {NativeWatchControls},
    './WatchTimeline': {NativeWatchTimeline: 'Timeline'}, './useSkipPromptMotion': {useSkipPromptMotion: () => ({value: undefined, interactive: false, bodyStyle: {}, positionStyle: {}})},
    '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined, usePublishedPlayerMorph: () => undefined}, './UpNextCard': {NativeUpNextCard: 'UpNextCard'}, './StillWatchingCard': {NativeStillWatchingCard: 'StillWatchingCard'}, '../../kit/artwork': {Artwork: 'Artwork'},
    './live-guide': {emptyLiveGuide: () => ({}), noLiveGuideSubscription: () => () => {}}, './live-actions': {emptyLiveActions: () => ({}), noLiveActionsSubscription: () => () => {}},
    '../../../../../packages/client-core/src/guide/now-next': {nowNext: () => ({})}, '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: () => ''},
    './TVMiniGuide': {NativeMiniGuide: 'MiniGuide'}, './TVLiveActions': {TVLiveActions: 'LiveActions'}, './TVControlLabel': {TVControlLabel: 'Label'}, '../../platform/tv-keys': {useTVKeyEvent() {}},
  });
  controls.source(localFence, {expanded: true, autoHide: false, hiddenByPanel: false, paused: true, scrubbing: false, focusHeld: false});
  const props: any = {source, controls, playback: {getSnapshot: () => source}, width: 1920, height: 1080, compact: false, text: {locale: 'en-US'}, quality: 'Original', subtitle: 'A very long subtitle name', skipBackSeconds: 10, skipForwardSeconds: 30,
    skipModes: {intro: 'off', recap: 'off', credits: 'off'}, reportError: assert.fail, ...(live ? {liveActions, goLive() {}} : {})};
  h.render(() => NativeWatchChrome(props));
  return {h, tree: () => h.flush(), close: () => {h.unmount(); controls.dispose();}};
}

test('TV chrome uses the scaled type, authored shadow layers, glass option states and shared identity row', async () => {
  const f = await chromeFixture();
  try {
    let tree = f.tree(); const all = descendants(tree);
    const title = all.find((node: any) => node.type === 'Text' && node.props.children === 'Title');
    assert.equal(flattenStyle(title.props.style).fontSize, 52.8); assert.equal(flattenStyle(title.props.style).textShadowRadius, 24);
    const episode = all.find((node: any) => node.type === 'Text' && node.props.children === 'Episode name');
    assert.equal(flattenStyle(episode.props.style).fontFamily, 'weight-700'); assert.equal(flattenStyle(episode.props.style).color, 'white');
    assert.equal(flattenStyle(episode.props.style).lineHeight, 38.4);
    const shadows = all.filter((node: any) => node.type === 'Svg' && typeof node.props.height === 'number');
    assert.deepEqual(shadows.map((node: any) => node.props.height), [400, 600]);
    assert.deepEqual(shadows.map((node: any) => descendants(node).filter((child: any) => child.type === 'Stop').map((child: any) => child.props.stopOpacity)), [[.88, .6, .2, 0], [.96, .84, .46, 0]]);
    const option = find(tree, 'watch-option-subtitles');
    assert.equal(option.props.style({focused: false}).backgroundColor, 'rgba(237,245,250,.14)');
    assert.equal(option.props.style({focused: true}).backgroundColor, 'rgba(237,245,250,.28)');
    assert.equal(option.props.style({focused: true}).height, 64); assert.equal(option.props.style({focused: true}).maxWidth, 200); assert.equal(option.props.style({focused: true}).minWidth, 92);
    const focusedValue = descendants(option.props.children({focused: true})).find((node: any) => node.props.children === 'A very long subtitle name');
    assert.equal(flattenStyle(focusedValue.props.style).fontSize, 24); assert.equal(flattenStyle(focusedValue.props.style).color, 'white');
    all.find((node: any) => node.type === 'GroupPill').props.onHost(44); tree = f.tree();
    const row = descendants(tree).find((node: any) => Array.isArray(node.props.children) && node.props.children.some((child: any) => child?.props?.id === 'watch-group'));
    assert.equal(row.props.style.flexDirection, 'row'); assert.equal(find(tree, 'watch-group').props.style.marginLeft, 28);
    assert.equal(find(tree, 'watch-group').props.edges.down, 'watch-seek-region');
  } finally {f.close();}
});

test('Live Record/More and Go Live have reciprocal paths to seek and transport without a group pill', async () => {
  const f = await chromeFixture(true);
  try {
    const tree = f.tree(), seek = find(tree, 'watch-seek-region'), actions = find(tree, 'watch-live-actions'), edge = find(tree, 'watch-live-edge');
    assert.equal(seek.props.stops.up, false); assert.equal(seek.props.edges.up, actions.props.id); assert.equal(actions.props.edges.down, seek.props.id);
    assert.equal(seek.props.edges.down, edge.props.id); assert.equal(edge.props.edges.up, seek.props.id); assert.equal(edge.props.edges.down, 'watch-transport');
    assert.equal(find(tree, 'watch-transport').props.edges.up, edge.props.id);
  } finally {f.close();}
});

async function failureModuleFixture(owner = true) {
  const h = watchHooks(), authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  let route = {revision: 1, authority}, role = owner ? 'owner' : 'member', live = true;
  let source: any = {fence, lease: 'lease', retired: false, dock: {local: {fence, expanded: true}}, capabilities: {retry: true},
    playback: {phase: 'error', error: 'private native diagnostic'}, entry: {id: 'item', title: 'Title', playback: {itemId: 'item'}},
    session: {id: fence.sessionId, itemId: 'item', versionId: 'version', playbackInfo: {route: 'converted', network: 'away'}}, options: {itemId: 'item', versions: [{id: 'version'}, {id: 'alternative'}]}};
  let player: any; const captures: any[] = [], versions: any[] = [], retries: any[] = [];
  const context: any = {current: () => live, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local', role}}), api: {}, subscribeAuthority: () => () => {},
    navigation: {getSnapshot: () => route, subscribe: () => () => {}}, playback: {getSnapshot: () => source, subscribe: () => () => {}, retry: async (...args: unknown[]) => {retries.push(args);}}};
  const {createNativeWatchModule} = await screenModule(new URL('../src-next/screens/watch/module.tsx', import.meta.url), {
    react: h.react, 'react-native': {Platform: {isTV: true}}, '../settings-live/group-port': {groupPort: () => undefined, subscribeGroupPort: () => () => {}}, '../../platform/device-settings': {},
    '../../../../../packages/client-core/src/audiobook-preferences': {}, '../../../../../packages/player-core/src': playerCore, '../../../../../packages/i18n/src': i18n, '../../../../../packages/client-core/src/playback-v1/quality': {},
    './factory': {createNativeWatchScreens: (ports: any) => {player = ports.player; return {watch: {}, dispose() {}, capture: (entry: any, current: () => boolean) => {const value = {entry, current}; captures.push(value); return value;}, versions: async (...args: unknown[]) => {versions.push(args);}};}},
    './PlaybackInfoPanel': {playbackInfoWords: () => ({network: (value: string) => value})}, './WatchPanels': {}, './panel-options': {NativeWatchPanelOptions: class {dispose() {}}}, '../../shell/model': {sameAuthority},
    './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {}, './ChannelRoute': {}, './live-guide': {NativeTVLiveGuide: class {dispose() {}}}, './live-actions': {NativeTVLiveActions: class {dispose() {}}}, './LiveActionPanels': {},
    './PlaybackFailed': {NativePlaybackFailed: 'Failure'}, './HandheldPlaybackFailed': {}, './PostPlayScreen': {}, '../../platform/device-platform': {}, './panel-names': {}, '../../platform/server-preferences': {},
    '../../../../../packages/client-core/src/presentation/browse-toolbar': {}, './system-media': {bindNativeVideoSystemMedia: () => () => {}}, './post-play-navigation': {nativePostPlayNavigation},
    '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: () => 'Public playback message'}, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`},
    '../../kit/states/DetailsPanel': {DetailsPanel: 'DetailsPanel'}, '../../platform/playback/subtitle-style-choices': {nativeSubtitleStyleKeys, nativeSubtitleStyleValue},
  });
  const module = createNativeWatchModule(context, {title: {owner, reportError: assert.fail}, openVersions() {}, beginVersions() {}, startVersion: async () => {}});
  const failure = {fence, sessionId: fence.sessionId, positionSeconds: 12};
  return {captures, versions, retries, draw: () => player.failed({runtime: {reportError: assert.fail}}, failure, true), actionable: () => player.failureAction({}, failure),
    source: () => source, change: (next: any) => {source = next;}, revoke: () => {role = 'member';}, route: () => {route = {...route, revision: 2};}, retire: () => {live = false;}, close: () => module.dispose()};
}

test('module exposes owner facts and actual alternate versions, without making ownership a version requirement', async () => {
  for (const owner of [true, false]) {
    const f = await failureModuleFixture(owner);
    try {
      const props = f.draw().props; assert.equal(typeof props.retry, 'function'); assert.equal(typeof props.differentVersion, 'function'); assert.equal(!!props.details, owner);
      if (owner) {const panel = props.details(); assert.equal(panel.type, 'DetailsPanel'); assert.equal(panel.props.facts[0].value, 'Public playback message'); assert.deepEqual(panel.props.facts.map((row: any) => row.id), ['reason', 'position', 'delivery', 'network']);}
      props.differentVersion(); assert.equal(f.versions.length, 1); assert.equal(f.versions[0][1], 'version'); assert.equal(f.captures[0].entry.id, 'item');
      f.change({...f.source(), playback: {phase: 'ready'}, fence: {...fence, generation: 2}});
      assert.equal(f.captures[0].current(), true, 'the chosen version may change its own phase and output while the original host stays open');
      props.differentVersion(); assert.equal(f.versions.length, 1, 'the retained failure button cannot open for the replacement');
      f.route(); assert.equal(f.captures[0].current(), false);
    } finally {f.close();}
  }
});

test('failure facts retire immediately when owner rights or the original route retire', async () => {
  const f = await failureModuleFixture();
  try {const details = f.draw().props.details; assert.ok(details()); f.revoke(); assert.equal(details(), null); f.route(); assert.equal(details(), null);}
  finally {f.close();}
});

test('failure bar edges follow real recovery actions and no invented alternate is offered', async () => {
  const f = await failureModuleFixture(false);
  try {
    f.change({...f.source(), capabilities: {retry: false}}); assert.equal(f.actionable(), true, 'the real version action remains reachable without Retry');
    f.change({...f.source(), options: {itemId: 'item', versions: [{id: 'version'}]}}); assert.equal(f.draw().props.differentVersion, undefined); assert.equal(f.actionable(), false);
    f.change({...f.source(), options: {itemId: 'item', versions: [{id: 'version'}, {id: 'alternative'}, {id: 'alternative'}]}}); assert.equal(f.draw().props.differentVersion, undefined);
    f.change({...f.source(), entry: undefined}); assert.equal(f.draw().props.differentVersion, undefined, 'a channel or missing entry cannot invent a version target');
  } finally {f.close();}
});
