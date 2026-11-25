import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {parseOptions, parseSession} from '@core/playback-v1/types.ts';
import type {PlayerOffersData} from '@core/player-offers.ts';
import type {SubtitlePlan, SubtitleResource} from '@core/subtitles.ts';
import type {Room} from '@core/room-protocol.ts';
import {queueScope, playbackFence, sameFence, type QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, textOf, tree} from './helpers/interaction-harness.mjs';
import {watchPanelModel} from '../src/screens/watch/panel-model.ts';
import {ListeningOwner} from '../src/screens/watch/listening/listening-owner.ts';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../src/platform/playback';

const css = {default: new Proxy({}, {get: (_target, key) => String(key)})};
const scope = queueScope({serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'});
const fence: QueuePlaybackFence = playbackFence({kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'movie', sourceId: 'source', entryId: 'entry', generation: 1});

function subtitleSnapshot(mode: 'track' | 'off' = 'track', offsetUs = '-500000'): WebPlaybackRuntimeSnapshot {
  const r1: SubtitleResource = {id: 'sub-1', sourceId: 'source', revision: 1, scope: 'personal', language: 'en', title: 'English', format: 'srt', origin: 'sidecar', rights: 'owned', offsetUs, canManage: true, enabled: true, renderer: 'external_text', default: false, forced: false};
  const r2: SubtitleResource = {...r1, id: 'sub-2', language: 'es', title: 'Spanish', default: true};
  const plan: SubtitlePlan = {version: 1, sessionId: 'session', generation: 1, sourceId: 'source', revision: 1, catalogRevision: 1, renderer: 'external_text', mode, offAvailable: true, offsetUs, selected: mode === 'track' ? r1 : null, resources: [r1, r2], discovered: []};
  return {
    authority: {...scope, epoch: 1, viewerFence: 'vf'}, sequence: 1, disposed: false, fence, nativeVideoSurfaceActive: false,
    playback: {playbackRun: 1, intentId: 'i1', phase: 'playing', intent: 'playing'} as never,
    dock: {local: {kind: 'video', fence, expanded: true}} as never,
    presentations: [], queueView: null, session: undefined,
    options: {itemId: 'movie', kind: 'movie', versions: [], qualities: [], markers: [], chapters: [{title: 'Intro', startMs: 0}, {title: 'Act 1', startMs: 60_000}, {title: 'Act 2', startMs: 180_000}]},
    offers: {subtitlePlan: plan} as unknown as PlayerOffersData,
    presentation: {fence, subject: {kind: 'video', item: {id: 'movie', kind: 'movie', title: 'Film'}}, kind: 'video', durationSeconds: 600, positionSeconds: 90, playing: true, expanded: true, finished: false, controls: ['play', 'pause', 'seek', 'volume', 'next', 'previous'], volume: 0.5, muted: false, rate: 1, policy: null},
  };
}

test('Task 1a: GlobalShortcuts routes / to visible search input or /search navigation and respects focus/surface guards', async () => {
  const h = createRenderer();
  const navigations: unknown[] = [];
  const mod = await componentModule(new URL('../src/shell/GlobalShortcuts.tsx', import.meta.url), {
    react: h.react,
    '../kit/states': {KeyboardShortcuts: 'KeyboardShortcuts'},
    '../router': {router: {navigate: async (dest: unknown) => { navigations.push(dest); }}},
  });
  const makeDoc = (surfaces: unknown[] = [], searchInputs: unknown[] = []) => ({
    querySelectorAll(selector: string) {
      if (selector.includes('input[type="search"]')) return searchInputs;
      return surfaces;
    },
  });
  const baseEvent = {defaultPrevented: false, repeat: false, ctrlKey: false, metaKey: false, altKey: false, key: '/', target: null};
  assert.equal(mod.globalShortcutFor(baseEvent, makeDoc()), 'search');
  assert.equal(mod.globalShortcutFor({...baseEvent, key: '?'}, makeDoc()), 'help');
  assert.equal(mod.globalShortcutFor({...baseEvent, ctrlKey: true}, makeDoc()), null);
  assert.equal(mod.globalShortcutFor({...baseEvent, metaKey: true}, makeDoc()), null);
  assert.equal(mod.globalShortcutFor({...baseEvent, altKey: true}, makeDoc()), null);
  assert.equal(mod.globalShortcutFor({...baseEvent, repeat: true}, makeDoc()), null);
  const visibleSurface = {closest: () => null, getClientRects: () => [{width: 100}]};
  const hiddenSurface = {closest: (s: string) => s.includes('hidden') ? {} : null, getClientRects: () => []};
  assert.equal(mod.globalShortcutFor(baseEvent, makeDoc([visibleSurface])), null);
  assert.equal(mod.globalShortcutFor(baseEvent, makeDoc([hiddenSurface])), 'search');
  let focused = 0, selected = 0;
  const searchInput = {disabled: false, closest: () => null, getClientRects: () => [{width: 200}], focus: () => { focused++; }, select: () => { selected++; }};
  assert.equal(mod.focusVisibleSearchInput(makeDoc([], [searchInput])), true);
  assert.equal(focused, 1);
  assert.equal(selected, 1);
  assert.equal(mod.focusVisibleSearchInput(makeDoc([], [])), false);
});

test('Task 1b: WatchScreen wires C, G, H, PageUp/PageDown (VOD) and ArrowUp/ArrowDown/G (Live TV)', async () => {
  const windowListeners = new Map<string, Set<(event: any) => void>>();
  const oldWindow = globalThis.window;
  let guideClicks = 0;
  const guideButton = {textContent: defaultI18n.t('player.live.guide'), click() { guideClicks++; }};
  const h = createRenderer({element: {querySelectorAll: () => [guideButton]}});
  globalThis.window = {
    innerWidth: 1440, innerHeight: 810,
    addEventListener(type: string, fn: (event: any) => void) { (windowListeners.get(type) ?? windowListeners.set(type, new Set()).get(type)!).add(fn); },
    removeEventListener(type: string, fn: (event: any) => void) { windowListeners.get(type)?.delete(fn); },
  } as unknown as Window & typeof globalThis;
  try {
    const watchMod = await componentModule(new URL('../src/screens/watch/WatchScreen.tsx', import.meta.url), {
      react: h.react, '@i18n': {defaultI18n},
      '../../../../packages/player-core/src/index.ts': {sameFence},
      '../../kit/Icon': {Icon: 'Icon'}, '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button'},
      '../../kit/frames': {PlayerChromeFrame: 'PlayerChromeFrame'}, '../../kit/player': {PlayerControl: 'PlayerControl', PlayerPill: 'PlayerPill', PlayerTitle: 'PlayerTitle', PlayerVolume: 'PlayerVolume'},
      '../../kit/overlays': {Menu: 'Menu', PanelPresence: 'PanelPresence', useLeavingPresence: (v: unknown) => ({shown: v, leaving: false})},
      '../../kit/states': {KeyboardShortcuts: 'KeyboardShortcuts'}, '../../kit/media': {LossNotice: 'LossNotice', SeekLine: 'SeekLine'},
      './ChaptersDrawer': {WatchChaptersDrawer: 'WatchChaptersDrawer'},
      './model': await import('../src/screens/watch/model.ts'),
      './screen-state': await import('../src/screens/watch/screen-state.ts'),
      './video-controls-focus': await import('../src/screens/watch/video-controls-focus.ts'),
      '../../kit/overlays/shared': {useReducedMotion: () => false}, './watch.module.css': css,
      '../../kit/states/PlaybackWaiting': {PlaybackWaiting: 'PlaybackWaiting', playbackWaiting: () => ({})},
      '../../shell/playback-model': {fenceKey: () => 'k'},
      '../../kit/states/PlaybackFailure': {PlaybackFailure: 'PlaybackFailure'},
      './ScrubPreview': {ScrubPreview: 'ScrubPreview'}, './preview': {watchPreviewFrame: () => null},
      '../../kit/theme': {useCoarsePointer: () => false}, './picture': {WatchPictureInput: class { cancel() {} }},
      './queue-neighbours': {watchQueueNeighbours: () => ({})}, './dock-panel-opening': {WatchDockPanelOpening: class {}},
      '../../kit/player/EndCard': {PlayerEndCard: 'PlayerEndCard'}, '../../kit/player/PlayerLive': {PlayerLive: 'PlayerLive'},
      '../../kit/player/LivePage': {LivePage: 'LivePage'}, './prompt-focus': {WatchPromptFocus: {Provider: 'Provider'}},
      './buffered-ranges': {bufferedSegments: () => []},
    });
    const active = subtitleSnapshot('track', '-500000');
    assert.equal(watchMod.watchSubtitleToggleTarget(active), null);
    assert.equal(watchMod.watchSubtitleToggleTarget(subtitleSnapshot('off', '0'), 'sub-1'), 'sub-1');
    assert.equal(watchMod.watchSubtitleToggleTarget(subtitleSnapshot('off', '0')), 'sub-2');
    assert.equal(watchMod.watchSubtitleTimingNextMs(active, -100), -600);
    assert.equal(watchMod.watchSubtitleTimingNextMs(active, 100), -400);
    assert.equal(watchMod.watchSubtitleTimingNextMs(subtitleSnapshot('off', '0'), 100), null);
    assert.equal(watchMod.watchSubtitleTimingNextMs(subtitleSnapshot('track', '600000000'), 100), null);
    assert.deepEqual(watchMod.watchChapterStepIntent(active, -1), {kind: 'seek', seconds: 0});
    assert.deepEqual(watchMod.watchChapterStepIntent(active, 1), {kind: 'seek', seconds: 180});
    const noChapters = {...active, options: {...active.options!, chapters: []}};
    assert.deepEqual(watchMod.watchChapterStepIntent(noChapters, -1), {kind: 'previous'});
    assert.deepEqual(watchMod.watchChapterStepIntent(noChapters, 1), {kind: 'next'});

    // Exercise live keydown wiring on mounted WatchScreen for VOD and Live TV.
    const calls: unknown[] = [];
    const runtime: any = {
      getSnapshot: () => active, bufferedRanges: () => [], speedChoices: () => [1], canStepFrame: () => false,
      onAction: async (action: unknown) => { calls.push(['action', action]); },
      selectSubtitle: async (_f: unknown, id: string | null) => { calls.push(['subtitle', id]); },
      setSubtitleOffset: async (_f: unknown, ms: number) => { calls.push(['timing', ms]); },
    };
    const binding: any = {runtime, artwork: {foreground: () => undefined}, environment: defaultI18n, skips: {back: 10, forward: 30}, reportError(e: unknown) { throw e; }};
    const renderedVod = tree(h.render(() => watchMod.WatchScreen({binding, snapshot: active, expanded: true, onCollapse() {}, surfaceSlotRef: {current: null}})));
    const vodRoot = renderedVod.find((n: any) => n.props?.['data-watch-screen']);
    if (typeof vodRoot?.props?.ref === 'function') vodRoot.props.ref({querySelectorAll: () => [guideButton]});
    const fire = (key: string) => {
      let prevented = false;
      for (const fn of [...(windowListeners.get('keydown') ?? [])]) {
        fn({key, shiftKey: false, ctrlKey: false, metaKey: false, altKey: false, defaultPrevented: false, target: null, preventDefault() { prevented = true; }});
      }
      return prevented;
    };
    assert.equal(fire('c'), true);
    assert.equal(fire('g'), true);
    assert.equal(fire('h'), true);
    assert.equal(fire('PageDown'), true);
    assert.deepEqual(calls, [
      ['subtitle', null],
      ['timing', -600],
      ['timing', -400],
      ['action', {kind: 'seek', seconds: 180, fence}],
    ]);
    h.dispose();

    // Now Live TV mode: ArrowUp/ArrowDown tune channels and G toggles mini-guide.
    const tuned: number[] = [];
    const livePort: any = {fence, isCurrent: () => true, up: {}, down: {}, tune: async (dir: number) => { tuned.push(dir); }, openRecordings() {}};
    const renderedLive = tree(h.render(() => watchMod.WatchScreen({binding, snapshot: active, expanded: true, onCollapse() {}, surfaceSlotRef: {current: null}, live: livePort})));
    const liveRoot = renderedLive.find((n: any) => n.props?.['data-watch-screen']);
    if (typeof liveRoot?.props?.ref === 'function') liveRoot.props.ref({querySelectorAll: () => [guideButton]});
    assert.equal(fire('ArrowUp'), true);
    assert.equal(fire('ArrowDown'), true);
    assert.equal(fire('g'), true);
    assert.deepEqual(tuned, [1, -1]);
    assert.equal(guideClicks, 1);
    h.dispose();
  } finally {
    globalThis.window = oldWindow;
  }
});

test('Task 2: AudiobookListening reads web sleepTimer endsAt, counts down with chained setTimeout, and caches Intl.NumberFormat', async context => {
  context.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1_000_000});
  const h = createRenderer();
  const mod = await componentModule(new URL('../src/screens/watch/listening/AudiobookListening.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n},
    '../../../kit/artwork': {ArtworkGlow: 'ArtworkGlow', GeneratedCover: 'GeneratedCover', WholeArtwork: 'WholeArtwork'},
    '../../../kit/controls': {Button: 'Button'},
    '../../../kit/frames': {AudioPlayerFrame: 'AudioPlayerFrame', StateRow: 'StateRow'},
    '../../../kit/player': {AudioPanelSurface: 'AudioPanelSurface', PlayerVolume: 'PlayerVolume'},
    '../../../kit/media': {SeekLine: 'SeekLine'},
    '../../../kit/overlays': {Banner: 'Banner'}, '../../../kit/Icon': {Icon: 'Icon'},
    '../model': await import('../src/screens/watch/model.ts'),
    './BookChapterList': {BookChapterList: 'BookChapterList', playingBookChapter: () => undefined},
    './audiobook-listening.module.css': css,
    './listening-owner': await import('../src/screens/watch/listening/listening-owner.ts'),
  });
  const snapshot: any = {disposed: false, dock: {}, playback: {}, presentation: {kind: 'audiobook', expanded: true, playing: true, positionSeconds: 10, durationSeconds: 3600, rate: 1.5, volume: 1, muted: false, controls: ['seek', 'pause']}};
  const noop = () => {};
  const endsAt = Date.now() + 15 * 60_000;
  const render = () => tree(h.render(() => mod.AudiobookListening({
    snapshot, title: 'Book', subtitle: 'Author', chapters: null, speedChoices: [1, 1.5], sleepChoices: [0, 15, 30], sleepChoice: 15, sleepEndsAt: endsAt,
    skips: {back: 15, forward: 30}, onIntent: noop, onSpeed: noop, onSleep: noop, onChapter: noop, onChapterPage: noop, onChapterRetry: noop, onCollapse: noop,
  })));
  const first = all(render(), 'AudioPlayerFrame')[0]!;
  assert.match(String(first.props.sleep.help), /15 min/);
  context.mock.timers.tick(5 * 60_000);
  const later = all(render(), 'AudioPlayerFrame')[0]!;
  assert.match(String(later.props.sleep.help), /10 min/);
  assert.equal(mod.formatAudiobookRate(1.25, 'en-US'), '1.25');
  assert.equal(mod.sleepCountdownDelayMs(Date.now() + 30_000, Date.now()), 1000);
  assert.equal(mod.sleepCountdownDelayMs(Date.now() - 1, Date.now()), null);
  h.dispose();

  let timerValue: {minutes: number; endsAt: number} | undefined = {minutes: 15, endsAt: 1_900_000};
  const listeners = new Set<() => void>();
  const bookFence = playbackFence({kind: 'queue', playback: {scope, run: 2}, sessionId: 's', generation: 1, itemId: 'b', entryId: 'e', sourceId: 'src'});
  const liveSnap: any = {authority: {...scope, epoch: 1, viewerFence: 'vf'}, sequence: 1, disposed: false, fence: bookFence,
    playback: {playbackRun: 2}, presentation: {kind: 'audiobook', fence: bookFence, expanded: true}, dock: {local: {kind: 'audiobook', fence: bookFence, expanded: true}}};
  const owner = new ListeningOwner({
    runtime: {
      getSnapshot: () => liveSnap, subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); },
      open() {}, collapse() {}, speedChoices: () => [1], setSpeed() {},
      sleepTimer: () => timerValue, setSleepTimer: async () => {},
      onAction: async () => {}, captureListening: () => undefined,
    } as unknown as WebShellPlaybackRuntime,
    snapshot: liveSnap, isCurrent: () => true, lyricsEnabled: false,
  });
  assert.equal(owner.sleepEndsAt(), 1_900_000);
  timerValue = undefined;
  for (const l of listeners) l();
  assert.equal(owner.sleepEndsAt(), undefined);
  owner.dispose();
});

