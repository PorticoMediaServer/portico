/** Real screen composition and callbacks against explicit ports. This shallow
 * harness does not prove native blur, gestures, focus reconciliation or geometry. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule, listeningCoverFixture} from './helpers/screen-harness.mjs';
import * as chapters from '../src-next/screens/destinations/music/audiobook-controls.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {GestureCommitScope} from '../src-next/kit/controls/gesture-commit.ts';
import {defaultI18n as realI18n} from '../../../packages/i18n/src/index.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const text = (value: any): string => Array.isArray(value) ? value.map(text).join('') : value?.props ? text(value.props.children) : value == null || typeof value === 'boolean' ? '' : String(value);
async function fixture(book = false, width = 390) {
  const ui = hooks();
  const react: any = {...ui.react, useId: () => 'player', useLayoutEffect: () => {}, createContext: (value: unknown) => ({value, Provider: 'Provider'}),
    isValidElement: (value: any) => !!value?.props, cloneElement: (value: any, patch: any) => ({...value, props: {...value.props, ...patch}})};
  react.default = react;
  const theme = {isTV: false, form: width < 700 ? 'phone' : 'expanded', platform: width < 700 ? 'phone' : 'tablet', layers: {player: 10}, colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#888', accent: '#409fd4', accentStrong: '#5cb3e2'}};
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
  const playerBindings = {
    react, 'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {position: 'absolute'}}, Text: 'Text', View: 'View'},
    'react-native-gesture-handler': {Gesture: {Pan: () => gesture}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, runOnJS: (fn: any) => fn, useAnimatedStyle: (fn: any) => fn(), useSharedValue: (value: any) => ui.react.useRef({value}).current},
    '../../../platform/playback/equalizer': {nativeEqualizer: {subscribe: () => () => {}, getSnapshot: () => ({supported: false})}},
    '../../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../../screens/watch/Equalizer': {NativeWatchEqualizer: 'Equalizer'},
    '../../../screens/destinations/music/MusicSignal': {MusicSignalLine: 'Signal', MusicSignalSheet: 'SignalSheet'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: any) => key === 'player.opt.minutes' ? `${values.count} min`
      : key === 'player.clockOf' || key === 'player.chapterTotal' ? realI18n.t(key, values) : key}},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText, clockText: (seconds: number) => `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}`},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/controls/gesture-commit': {GestureCommitScope}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/theme': {useTheme: () => theme},
    '../../../kit/dock/ListeningCoverBridge': listeningCoverFixture, '../../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}:${opacity}`, browseType: (_theme: any, size: number, weight: number) => ({fontSize: size, fontWeight: weight})},
    '../../../kit/dock/model': {commandAllowed: () => true, dockCommand: (_occupant: unknown, type: string) => ({type}),
      playbackPosition: (occupant: any) => occupant.playback.positionSeconds, seekBounds: () => ({start: 0, end: 180}),
      seekTo: (_occupant: unknown, seconds: number) => ({type: 'seek', seconds: Math.max(0, Math.min(180, seconds))})},
    '../../../kit/frames': {AudioPlayerFrame: 'AudioPlayerPage'}, '../../../screens/destinations/music/NowPlayingQueue': {nowPlayingSource: () => undefined, NowPlayingQueue: 'QueuePublisher', nowPlayingQueueWindow: (view: any) => view.window,
      nowPlayingQueueEntry: () => ({available: true, segmentId: 'segment'})},
    '../../../screens/destinations/music/ListeningSongActions': {ListeningSongActions: 'SongActions', ListeningFavoritePaint: 'FavoritePaint', listeningSongFavorite: () => undefined},
    '../../../screens/destinations/music/TVMusicFocus': {MusicFocusRegion: 'MusicFocusRegion', TVMusicFocus: 'TVMusicFocus'}, './TVNowPlayingScreen': {TVNowPlayingScreen: 'TVScreen'},
    '../../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: true})}, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'},
    '../../../screens/destinations/music/use-audiobook-read': {useAudiobookRead: () => ({reader: book ? reader : undefined, snapshot: book ? snapshot : {phase: 'retired'}})},
    '../../../screens/destinations/music/audiobook-controls': chapters, '../../../kit/overlays/SurfacePaintHost': {RetainedOverlay: 'RetainedOverlay'},
    '../../../kit/dock/PlayerMorphBridge': {usePublishedPlayerMorph: () => undefined}, '../../../kit/overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({visible: true, interactive: true})},
    '../../shared/player/NowPlayingLyrics': {NowPlayingLyrics: 'LyricsPublisher'}, '../../../kit/player/ListeningPanePaint': {ListeningQueuePaint: 'QueuePaint', ListeningLyricsPaint: 'LyricsPaint'},
    '../../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})},
  };
  const playerHook = await screenModule(new URL('../src-next/pages/shared/player/useTouchAudioPlayer.ts', import.meta.url), playerBindings);
  const {AudioPlayerPage} = await screenModule(new URL('../src-next/pages/touch/player/AudioPlayerPage.tsx', import.meta.url), {
    ...playerBindings, '../../shared/player/useTouchAudioPlayer': playerHook,
    '../../../kit/player/queue': {AudioPanel: 'AudioPanel', ChapterList: 'ChapterList', ChapterTicks: 'ChapterTicks'},
    '../../../kit/player/ListeningSurface': {ListeningCover: 'ListeningCover', ListeningNoticeLine: 'NoticeLine', ListeningPageBackground: 'ListeningPageBackground', ListeningPanel: 'ListeningPanel', ListeningSurface: 'ListeningSurface'},
    '../../../kit/player/listening-presentation': {equalizerCopy: () => ({}), retainedListeningParts: [], omittedListeningParts: []},
  });
  const fence = {kind: 'queue', sessionId: 'session', generation: 1, sourceId: 'source', itemId: 'item'};
  const props: any = {occupant: {mode: book ? 'audiobook' : 'music', playback: {fence, positionSeconds: 70, playing: true},
    presentation: {identity: {title: 'Title', subtitle: [{id: 'artist', text: 'Author'}]}, durationSeconds: 180, artwork: {}, skipBackSeconds: 10,
      capabilities: {seek: true, play: true, pause: true, previous: true, next: true, stop: true}}},
    queue: {queue: {id: 'queue', currentEntryId: 'current', revision: '1'}, window: {segments: [{id: 'segment', label: 'An album'}]}},
    width, height: 844, onAction: (command: any) => commands.push(command), onClose() {}, reportError: (error: unknown) => {throw error;},
    audiobook: {read: () => reader, setRate: (_fence: unknown, rate: number) => rates.push(rate), setSleepTimer: async (_fence: unknown, minutes: number | null) => {sleeps.push(minutes);}}};
  let tree: any;
  const render = () => tree = ui.render(() => AudioPlayerPage(props)); render();
  const page = () => nodes(tree).find(node => node.type === 'AudioPlayerPage');
  return {render, props, commands, rates, sleeps, retire: () => {active = false;}, nodes: () => nodes(tree), tree: () => tree,
    page,
    button: (id: string) => {
      const testID = `now-playing-${id}`;
      const button = nodes(tree).find(node => node.props.testID === testID);
      const data = page()?.props;
      const item = data && [...data.transport, data.queue, data.lyrics, data.speed, data.sleep, data.chapters].find(value => value?.testID === testID);
      return button ?? (item && {props: {...item, accessibilityLabel: item.label}});
    }};
}

test('phone music supplies its cover, transport and remaining time to the audio frame', async () => {
  const f = await fixture();
  const page = f.page().props;
  assert.equal(page.cover.props.source, f.props.occupant.presentation.artwork);
  assert.equal(page.close.label, 'player.minimize');
  assert.equal(page.transport[2].testID, 'now-playing-toggle');
  assert.equal(page.times.end, '-1:50');
});

test('phone queue lives in a dismissible BottomSheet while the player stays mounted', async () => {
  const f = await fixture();
  f.button('queue-toggle').props.onPress(); f.render();
  const sheet = f.nodes().find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.open, true); assert.ok(f.page());
  // Detents are heights in points: fractions (.5, .94) made the sheet rise 1 point, a sliver under a dimmed screen.
  assert.deepEqual(sheet.props.detents, [422, 793]);
  assert.ok(nodes(sheet).some(node => node.type === 'QueuePaint'));
  sheet.props.onOpenChange(false); f.render();
  assert.equal(f.nodes().find(node => node.type === 'BottomSheet').props.open, false);
});

test('tablet queue toggles closed and restores the centred column with the specified glass panel', async () => {
  const f = await fixture(false, 1180);
  assert.equal(f.page().props.panel.type, 'AudioPanel');
  assert.equal(f.page().props.transport[2].testID, 'now-playing-toggle');
  f.button('queue-toggle').props.onPress(); f.render();
  assert.equal(f.page().props.panel, undefined);
});

test('audiobook controls use configured skips and canonical chapter/rate/sleep ports', async () => {
  const f = await fixture(true);
  assert.equal(f.button('shuffle'), undefined); assert.equal(f.button('repeat'), undefined);
  f.button('skip-back').props.onPress(); f.button('skip-forward').props.onPress();
  assert.deepEqual(f.commands.map(command => command.seconds), [55, 100]);
  // MU-7: the chapter arrows are the runtime's Previous/Next, which move by book chapter across parts.
  f.button('next-chapter').props.onPress(); f.button('previous-chapter').props.onPress();
  assert.deepEqual(f.commands.slice(-2).map(command => command.type), ['next', 'previous']);
  f.button('speed').props.onPress(); f.render();
  const speed = f.button('speed-menu'); assert.equal(speed.props.open, true);
  speed.props.items.find((item: any) => item.id === '1.5').onSelect(); assert.deepEqual(f.rates, [1.5]);
  speed.props.onOpenChange(false); f.render();
  f.button('sleep').props.onPress(); f.render();
  f.button('sleep-menu').props.items.find((item: any) => item.id === '30').onSelect(); await Promise.resolve();
  assert.deepEqual(f.sleeps, [30]);
  f.retire(); speed.props.items[0].onSelect(); assert.deepEqual(f.rates, [1.5]);
});

test('the chapter arrows say Previous/Next chapter only when the book\'s list covers the part; else they move parts, as for a track', async () => {
  const f = await fixture(true);
  const labels = () => [f.button('previous-chapter').props.accessibilityLabel, f.button('next-chapter').props.accessibilityLabel];
  assert.deepEqual(labels(), ['player.previous', 'player.next'], 'no book list: Previous and Next move the queue by part');
  for (const phase of ['loading', 'error'] as const) {
    f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase, chapters: [], current: -1})}; f.render();
    assert.deepEqual(labels(), ['player.previous', 'player.next'], phase);
  }
  f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase: 'ready', current: 0,
    chapters: [{id: 'item:1', itemId: 'item', index: 1, number: 1, title: 'One', startSeconds: 0, endSeconds: 60}]})}; f.render();
  assert.deepEqual(labels(), ['next.dock.previousChapter', 'next.dock.nextChapter']);
});

test('landscape audiobook uses its horizontal cover/identity composition and a Chapters panel', async () => {
  const f = await fixture(true, 1180);
  assert.equal(f.page().props.icon, 'i-book');
  assert.equal(f.page().props.panel.type, 'AudioPanel');
  assert.deepEqual(f.page().props.panel.props.tabs.items.map((item: any) => item.value), ['chapters', 'queue']);
  assert.ok(f.button('chapters'));
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
  const rows = pane.props.chapters;
  assert.deepEqual(rows.map((row: any) => row.selected), [false, false, true]);
  assert.equal(rows[2].number, 3, 'numbers count through the book');
  rows[0].onPress(); await Promise.resolve();
  assert.deepEqual(played, ['part-a:1'], 'a chapter in another part is played through the runtime');
  assert.equal(f.page().props.chapter.label, 'Three');
  assert.match(f.page().props.chapter.of, /\d+:\d\d of 1:00$/);
});

test('phone-app 15: on a phone the chapter line ends with the book\'s chapter count, "of N", never a bare or unknown total', async () => {
  const f = await fixture(true), row = (id: string, itemId: string, number: number, title: string, start: number, end: number) => ({id, itemId, index: 1, number, title, startSeconds: start, endSeconds: end});
  f.props.audiobook = {...f.props.audiobook, bookChapters: () => ({bookId: 'book', itemId: 'item', phase: 'ready', current: 2,
    chapters: [row('part-a:1', 'part-a', 1, 'One', 0, 600), row('part-a:2', 'part-a', 2, 'Two', 600, 1200), row('item:1', 'item', 3, 'Three', 0, 7200)]}), playBookChapter: async () => {}};
  f.render();
  const {label, of} = f.page().props.chapter;
  const line = `${label} ${of}`;
  assert.ok(line.includes('Three')); assert.ok(line.endsWith('of 3'), line);
  assert.doesNotMatch(line, /\d+\/\d+|undefined/);
});

test('phone audiobook passes the three options, open chapter state and whole-book remaining line', async () => {
  const f = await fixture(true);
  for (const id of ['speed', 'sleep', 'chapters-toggle']) assert.ok(f.button(id), id);
  assert.ok(f.page().props.times.middle);
  f.button('chapters-toggle').props.onPress(); f.render();
  assert.equal(f.button('chapters-toggle').props.active, true);
});

test('tablet audiobook ticks mark actual chapter boundaries within the full-book seek range', async () => {
  const f = await fixture(true, 1180), ticks = f.page().props.scrubber;
  assert.deepEqual(ticks.props.ticks, [60 / 180, 120 / 180]);
  assert.equal(ticks.props.children[0].props.span, 180);
});
