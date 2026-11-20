/** Shallow real screen projections and callback wiring; native layout requires a device. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {expandWindows, musicWindowStub} from './helpers/music-window-stub.ts';
import {audiobookAuthorInitials} from '../src-next/screens/destinations/audiobooks/presentation.ts';
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const content = (node: any): string => [node?.props?.children].flat(Infinity).filter(value => typeof value === 'string' || typeof value === 'number').join('');
const style = (node: any) => Object.assign({}, ...[node?.props?.style].flat(Infinity).filter(Boolean));
const colors = {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#777', accentStrong: '#5cb3e2', line: '#3b5668', raised: '#112631', shadow: '#000'};
const type = (_theme: unknown, fontSize: number, fontWeight: number, letterSpacing = 0) => ({fontSize, fontWeight, letterSpacing});
const i18n = {t: (key: string) => key, number: String, duration: (value: number) => `${value}s`, list: (items: string[]) => items.join(', ')};
const runtime = {artwork: () => ({cacheKey: 'art'}), ambient: () => ({cacheKey: 'ambient'}), text: {locale: 'en-US'}, reportError: (error: unknown) => {throw error;}};

// A phone's album and artist pages are rebuilt (pages/touch/music): their song rows are TrackWindow's (next-track-window).

async function audiobook(kind: 'book' | 'author') {
  const ui = hooks(), theme = {isTV: false, platform: 'phone', colors};
  const common = {react: ui.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})}, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../kit/artwork': {useWholeBox: (_source: unknown, width: number, height: number) => ({box: {width, height}, onSize: undefined}), missingArt: () => undefined, StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', PersonCircle: 'PersonCircle'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/browse': {TrackNumber: 'TrackNumber'}, '../../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`, browseType: type},
    '../../../kit/theme': {useTheme: () => theme}, '../music/MusicWindow': musicWindowStub};
  const screen = kind === 'book'
    ? await screenModule(new URL('../src-next/screens/destinations/audiobooks/BookScreen.tsx', import.meta.url), {...common,
      '../../../../../../packages/client-core/src/presentation/errors': {}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/states': {StateView: 'StateView'},
      '../../../../../../packages/client-core/src/presentation/server-text': {}, '../../downloads/request': {askNativeDownload() {}}, '../../watch/download-request': {nativeDownloadRequest: () => ({})}})
    : await screenModule(new URL('../src-next/screens/destinations/audiobooks/AuthorSeriesScreen.tsx', import.meta.url), {...common,
      './BookScreen': {AudiobookErrorState: 'Error'}, './presentation': {audiobookAuthorInitials}});
  const progress = {itemId: 'file', positionMs: 60_000, durationMs: 180_000, remainingMs: 120_000, partPositionMs: 60_000};
  const entry = {id: kind, libraryId: 'books', kind, title: kind === 'author' ? 'Andy Weir' : 'Book', duration: 180, narrators: ['Narrator'], bookProgress: progress};
  const books = ['a', 'b'].map(id => ({...entry, id, kind: 'book'}));
  let starts = 0, menu: any[] = [];
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', entity: entry, sections: [{id: 'books', entries: books}], finished: false}), startOver: async () => {starts++;}};
  const tree = expandWindows(ui.render(() => screen[kind === 'book' ? 'BookScreen' : 'AuthorSeriesScreen']({controller, entry, runtime, actions: {menu: (...args: any[]) => {menu = args;}}, openEntry() {}, onBack() {}})));
  return {tree, ui, starts: () => starts, menu: () => menu};
}

test('book progress follows the three primary actions and Start over remains in More', async () => {
  const f = await audiobook('book'), all = nodes(f.tree);
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

test('author header, book rows and grouped details retain their specified hierarchy', async () => {
  const f = await audiobook('author'), all = nodes(f.tree);
  // The author's circle and its initials are the kit cover hero's person shape (its size is the kit's own table).
  const hero = all.find(node => node.type === 'CoverHero');
  assert.equal(hero.props.shape, 'person'); assert.equal(hero.props.layout, 'stack'); assert.equal(hero.props.person.initials, 'AW');
  const rowElements = all.filter(node => node.type?.name === 'BookRow');
  const first = f.ui.render(() => rowElements[0].type(rowElements[0].props));
  const second = f.ui.render(() => rowElements[1].type(rowElements[1].props));
  assert.equal(nodes(first).find(node => node.type === 'Artwork').props.width, 66);
  assert.equal(first.props.style.gap, 14); assert.equal(first.props.style.paddingVertical, 10);
  assert.equal(nodes(first).some(node => style(node).height === 1 && style(node).left === 80), false);
  assert.equal(nodes(second).some(node => style(node).height === 1 && style(node).left === 80), true);
  assert.ok(nodes(first).some(node => style(node).left === 5 && style(node).height === 3));
  assert.ok(all.some(node => style(node).borderRadius === 14 && style(node).marginHorizontal === -4 && style(node).backgroundColor === colors.raised));
  assert.ok(all.some(node => node.type === 'Text' && content(node) === 'next.kit.narrators' && style(node).fontSize === 15.5));
});