test('Task 3: LyricsPanel auto-scrolls active synced line, respects reduced motion, and yields to manual scroll', async () => {
  const listeners = new Map<string, Set<() => void>>();
  const h = createRenderer({
    element: {
      addEventListener: (type: string, fn: () => void) => { (listeners.get(type) ?? listeners.set(type, new Set()).get(type)!).add(fn); },
      removeEventListener: (type: string, fn: () => void) => { listeners.get(type)?.delete(fn); },
    },
  });
  const mod = await componentModule(new URL('../src/screens/watch/listening/LyricsPanel.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/overlays': {Banner: 'Banner'},
    './listening-panel.module.css': css,
  });
  const scrollCalls: unknown[] = [];
  const doc = {format: 'lrc', text: 'One\nTwo', embeddedOffsetMs: 0, lines: [{atMs: 0, text: 'One'}, {atMs: 5000, text: 'Two'}]};
  const resource = {id: 'r1', revision: 1, offsetMs: 0, language: 'en', provenance: {label: 'Local'}, document: doc};
  const snapshot: any = {busy: false, generation: 1, error: '', data: {selection: {resource}, source: {startSeconds: 0, duration: 60}, resources: [resource]}};
  const lineNode = {scrollIntoView: (opts?: unknown) => { scrollCalls.push(opts); }};
  const oldMatchMedia = globalThis.matchMedia;
  globalThis.matchMedia = (() => ({matches: false})) as never;
  try {
    const mount = (pos: number) => {
      const rendered = tree(h.render(() => mod.LyricsPanel({snapshot, positionSeconds: pos, enabled: true, canSeek: true, onRetry: () => {}, onSeek: () => {}})));
      const activeButton = rendered.find((n: any) => n.props?.['aria-current'] === 'true');
      if (typeof activeButton?.props?.ref === 'function') activeButton.props.ref(lineNode);
    };
    // First render attaches line ref; advancing to line 2 scrolls smoothly into view.
    mount(1);
    mount(6);
    assert.deepEqual(scrollCalls.at(-1), {behavior: 'smooth', block: 'center', inline: 'nearest'});
    // Reduced motion switches to behavior: 'auto'.
    globalThis.matchMedia = (() => ({matches: true})) as never;
    mount(1);
    assert.deepEqual(scrollCalls.at(-1), {behavior: 'auto', block: 'center', inline: 'nearest'});
    // Manual wheel pauses auto-scroll.
    const countBefore = scrollCalls.length;
    for (const fn of listeners.get('wheel') ?? []) fn();
    mount(6);
    assert.equal(scrollCalls.length, countBefore);
    h.dispose();
  } finally {
    globalThis.matchMedia = oldMatchMedia;
  }
});

