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

async function music(platform = 'tablet') {
  const ui = hooks(), theme = {platform, isTV: false, colors}, react: any = {...ui.react, useId: () => 'wash'}; react.default = react;
  const module = await screenModule(new URL('../src-next/screens/destinations/music/PhoneMusicScreens.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})}, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({detail: [{id: 'duration', text: '3:00'}]})},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork', AmbientArt: 'AmbientArt', ArtistCircle: 'ArtistCircle'},
    '../../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'}, '../../../kit/cards': {CircleCard: 'CircleCard', SquareCard: 'SquareCard'},
    '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/browse/presentation': {browseType: type, alpha: (color: string, opacity: number) => `${color}/${opacity}`},
    '../../../kit/theme': {useTheme: () => theme}, './MusicSectionControls': {MusicSectionControls: 'PageControls'},
    '../../../../../../packages/client-core/src/presentation/library-tabs': {artistReleaseGroups: () => []}, './MusicErrorView': {MusicErrorView: 'Error'},
    './MusicFormatBadges': {MusicFormatBadges: 'Badges'}, './use-playing-item': {usePlayingItem: () => 'song'},
    './MusicWindow': {MusicScrollView: 'MusicScrollView', MusicWindow: 'MusicWindow', MusicShelf: 'MusicShelf'}, '../../../kit/selection': {SelectionCircle: 'SelectionCircle'},
  });
  const song = {id: 'song', kind: 'song', title: 'Song', libraryId: 'music', duration: 180}, artist = {id: 'artist', kind: 'artist', libraryId: 'music', title: 'Artist'};
  const entry = {id: 'album', kind: 'album', libraryId: 'music', title: 'Album', artist: {id: 'artist', name: 'Artist'}};
  const page = {phase: 'ready', items: [song], offset: 0}, snapshot = {phase: 'ready', songs: [song], songPage: page, releases: {items: []}, similar: [], favorite: false};
  let selecting = false, selected = false, plays = 0, favorites = 0, starts = 0, menus: any[] = [], opened: any[] = [];
  const enter = () => {selecting = true;}, toggle = () => {selected = !selected;};
  const selection = {subscribe: () => () => {}, getSnapshot: () => 0, start: () => {starts++;}, item: () => ({active: selecting, selected, onEnter: enter, onToggle: toggle})};
  const controller: any = {getSnapshot: () => snapshot, subscribe: () => () => {}, artistEntry: () => artist, selectionProjection: () => ({}),
    play: async () => {plays++;}, songFavorite: () => false, songFavoritePending: () => false, toggleSongFavorite: async () => {favorites++;}};
  const props: any = {controller, entry, runtime, selection, actions: {menu: (...args: any[]) => menus.push(args)}, openEntry: (value: any) => opened.push(value), onBack() {}};
  const tree = ui.render(() => module.PhoneAlbumScreen(props));
  const renderChild = (node: any) => ui.render(() => node.type(node.props));
  return {tree, song, artist, controller, selection, renderChild, enter, toggle, menus, opened, counts: () => ({plays, favorites, starts, selected}), ui};
}

test('tablet album matches hero/table metrics and exposes the canonical artist link and Select toolbar', async () => {
  const f = await music();
  const all = nodes(f.tree);
  const cover = all.find(node => node.type === 'Artwork'); assert.equal(cover.props.width, 260);
  const title = all.find(node => node.type === 'Text' && content(node) === 'Album'); assert.equal(style(title).fontSize, 80);
  const link = all.find(node => node.type === 'Focusable' && node.props.accessibilityRole === 'link');
  assert.equal(link.props.style.minHeight, 44); link.props.onPress(); assert.deepEqual(f.opened, [f.artist]);
  const select = all.find(node => node.type === 'Chip'); select.props.onPress(); assert.equal(f.counts().starts, 1);
  const list = all.find(node => node.type === 'MusicWindow'); assert.equal(list.props.itemHeight, 60);
  const rowElement = nodes(list.props.renderItem(f.song, 0)).find(node => node.type?.name === 'TabletSongLine');
  const row = f.renderChild(rowElement);
  assert.equal(row.props.style.height, 60); assert.equal(row.props.style.backgroundColor, '#edf5fa/0.08');
  const favorite = nodes(row).find(node => node.props.icon === 'i-heart');
  assert.equal(favorite.props.style.width, 44); favorite.props.onPress(); assert.equal(f.counts().favorites, 1);
});

test('phone song long press opens its anchored menu with Select; selected taps toggle instead of play', async () => {
  const f = await music('phone');
  const lines = nodes(f.tree).find(node => node.type?.name === 'SongLines');
  const window = f.renderChild(lines);
  const rowElement = nodes(window.props.renderItem(f.song, 0)).find(node => node.type?.name === 'SongLine');
  let row = f.renderChild(rowElement);
  row.props.onLongPressAction();
  assert.equal(f.menus[0][0], f.song); assert.ok(f.menus[0][1]); assert.equal(f.menus[0][2], f.enter);
  f.menus[0][2](); row = f.renderChild(rowElement);
  assert.equal(row.props.accessibilityRole, 'checkbox'); row.props.onPress();
  assert.equal(f.counts().selected, true); assert.equal(f.counts().plays, 0);
});

test('album wash keeps its ambient treatment and fades continuously to ink', async () => {
  for (const platform of ['phone', 'tablet']) {
    const f = await music(platform), wash = nodes(f.tree).find(node => node.type?.name === 'Wash');
    const tree = f.renderChild(wash), all = nodes(tree);
    assert.equal(tree.props.style.height, platform === 'phone' ? 760 : 700);
    assert.ok(all.some(node => node.type === 'AmbientArt'));
    assert.deepEqual(all.filter(node => node.type === 'Stop').map(node => [node.props.offset, node.props.stopOpacity]), [['0.25', 0], ['1', 1]]);
  }
});

async function audiobook(kind: 'book' | 'author') {
  const ui = hooks(), theme = {isTV: false, platform: 'phone', colors};
  const common = {react: ui.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})}, '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', PersonCircle: 'PersonCircle'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`, browseType: type},
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
  const resume = all.findIndex(node => node.props.focusId === 'book-book.resume');
  const progress = all.findIndex(node => node.type === 'Text' && content(node) === 'title.resumeRemaining');
  assert.ok(progress > resume);
  assert.equal(all.some(node => node.props.focusId === 'book-book.start'), false);
  const more = all.find(node => node.type === 'Button' && node.props.icon === 'i-more'); more.props.onPress();
  const action = f.menu()[3].find((value: any) => value.id === 'book-start-over'); assert.ok(action); await action.onSelect();
  assert.equal(f.starts(), 1);
});

test('author header, book rows and grouped details retain their specified hierarchy', async () => {
  const f = await audiobook('author'), all = nodes(f.tree);
  assert.equal(all.find(node => node.type === 'PersonCircle').props.size, 150);
  assert.ok(all.some(node => node.type === 'Text' && content(node) === 'AW' && style(node).fontSize === 54));
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
