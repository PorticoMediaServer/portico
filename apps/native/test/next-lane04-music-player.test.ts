/** Real screen composition and callbacks against explicit ports. This shallow
 * harness does not prove native blur, gestures, focus reconciliation or geometry. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule, listeningCoverFixture} from './helpers/screen-harness.mjs';
import * as chapters from '../src-next/screens/destinations/music/audiobook-controls.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {GestureCommitScope} from '../src-next/kit/controls/gesture-commit.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
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
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: any) => key === 'player.opt.minutes' ? `${values.count} min` : key}},
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
    column: () => nodes(tree).find(node => node.type?.name === 'ListeningColumn')};
}

test('phone music uses full 24-point-gutter cover, prominent transport and remaining time', async () => {
  const f = await fixture();
  assert.equal(f.column().props.cover, 342);
  const header = nodes(f.column().props.header);
  assert.equal(header.filter(node => node.type === 'Button').length, 2);
  assert.equal(header.find(node => node.props.icon === 'i-down').props.style.height, 48);
  assert.equal(f.button('toggle').props.style.width, 76);
  assert.equal(f.button('previous').props.variant, 'text');
  assert.equal(f.button('previous').props.style.width, 48);
  assert.equal(f.button('close'), undefined);
  assert.ok(f.nodes().some(node => node.type === 'Text' && text(node) === '-1:50'));
});

test('phone queue lives in a dismissible BottomSheet while the player stays mounted', async () => {
  const f = await fixture();
  f.button('queue-toggle').props.onPress(); f.render();
  const sheet = f.nodes().find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.open, true); assert.ok(f.column());
  assert.ok(nodes(sheet).some(node => node.type === 'QueuePaint'));
  sheet.props.onOpenChange(false); f.render();
  assert.equal(f.nodes().find(node => node.type === 'BottomSheet').props.open, false);
});

test('tablet queue toggles closed and restores the centred column with the specified glass panel', async () => {
  const f = await fixture(false, 1180);
  const side = f.nodes().find(node => node.type?.name === 'ListeningSidePanel'); assert.equal(side.props.width, 380);
  const rendered = side.type(side.props);
  assert.equal(rendered.props.style.top, 100); assert.equal(rendered.props.style.right, 20);
  assert.equal(rendered.props.style.bottom, 28); assert.equal(rendered.props.style.borderRadius, 20);
  assert.equal(nodes(rendered).find(node => node.type === 'BlurView').props.blurAmount, 30);
  assert.equal(f.button('toggle').props.style.width, 74);
  assert.equal(f.button('previous').props.style.width, 52);
  f.button('queue-toggle').props.onPress(); f.render();
  assert.equal(f.nodes().some(node => node.type?.name === 'ListeningSidePanel'), false);
  assert.equal(f.column().props.cover, 410);
});

test('audiobook controls use configured skips and canonical chapter/rate/sleep ports', async () => {
  const f = await fixture(true);
  assert.equal(f.button('shuffle'), undefined); assert.equal(f.button('repeat'), undefined);
  f.button('skip-back').props.onPress(); f.button('skip-forward').props.onPress();
  assert.deepEqual(f.commands.map(command => command.seconds), [55, 100]);
  f.button('next-chapter').props.onPress(); await Promise.resolve(); await Promise.resolve();
  assert.equal(f.commands.at(-1).seconds, 120);
  f.button('speed').props.onPress(); f.render();
  const speed = f.button('speed-menu'); assert.equal(speed.props.open, true);
  speed.props.items.find((item: any) => item.id === '1.5').onSelect(); assert.deepEqual(f.rates, [1.5]);
  speed.props.onOpenChange(false); f.render();
  f.button('sleep').props.onPress(); f.render();
  f.button('sleep-menu').props.items.find((item: any) => item.id === '30').onSelect(); await Promise.resolve();
  assert.deepEqual(f.sleeps, [30]);
  f.retire(); speed.props.items[0].onSelect(); assert.deepEqual(f.rates, [1.5]);
});

test('landscape audiobook uses its horizontal cover/identity composition and a Chapters panel', async () => {
  const f = await fixture(true, 1180);
  assert.equal(f.column().props.cover, 250); assert.equal(f.column().props.horizontal, true);
  assert.ok(f.column().props.identity);
  assert.ok(f.button('chapters')); assert.ok(f.button('tab-chapters'));
  assert.equal(f.nodes().find(node => node.type === 'BottomSheet').props.open, false);
});


test('phone audiobook options use 54pt glass faces with the active ring and whole-book remaining line', async () => {
  const f = await fixture(true);
  const style = (node: any) => Object.assign({}, ...[node.props.style].flat().filter(Boolean));
  for (const id of ['speed', 'sleep', 'chapters-toggle']) {
    const option = f.button(id);
    assert.equal(style(option).height, 54);
    assert.equal(style(option).backgroundColor, '#edf5fa:0.12');
    assert.equal(nodes(option).find(node => node.type === 'BlurView').props.blurAmount, 16);
  }
  assert.ok(f.button('book-remaining'));
  f.button('chapters-toggle').props.onPress(); f.render();
  assert.equal(style(f.button('chapters-toggle')).backgroundColor, '#409fd4:0.22');
  assert.equal(style(f.button('chapters-toggle')).borderWidth, 1);
  assert.equal(style(f.button('chapters-toggle')).borderColor, '#5cb3e2:0.5');
});

test('tablet audiobook ticks mark actual chapter boundaries within the full-book seek range', async () => {
  const f = await fixture(true, 1180), ticks = f.button('chapter-ticks');
  const marks = nodes(ticks).filter(node => node !== ticks);
  assert.equal(marks.length, 2);
  assert.deepEqual(marks.map(node => node.props.style.left), [`${60 / 180 * 100}%`, `${120 / 180 * 100}%`]);
  assert.equal(ticks.props.pointerEvents, 'none');
  assert.equal(f.button('scrubber').props.span, 180);
});