test('Task 4: StartingPlayer checks actual expanded player surfaces instead of non-existent [data-watch-expanded]', async () => {
  const h = createRenderer();
  const mod = await componentModule(new URL('../src/screens/watch/StartingPlayer.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button'}, './starting-player.module.css': css,
  });
  const oldDoc = globalThis.document, oldHtml = globalThis.HTMLElement;
  class FakeEl { isConnected = true; focused = 0; focus() { this.focused++; } }
  globalThis.HTMLElement = FakeEl as unknown as typeof HTMLElement;
  let expandedMatch: unknown = null;
  let lastSelector = '';
  const origin = new FakeEl();
  globalThis.document = {activeElement: origin, querySelector(sel: string) { lastSelector = sel; return expandedMatch; }} as unknown as Document;
  try {
    const props = {starting: {key: 'k1', display: {subject: {kind: 'video', item: {id: '1', kind: 'movie', title: 'Film'}}}, stop() {}}, artwork: {foreground: () => undefined}, environment: defaultI18n};
    expandedMatch = {};
    h.render(() => mod.StartingPlayer(props));
    h.dispose();
    assert.match(lastSelector, /data-watch-screen/);
    assert.doesNotMatch(lastSelector, /data-watch-expanded/);
    assert.equal(origin.focused, 0);
    expandedMatch = null;
    h.render(() => mod.StartingPlayer(props));
    h.dispose();
    assert.equal(origin.focused, 1);
  } finally {
    globalThis.document = oldDoc;
    globalThis.HTMLElement = oldHtml;
  }
});

