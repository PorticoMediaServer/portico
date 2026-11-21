/** Shallow real screen projections and callback wiring; native layout requires a device. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {expandWindows, musicWindowStub} from './helpers/music-window-stub.ts';
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const content = (node: any): string => [node?.props?.children].flat(Infinity).filter(value => typeof value === 'string' || typeof value === 'number').join('');
const colors = {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#777', accentStrong: '#5cb3e2', line: '#3b5668', raised: '#112631', shadow: '#000'};
const type = (_theme: unknown, fontSize: number, fontWeight: number, letterSpacing = 0) => ({fontSize, fontWeight, letterSpacing});
const i18n = {t: (key: string) => key, number: String, duration: (value: number) => `${value}s`, list: (items: string[]) => items.join(', ')};
const runtime = {artwork: () => ({cacheKey: 'art'}), ambient: () => ({cacheKey: 'ambient'}), text: {locale: 'en-US'}, reportError: (error: unknown) => {throw error;}};

// A phone's album and artist pages are rebuilt (pages/touch/music): their song rows are TrackWindow's (next-track-window).

async function audiobook() {
  const ui = hooks(), theme = {isTV: false, platform: 'phone', colors};
  const common = {react: ui.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})}, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../kit/artwork': {useWholeBox: (_source: unknown, width: number, height: number) => ({box: {width, height}, onSize: undefined}), missingArt: () => undefined, StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', PersonCircle: 'PersonCircle'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/browse': {TrackNumber: 'TrackNumber'}, '../../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`, browseType: type},
    '../../../kit/theme': {useTheme: () => theme}, '../music/MusicWindow': musicWindowStub};
  const {BookScreen} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/BookScreen.tsx', import.meta.url), {...common,
      '../../../../../../packages/client-core/src/presentation/errors': {}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/states': {StateView: 'StateView'},
      '../../../../../../packages/client-core/src/presentation/server-text': {}, '../../downloads/request': {askNativeDownload() {}}, '../../watch/download-request': {nativeDownloadRequest: () => ({})}})
  const progress = {itemId: 'file', positionMs: 60_000, durationMs: 180_000, remainingMs: 120_000, partPositionMs: 60_000};
  const entry = {id: 'book', libraryId: 'books', kind: 'book', title: 'Book', duration: 180, narrators: ['Narrator'], bookProgress: progress};
  let starts = 0, menu: any[] = [];
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', entity: entry, sections: [], finished: false}), startOver: async () => {starts++;}};
  const tree = expandWindows(ui.render(() => BookScreen({controller, entry, runtime, actions: {menu: (...args: any[]) => {menu = args;}}, openEntry() {}, onBack() {}})));
  return {tree, starts: () => starts, menu: () => menu};
}

test('book progress follows the three primary actions and Start over remains in More', async () => {
  const f = await audiobook(), all = nodes(f.tree);
  // The phone hero is the kit's cover hero: its actions, then the progress under them (the kit draws `under` after `actions`).
  const hero = all.find(node => node.type === 'CoverHero'), heroActions = nodes(hero.props.actions);
  assert.equal(hero.props.shape, 'square'); assert.equal(hero.props.layout, 'stack');
  assert.ok(heroActions.some(node => node.props.focusId === 'book-book.resume'));
  assert.ok(nodes(hero.props.under).some(node => node.type === 'Text' && content(node) === 'title.resumeRemaining'));
  assert.equal([...all, ...heroActions].some(node => node.props.focusId === 'book-book.start'), false);
  const more = all.find(node => node.type === 'TopBar').props.trailing; assert.equal(more.props.icon, 'i-more'); more.props.onPress();
  const action = f.menu()[3].find((value: any) => value.id === 'book-start-over'); assert.ok(action); await action.onSelect();
  assert.equal(f.starts(), 1);
});
