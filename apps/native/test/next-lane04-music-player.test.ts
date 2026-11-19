/** Real screen composition and callbacks against explicit ports. This shallow
 * harness does not prove native blur, gestures, focus reconciliation or geometry. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule, listeningCoverFixture} from './helpers/screen-harness.mjs';
import * as chapters from '../src-next/screens/destinations/music/audiobook-controls.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {GestureCommitScope} from '../src-next/kit/controls/gesture-commit.ts';
import {defaultI18n as realI18n} from '../../../packages/i18n/src/index.ts';

// The page is AudioPlayerFrame (a string in this harness): its regions are its props, and its controls are data.
const regions = (value: any) => value.type === 'AudioPlayerFrame' ? ['cover', 'signal', 'action', 'scrubber', 'notice', 'panel'].map(key => value.props[key]) : value.props.children;
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(regions(value))] : [];
const text = (value: any): string => Array.isArray(value) ? value.map(text).join('') : value?.props ? text(value.props.children) : value == null || typeof value === 'boolean' ? '' : String(value);
async function fixture(book = false, width = 390) {
  const ui = hooks();
  const react: any = {...ui.react, useId: () => 'player', useLayoutEffect: () => {}, createContext: (value: unknown) => ({value, Provider: 'Provider'}),
    isValidElement: (value: any) => !!value?.props, cloneElement: (value: any, patch: any) => ({...value, props: {...value.props, ...patch}})};
  react.default = react;
  const theme = {isTV: false, platform: width < 700 ? 'phone' : 'tablet', layers: {player: 10}, colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#888', accent: '#409fd4', accentStrong: '#5cb3e2'}};
  const gesture: any = {}; for (const name of ['enabled', 'minDistance', 'onBegin', 'onUpdate', 'onEnd', 'onFinalize']) gesture[name] = () => gesture;
  const commands: any[] = [], rates: number[] = [], sleeps: (number | null)[] = [];
  let active = true;
  const snapshot: any = {phase: 'current', rate: {selected: 1.25, effective: 1.25, supported: [1, 1.25, 1.5]},
    preferences: {phase: 'ready', values: null, audiobook: {skipBackMs: 15_000, skipForwardMs: 30_000, resumeRewindMs: 0}},
    chapters: {phase: 'ready', pagination: {cursor: null, canPrevious: false}, data: {
      scope: {serverId: 'server', itemId: 'book', libraryId: 'books', viewerFence: 'viewer', sessionId: 'session', sourceId: 'source', generation: 1},
      revision: '1', status: 'available', reason: null, totalCount: 3, duration: 180, nextCursor: '',
      chapters: [1, 2, 3].map(index => ({id: String(index), index, title: `Chapter ${index}`, startSeconds: (index - 1) * 60, endSeconds: index * 60}))}}};
  const reader: any = {current: () => active, getSnapshot: () => snapshot, readChapters: async () => {}, readNextChapterPage: async () => {}, readPreviousChapterPage: async () => {}};
  const {NowPlayingScreen} = await screenModule(new URL('../src-next/screens/destinations/music/NowPlayingScreen.tsx', import.meta.url), {
    react, 'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {position: 'absolute'}}, Text: 'Text', View: 'View'},
    'react-native-gesture-handler': {Gesture: {Pan: () => gesture}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, runOnJS: (fn: any) => fn, useAnimatedStyle: (fn: any) => fn(), useSharedValue: (value: any) => ui.react.useRef({value}).current},
    '../../../platform/playback/equalizer': {nativeEqualizer: {subscribe: () => () => {}, getSnapshot: () => ({supported: false})}},
    '../../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../watch/Equalizer': {NativeWatchEqualizer: 'Equalizer'},
    './MusicSignal': {MusicSignalLine: 'Signal', MusicSignalSheet: 'SignalSheet'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: any) => key === 'player.opt.minutes' ? `${values.count} min`
      : key === 'player.clockOf' || key === 'player.chapterTotal' ? realI18n.t(key, values) : key}},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText, clockText: (seconds: number) => `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}`},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/controls/gesture-commit': {GestureCommitScope}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/theme': {useTheme: () => theme},
    '../../../kit/dock/ListeningCoverBridge': listeningCoverFixture, '../../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}:${opacity}`, browseType: (_theme: any, size: number, weight: number) => ({fontSize: size, fontWeight: weight})},
    '../../../kit/dock/model': {commandAllowed: () => true, dockCommand: (_occupant: unknown, type: string) => ({type}),
      playbackPosition: (occupant: any) => occupant.playback.positionSeconds, seekBounds: () => ({start: 0, end: 180}),
      seekTo: (_occupant: unknown, seconds: number) => ({type: 'seek', seconds: Math.max(0, Math.min(180, seconds))})},
    './NowPlayingQueue': {NowPlayingQueue: 'QueuePublisher', nowPlayingQueueWindow: (view: any) => view.window,
      nowPlayingQueueEntry: () => ({available: true, segmentId: 'segment'})},
    './ListeningSongActions': {ListeningSongActions: 'SongActions', ListeningFavoritePaint: 'FavoritePaint', listeningSongFavorite: () => undefined},
    './TVMusicFocus': {MusicFocusRegion: 'MusicFocusRegion', TVMusicFocus: 'TVMusicFocus'}, './TVNowPlayingScreen': {TVNowPlayingScreen: 'TVScreen'},
    '../../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: true})}, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'},
    './use-audiobook-read': {useAudiobookRead: () => ({reader: book ? reader : undefined, snapshot: book ? snapshot : {phase: 'retired'}})},
    './audiobook-controls': chapters, '../../../kit/overlays/SurfacePaintHost': {RetainedOverlay: 'RetainedOverlay'},
    '../../../kit/dock/PlayerMorphBridge': {usePublishedPlayerMorph: () => undefined}, '../../../kit/overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({visible: true, interactive: true})},
    './NowPlayingLyrics': {NowPlayingLyrics: 'LyricsPublisher'}, './ListeningPanePaint': {ListeningQueuePaint: 'QueuePaint', ListeningLyricsPaint: 'LyricsPaint'},
    '../../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})},
    '../../../kit/frames': {AudioPlayerFrame: 'AudioPlayerFrame', audioPlayerFramePaintTypes: []}, '../../../kit/frames/metrics': {useFrameMetrics: () => ({form: width < 600 ? 'phone' : 'expanded'})},
  });
  const fence = {kind: 'queue', sessionId: 'session', generation: 1, sourceId: 'source', itemId: 'item'};
  const props: any = {occupant: {mode: book ? 'audiobook' : 'music', playback: {fence, positionSeconds: 70, playing: true},
    presentation: {identity: {title: 'Title', subtitle: [{id: 'artist', text: 'Author'}]}, durationSeconds: 180, artwork: {}, skipBackSeconds: 10,
      capabilities: {seek: true, play: true, pause: true, previous: true, next: true, stop: true}}},
    queue: {queue: {id: 'queue', currentEntryId: 'current', revision: '1'}, window: {segments: [{id: 'segment', label: 'An album'}]}},
    width, height: 844, onAction: (command: any) => commands.push(command), onClose() {}, reportError: (error: unknown) => {throw error;},
    audiobook: {read: () => reader, setRate: (_fence: unknown, rate: number) => rates.push(rate), setSleepTimer: async (_fence: unknown, minutes: number | null) => {sleeps.push(minutes);}}};
  const root = NowPlayingScreen(props);
  let tree: any;
  const render = () => tree = ui.render(() => root.type(props)); render();
  return {render, props, commands, rates, sleeps, retire: () => {active = false;}, nodes: () => nodes(tree), tree: () => tree,
    button: (id: string) => nodes(tree).find(node => node.props.testID === `now-playing-${id}`),
    frame: () => nodes(tree).find(node => node.type === 'AudioPlayerFrame'),
    // A control the frame draws, by its test id: the frame's own controls are data in its props.
    control: (id: string) => {const page = nodes(tree).find(node => node.type === 'AudioPlayerFrame')?.props; if (!page) return undefined;
      return [page.close, page.device, page.more, page.favourite, page.lyrics, page.queue, page.speed, page.sleep, page.chapters, ...page.transport].find((item: any) => item?.testID === `now-playing-${id}`);}};
}

test('phone music fills the frame: Minimise, the music transport with its ends, and the time left', async () => {
  const f = await fixture(), page = f.frame().props;
  assert.equal(page.close.icon, 'i-down'); assert.equal(page.icon, 'i-music');
  assert.deepEqual(page.transport.map((item: any) => item.testID), ['shuffle', 'previous', 'toggle', 'next', 'repeat'].map(id => `now-playing-${id}`));
  assert.equal(page.times.end, '-1:50'); assert.equal(page.panel, undefined, 'a phone has no panel: the queue is the sheet');
  assert.equal(page.context, 'next.music.playingFrom'); assert.equal(page.source, 'An album');
  page.transport[2].onPress(); assert.deepEqual(f.commands.map(command => command.type), ['pause']);
});

test('phone queue lives in a dismissible BottomSheet while the player stays mounted', async () => {
  const f = await fixture();
  f.control('queue-toggle').onPress(); f.render();
  const sheet = f.nodes().find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.open, true); assert.ok(f.frame());
  // Detents are heights in points: fractions (.5, .94) made the sheet rise 1 point, a sliver under a dimmed screen.
  assert.deepEqual(sheet.props.detents, [422, 793]);
  assert.ok(nodes(sheet).some(node => node.type === 'QueuePaint'));
  sheet.props.onOpenChange(false); f.render();
  assert.equal(f.nodes().find(node => node.type === 'BottomSheet').props.open, false);
});

test("tablet queue sits in the frame's panel place in the glass panel, and toggles closed", async () => {
  const f = await fixture(false, 1180);
  const side = nodes(f.frame().props.panel).find(node => node.type?.name === 'ListeningSidePanel'); assert.ok(side);
  const rendered = side.type(side.props);
  assert.equal(rendered.props.style.flex, 1); assert.equal(rendered.props.style.borderRadius, 20);
  assert.equal(nodes(rendered).find(node => node.type === 'BlurView').props.blurAmount, 30);
  f.control('queue-toggle').onPress(); f.render();
  assert.equal(f.frame().props.panel, undefined, 'the column takes the room back');
});

test('audiobook controls use configured skips and canonical chapter/rate/sleep ports', async () => {
  const f = await fixture(true);
  assert.equal(f.control('shuffle'), undefined); assert.equal(f.control('repeat'), undefined);
  assert.deepEqual([f.control('skip-back').skip, f.control('skip-forward').skip], [15, 30]);
  f.control('skip-back').onPress(); f.control('skip-forward').onPress();
  assert.deepEqual(f.commands.map(command => command.seconds), [55, 100]);
  // MU-7: the chapter arrows are the runtime's Previous/Next, which move by book chapter across parts.
  f.control('next-chapter').onPress(); f.control('previous-chapter').onPress();
  assert.deepEqual(f.commands.slice(-2).map(command => command.type), ['next', 'previous']);
  f.control('speed').onPress(); f.render();
  const speed = f.button('speed-menu'); assert.equal(speed.props.open, true);
  speed.props.items.find((item: any) => item.id === '1.5').onSelect(); assert.deepEqual(f.rates, [1.5]);
  speed.props.onOpenChange(false); f.render();
  f.control('sleep').onPress(); f.render();
  f.button('sleep-menu').props.items.find((item: any) => item.id === '30').onSelect(); await Promise.resolve();
  assert.deepEqual(f.sleeps, [30]);
  f.retire(); speed.props.items[0].onSelect(); assert.deepEqual(f.rates, [1.5]);
});

test('the chapter arrows say Previous/Next chapter only when the book\'s list covers the part; else they move parts, as for a track', async () => {
  const f = await fixture(true);
  const labels = () => [f.control('previous-chapter').label, f.control('next-chapter').label];
  assert.deepEqual(labels(), ['player.previous', 'player.next'], 'no book list: Previous and Next move the queue by part');
  for (const phase of ['loading', 'error'] as const) {
    f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase, chapters: [], current: -1})}; f.render();
    assert.deepEqual(labels(), ['player.previous', 'player.next'], phase);
  }
  f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase: 'ready', current: 0,
    chapters: [{id: 'item:1', itemId: 'item', index: 1, number: 1, title: 'One', startSeconds: 0, endSeconds: 60}]})}; f.render();
  assert.deepEqual(labels(), ['next.dock.previousChapter', 'next.dock.nextChapter']);
});

test("landscape audiobook is the frame's book form with a Chapters panel", async () => {
  const f = await fixture(true, 1180), page = f.frame().props;
  assert.equal(page.icon, 'i-book'); assert.ok(page.chapter, 'the chapter line makes it the book form');
  assert.ok(page.speed.choices.length); assert.ok(page.sleep.choices.length, 'the wide form draws Speed and Sleep timer as rows');
  assert.ok(f.button('tab-chapters'));
  assert.equal(f.nodes().find(node => node.type === 'BottomSheet').props.open, false);
});


test('MU-2: with the whole book known, the Chapters pane lists every part\'s chapters numbered through the book, and a row plays that chapter', async () => {
  const f = await fixture(true, 1180), played: string[] = [];
  const row = (id: string, itemId: string, number: number, title: string, start: number, end: number) => ({id, itemId, index: 1, number, title, startSeconds: start, endSeconds: end});
  const view = {bookId: 'book', itemId: 'item', phase: 'ready', current: 2,
    chapters: [row('part-a:1', 'part-a', 1, 'One', 0, 600), row('part-a:2', 'part-a', 2, 'Two', 600, 1200), row('item:1', 'item', 3, 'Three', 60, 120)]};
  f.props.audiobook = {...f.props.audiobook, bookChapters: () => view, playBookChapter: async (_fence: unknown, id: string) => {played.push(id);}};
  f.render();
  const pane = f.nodes().find(node => node.props.testID === 'now-playing-chapters');
  const rows = nodes(pane).filter(node => node.props.accessibilityRole === 'button' && node.props.accessibilityState);
  assert.deepEqual(rows.map(node => node.props.accessibilityState.selected), [false, false, true]);
  assert.ok(nodes(rows[2]).some(node => node.type === 'Text' && text(node) === '3'), 'numbers count through the book');
  rows[0].props.onPress(); await Promise.resolve();
  assert.deepEqual(played, ['part-a:1'], 'a chapter in another part is played through the runtime');
  assert.equal(f.frame().props.chapter.label, 'Three');
  // tablet-play: the chapter's own clock in words.
  assert.match(f.frame().props.chapter.of, /\d+:\d\d of 1:00$/);
});

test('phone-app 15: on a phone the chapter line ends with the book\'s chapter count, "of N", never a bare or unknown total', async () => {
  const f = await fixture(true), row = (id: string, itemId: string, number: number, title: string, start: number, end: number) => ({id, itemId, index: 1, number, title, startSeconds: start, endSeconds: end});
  f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase: 'ready', current: 2,
    chapters: [row('part-a:1', 'part-a', 1, 'One', 0, 600), row('part-a:2', 'part-a', 2, 'Two', 600, 1200), row('item:1', 'item', 3, 'Three', 0, 7200)]}), playBookChapter: async () => {}};
  f.render();
  const chapter = f.frame().props.chapter, line = `${chapter.label} ${chapter.of}`;
  assert.ok(line.includes('Three')); assert.ok(line.endsWith('of 3'), line);
  assert.doesNotMatch(line, /\d+\/\d+|undefined/);
});

test("phone audiobook options are the frame's tiles: Speed, Sleep timer and Chapters with their values, and the book's time left", async () => {
  const f = await fixture(true), page = f.frame().props;
  assert.equal(page.speed.value, '1.25×'); assert.equal(page.chapters.value, '3'); assert.equal(page.chapters.active, false);
  assert.equal(page.times.middle, 'next.kit.remaining');
  f.control('chapters-toggle').onPress(); f.render();
  assert.equal(f.control('chapters-toggle').active, true);
});

test('tablet audiobook ticks mark actual chapter boundaries within the full-book seek range', async () => {
  const f = await fixture(true, 1180), ticks = f.button('chapter-ticks');
  const marks = nodes(ticks).filter(node => node !== ticks);
  assert.equal(marks.length, 2);
  assert.deepEqual(marks.map(node => node.props.style.left), [`${60 / 180 * 100}%`, `${120 / 180 * 100}%`]);
  assert.equal(ticks.props.pointerEvents, 'none');
  assert.equal(f.button('scrubber').props.span, 180);
});