test('Task 5: SeekLine keyboard steps by 5s (10s with Shift) and uses whole-second step / aria-valuenow', async () => {
  const h = createRenderer();
  const mod = await componentModule(new URL('../src/kit/media/SeekLine.tsx', import.meta.url), {
    react: h.react, './seek-line.module.css': css,
  });
  assert.equal(mod.seekLineKeyboardValue('ArrowRight', false, 20, 0, 100), 25);
  assert.equal(mod.seekLineKeyboardValue('ArrowLeft', false, 20, 0, 100), 15);
  assert.equal(mod.seekLineKeyboardValue('ArrowRight', true, 20, 0, 100), 30);
  assert.equal(mod.seekLineKeyboardValue('ArrowLeft', true, 5, 0, 100), 0);
  assert.equal(mod.seekLineKeyboardValue('PageUp', false, 20, 0, 100), 50);
  assert.equal(mod.seekLineKeyboardValue('PageDown', false, 20, 0, 100), 0);
  assert.equal(mod.seekLineKeyboardValue('Home', false, 20, 10, 100), 10);
  assert.equal(mod.seekLineKeyboardValue('End', false, 20, 10, 100), 100);
  const changed: number[] = [];
  const rendered = tree(h.render(() => mod.SeekLine({
    variant: 'player', value: 0.25,
    input: {min: 0, max: 100, step: 0.1, value: 25.4, onChange: (e: {currentTarget: {value: string}}) => changed.push(Number(e.currentTarget.value))},
  })));
  const input = all(rendered, 'input')[0]!;
  assert.equal(input.props.step, 1);
  assert.equal(input.props['aria-valuenow'], 25);
  let prevented = false, stopped = false;
  input.props.onKeyDown({
    key: 'ArrowRight', shiftKey: false, defaultPrevented: false,
    preventDefault() { prevented = true; }, stopPropagation() { stopped = true; },
    currentTarget: {value: '25.4', valueAsNumber: 25.4},
  });
  assert.equal(prevented, true);
  assert.equal(stopped, true);
  assert.deepEqual(changed, [30.4]);
  h.dispose();
});

