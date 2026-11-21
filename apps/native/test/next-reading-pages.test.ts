import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('the reading library page keeps its tabs, library picker and selection above the selected shelf', async () => {
  const h = hooks(), actions: string[] = [];
  const publicLibrary = {id: 'public', name: 'Public Domain Books'};
  const readingActions = {}, model = {name: publicLibrary.name, counts: [],
    tab: 'discover', tabs: [{id: 'discover', label: 'home.row.recentlyAdded'}, {id: 'browse', label: 'saved.title'}],
    selectTab: (id: string) => actions.push(id), readingLibraries: [publicLibrary], openLibrary: (id: string) => actions.push(id),
    waiting: false, showSelection: true, readingActions, startDownload: () => actions.push('download')};
  const {BooksPage} = await screenModule(new URL('../src-next/pages/touch/reading/BooksPage.tsx', import.meta.url), {
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/controls': {Button: 'Button'}, '../../../screens/read/ReadingBooks': {NativeReadingBooks: 'Grid'}, './ReadingPeriodicalShelf': {ReadingPeriodicalShelf: 'PeriodicalShelf'},
    react: h.react, '../../../kit/browse': {LibrarySwitcher: 'LibrarySwitcher'}, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
    '../../../kit/reading/ReadingLibraryParts': {ReadingLibraryBody: 'Body', ReadingDiscovery: 'Discovery', ReadingFallbackGrid: 'Books',
      useReadingLibraryLayout: () => ({tablet: false, reportWidth() {}})},
    '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/reading/ReadingActions': {ReadingSelectionToolbar: 'Toolbar', ReadingItemMenu: 'ItemMenu'},
    '../../shared/reading/useBooksLibrary': {useBooksLibrary: () => model},
  });
  const render = () => h.render(() => BooksPage({libraryId: 'public', onBack: () => actions.push('library picker')}));
  const tree = render(), [frame, menu, download] = tree.props.children;
  assert.equal(frame.props.grid.type, 'Discovery');
  assert.equal(frame.props.toolbar.props.port, readingActions);
  assert.equal(menu.props.port, readingActions);
  frame.props.header.tabs.onValueChange('browse');
  frame.props.header.switcher.props.onValueChange('comics');
  frame.props.header.back.onPress();
  download.props.items[0].onSelect();
  assert.deepEqual(actions, ['browse', 'comics', 'library picker', 'download']);
  model.tab = 'browse';
  assert.equal(render().props.children[0].props.grid.type, 'Books');
});