test('Task 6: NowPlaying latches listeningNotice sequence and aborts radio AbortController on unmount', async () => {
  const h = createRenderer();
  const radioSignals: AbortSignal[] = [];
  const mod = await componentModule(new URL('../src/screens/watch/music/NowPlaying.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n},
    '../../../kit/artwork': {ArtworkGlow: 'ArtworkGlow'},
    '../../../kit/frames': {ArtPlaceholder: 'ArtPlaceholder', AudioPlayerFrame: 'AudioPlayerFrame'},
    '../../../kit/player': {AudioPanelSurface: 'AudioPanelSurface', PlayerVolume: 'PlayerVolume'},
    '../../../kit/overlays': {Menu: 'Menu', Dialog: 'Dialog', Banner: 'Banner'},
    '../../../router': {router: {navigate: async () => {}}},
    '../../../kit/media': {ListeningNotice: 'ListeningNotice', SeekLine: 'SeekLine', SignalLine: 'SignalLine', SignalPath: 'SignalPath'},
    '../model': await import('../src/screens/watch/model.ts'),
    '../listening/ListeningPanels': {ListeningPanels: 'ListeningPanels', ListeningTabs: 'ListeningTabs'},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/states': {StateView: 'StateView'},
    './signal': {watchMusicSignal: () => undefined}, './audio-facts': {readWatchMusicAudioFacts: () => ({})},
    '../../../shell/playback-model': {fenceKey: () => 'music-fence'},
    './EqualizerPanel': {WatchEqualizerPanel: 'WatchEqualizerPanel', hostAdmit: () => true, surfaceAdmit: () => true},
    './queue-facts': {musicQueueNext: () => undefined, musicQueueOrigin: () => undefined, musicQueueSong: () => ({itemId: 'song-1', libraryId: 'lib-1'})},
    './use-music-favorite': {useMusicFavorite: () => undefined},
    './now-playing.module.css': css,
  });
  const errors: unknown[] = [];
  const makeSnap = (sequence: number, listeningNotice?: string): any => ({
    authority: {...scope, epoch: 1, viewerFence: 'vf'}, sequence, disposed: false, fence,
    playback: {playbackRun: 1, intentId: 'i1', phase: 'ready', intent: 'paused', listeningNotice},
    dock: {local: {kind: 'music', fence, expanded: true}},
    presentation: {fence, subject: {kind: 'music', item: {id: 'song-1', kind: 'song', title: 'Track'}}, kind: 'music', durationSeconds: 200, positionSeconds: 10, playing: false, expanded: true, finished: false, controls: ['play', 'seek'], volume: 1, muted: false, rate: 1, policy: null},
    queueView: null,
  });
  let currentSnap = makeSnap(10, 'audio_interrupted');
  const runtime: any = {
    getSnapshot: () => currentSnap,
    playFromQueue: async (_req: unknown, opts: {signal: AbortSignal}) => {
      radioSignals.push(opts.signal);
      await new Promise((_res, rej) => { opts.signal.addEventListener('abort', () => rej(new Error('aborted'))); });
    },
  };
  const binding: any = {runtime, artwork: {foreground: () => undefined, ambient: () => undefined}, environment: defaultI18n, skips: {back: 10, forward: 30}, reportError: (e: unknown) => errors.push(e)};
  const firstTree = tree(h.render(() => mod.WatchMusicNowPlaying({binding, snapshot: currentSnap, expanded: true, onCollapse: () => {}, phone: false})));
  const firstNotice = all(firstTree, 'ListeningNotice')[0]!;
  assert.deepEqual(firstNotice.props.notice, {code: 'audio_interrupted', sequence: 10});
  currentSnap = makeSnap(25, 'audio_interrupted');
  const secondTree = tree(h.render(() => mod.WatchMusicNowPlaying({binding, snapshot: currentSnap, expanded: true, onCollapse: () => {}, phone: false})));
  const secondNotice = all(secondTree, 'ListeningNotice')[0]!;
  assert.deepEqual(secondNotice.props.notice, {code: 'audio_interrupted', sequence: 10});
  const songMenu = all(secondTree, 'Menu').find((n: any) => Array.isArray(n.props.items) && n.props.items.some((i: {id: string}) => i.id === 'radio'))!;
  songMenu.props.items.find((i: {id: string}) => i.id === 'radio').onSelect();
  assert.equal(radioSignals.length, 1);
  assert.equal(radioSignals[0]!.aborted, false);
  h.dispose();
  assert.equal(radioSignals[0]!.aborted, true);
  await Promise.resolve();
  assert.equal(errors.length, 0);
});

test('Task 7: panel-model catches async rejections cleanly and PlayerOptions exposes mobile subtitle controls & error handling', async () => {
  const options = parseOptions({itemId: 'movie', kind: 'movie', versions: [{id: 'v1', label: 'V1', durationMs: 120000, parts: [], video: [], audio: [{id: '1', codec: 'aac', language: 'en'}], subtitles: []}], qualities: [{mode: 'original'}], chapters: [], markers: []});
  const session = parseSession({id: 'session', revision: '1', kind: 'vod', role: 'local', state: 'playing', itemId: 'movie', versionId: 'v1', queue: {queueId: 'queue', entryId: 'entry'}, presentation: {generation: 1, mode: 'direct', url: '/v1/media/current', startPositionMs: 0, subtitles: [], decision: {}, audioTrackId: '1', quality: {mode: 'original'}}});
  const resource: SubtitleResource = {id: 's1', sourceId: 'source', revision: 1, scope: 'personal', language: 'en', title: '', format: 'srt', origin: 'sidecar', rights: 'owned', offsetUs: '0', canManage: true, enabled: true, renderer: 'external_text', default: false, forced: false};
  const plan: SubtitlePlan = {version: 1, sessionId: 'session', generation: 1, sourceId: 'source', revision: 1, catalogRevision: 1, renderer: 'external_text', mode: 'track', offAvailable: true, offsetUs: '0', selected: resource, resources: [resource], discovered: []};
  const offers: PlayerOffersData = {scope: {serverId: 'server', libraryId: 'lib', itemId: 'movie', viewerFence: 'vf'}, revision: '1', preparedVersions: [], preparedOffersRevision: '', subtitlePlan: plan, subtitlePlanUnavailableReason: null, current: {sessionId: 'session', generation: 1, state: 'playing', sourceId: 'source', delivery: 'direct', audioSelection: 'platform_default', subtitleSelection: 'track', qualityId: 'original'}, sources: [{id: 'source', available: true, container: 'mkv', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080, duration: 120, factsRevision: 1, factsStatus: 'known', relation: 'unknown', boundary: 'whole_source', enabled: false, reason: '', streams: [{index: 1, type: 'audio', codec: 'aac', default: true, forced: false, enabled: true, reason: ''}], qualities: [{id: 'original', kind: 'original', label: 'Original', enabled: true}]}], controls: [], markers: null, deliveryPolicy: null, offersRevision: 'a'.repeat(64), transcodingEnabled: true};
  const live: WebPlaybackRuntimeSnapshot = {authority: {...scope, epoch: 1, viewerFence: 'vf'}, sequence: 1, disposed: false, fence, nativeVideoSurfaceActive: false, playback: {} as never, dock: {} as never, presentations: [], queueView: null, session, options, offers, presentation: {rate: 1} as never};
  const runtime = {
    getSnapshot: () => live, speedChoices: () => [1],
    sleepTimer: () => ({minutes: 0, endsAt: 0}),
    setSleepTimer: async () => { throw new Error('sleep_failed'); },
    subtitleStyle: () => ({size: 'medium' as const, background: 'translucent' as const}),
    setSubtitleStyle: async () => { throw new Error('style_failed'); },
    subtitleForced: () => ({only: false, lines: {forced: 1, total: 2}}),
    setSubtitleForcedOnly: async () => { throw new Error('forced_failed'); },
  } as unknown as WebShellPlaybackRuntime;
  const model = watchPanelModel(runtime, live, defaultI18n)!;
  model.sleepTimer!.set(15);
  await assert.rejects(Promise.resolve(model.sleepTimer!.set(15)), /sleep_failed/);
  await assert.rejects(Promise.resolve(model.subtitleStyle!.set({size: 'large'})), /style_failed/);
  await assert.rejects(Promise.resolve(model.subtitleForced!.set(true)), /forced_failed/);

  const h = createRenderer({element: {querySelector: () => ({focus() {}, querySelector: () => ({focus() {}})})}});
  const optionsMod = await componentModule(new URL('../src/kit/player/PlayerOptions.tsx', import.meta.url), {
    react: h.react,
    '../controls': {Segmented: 'Segmented', Switch: 'Switch'},
    '../media': {TrackChoices: 'TrackChoices'},
    '../Icon': {Icon: 'Icon'},
    '../overlays': {BottomSheet: ({children}: {children: unknown}) => children, Dialog: ({children}: {children: unknown}) => children, Menu: 'Menu'},
    './panel-types': await import('../src/kit/player/panel-types.ts'),
    './options.module.css': css,
  });
  const reported: unknown[] = [];
  const phoneModel = {
    ...model,
    subtitleTiming: {offsetSeconds: 0.2, shift: async () => {}},
    subtitleAdditions: [{id: 'add-1', primary: 'French', fullName: 'French', selected: false, addable: true, unavailable: false}],
    subtitleSearch: {busy: false, searched: false, canRetry: true, credit: 'OpenSubtitles', results: [], search: async () => {}, choose: async () => {}},
  };
  const phoneTree = tree(h.render(() => optionsMod.PlayerOptions({
    model: phoneModel,
    context: {panel: 'subtitles', snapshot: live, layout: 'phone', phoneTop: 0, close() {}, show() {}, returnFocusRef: {current: null}},
    i18n: defaultI18n, reportError: (e: unknown) => reported.push(e), openSettings() {},
  })));
  assert.ok(phoneTree.some((n: any) => n.props?.['aria-label'] === defaultI18n.t('player.opt.earlier')), 'mobile timing controls shown');
  assert.ok(all(phoneTree, 'TrackChoices').some((n: any) => n.props.title === defaultI18n.t('player.opt.notAdded')), 'mobile additions shown');
  const forcedSwitch = all(phoneTree, 'Switch')[0]!;
  assert.ok(forcedSwitch, 'mobile forced switch shown');
  const sizeSegment = all(phoneTree, 'Segmented').find((n: any) => n.props['aria-label'] === defaultI18n.t('settings.row.subtitleSize'))!;
  assert.ok(sizeSegment, 'mobile subtitle style size shown');
  sizeSegment.props.onValueChange('large');
  forcedSwitch.props.onCheckedChange(true);
  await Promise.resolve();
  assert.equal(reported.length, 2);
  h.dispose();
});

test('Task 8: TogetherScreen renders full room controls on phone and ticks host-disconnect countdown via setTimeout', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const h = createRenderer();
  const mod = await componentModule(new URL('../src/screens/together/Together.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n},
    '../../platform/playback/subject': {itemSubject: (item: unknown) => ({kind: 'video', item})},
    '../../kit/artwork': {Artwork: 'Artwork', ProfileFace: 'ProfileFace'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/controls': {Button: 'Button', Segmented: 'Segmented', TextField: 'TextField'},
    '../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: ({children}: {children: unknown}) => children, StateRow: 'StateRow'},
    '../../kit/shell/SettingsSurfaces': {SettingsRow: 'SettingsRow'},
    '../../kit/overlays/shared': {usePhone: () => true},
    '../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog', Menu: 'Menu'},
    '../watch/model': await import('../src/screens/watch/model.ts'),
    './together-model': await import('../src/screens/together/together-model.ts'),
    './together.module.css': css,
  });
  let nowMs = Date.parse('2026-11-16T11:59:30Z');
  const group: Room = {
    id: 'g1', name: 'Movie night', control: 'host',
    you: {host: true, memberId: 'host', mayControl: true, mayBecomeHost: false},
    clock: {entryId: 'e1', positionMs: 0, at: '2026-11-16T11:59:30Z', playing: true, ready: 2, waitingFor: 0},
    host: {present: false, pausesAt: '2026-11-16T12:00:00Z', endsAt: '2026-11-16T12:05:00Z'},
    members: [
      {id: 'host', name: 'Host', host: true, you: true, status: 'in_step'},
      {id: 'guest', name: 'Guest', host: false, you: false, status: 'in_step'},
    ],
    queue: [{entryId: 'e1', itemId: 'item-1', available: true}],
    inStepMs: 750, jumpMs: 3000, reportEveryMs: 10000,
  };
  const snap = {room: {phase: 'live' as const, busy: false, group, directory: []}, items: new Map([['item-1', {id: 'item-1', kind: 'movie', title: 'Feature Film'}]])};
  const binding: any = {
    isCurrent: () => true, subscribe: () => () => {}, getSnapshot: () => snap,
    runtime: {subscribe: () => () => {}, getSnapshot: () => ({fence, presentation: {positionSeconds: 10, durationSeconds: 120}}), groupPlaybackMatches: () => true, open() {}},
    service: {refreshDirectory: async () => {}, serverNow: () => nowMs, leave: async () => {}, end: async () => {}, makeHost: async () => {}, transport: async () => {}, queueRemove: async () => {}, invite: async () => {}},
    artwork: {foreground: () => undefined}, environment: defaultI18n, t: defaultI18n.t, number: (n: number) => String(n),
    profileName: 'Host', openSettings() {}, reportError(e: unknown) { throw e; },
  };
  const render = () => tree(h.render(() => mod.TogetherScreen({binding})));
  const initial = render();
  const buttonLabels = all(initial, 'Button').map(textOf);
  assert.ok(buttonLabels.includes(defaultI18n.t('web.together.openPlayer')), 'phone has Open Player');
  assert.ok(buttonLabels.includes(defaultI18n.t('web.together.makeHost')), 'phone has Make Host');
  assert.ok(buttonLabels.includes(defaultI18n.t('web.together.leave')), 'phone has Leave');
  assert.ok(buttonLabels.includes(defaultI18n.t('web.together.endAll')), 'phone has End room');
  assert.ok(all(initial, 'SectionCard').some((n: any) => String(n.props.label ?? '').startsWith(defaultI18n.t('web.together.queue'))), 'phone has Queue section');
  assert.match(textOf(all(initial, 'Banner')[0]), /0:30/);
  nowMs += 5000;
  context.mock.timers.tick(5000);
  const ticked = render();
  assert.match(textOf(all(ticked, 'Banner')[0]), /0:25/);
  h.dispose();
});
