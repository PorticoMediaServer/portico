import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('the phone books page keeps Discover, Books and the library picker above the real reading list', async () => {
  const h = hooks(), actions: string[] = [];
  const {BooksPage} = await screenModule(new URL('../src-next/pages/touch/reading/BooksPage.tsx', import.meta.url), {
    react: h.react, '../../../kit/browse': {LibrarySwitcher: 'LibrarySwitcher'}, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
  });
  const tree = h.render(() => BooksPage({title: 'Public Domain Books', library: {libraries: [{id: 'public', name: 'Public Domain Books'}],
    value: 'public', onValueChange: (id: string) => actions.push(id)}, back: {label: 'Libraries', onPress: () => actions.push('library picker')},
    tabs: {items: [{value: 'discover', label: 'Discover'}, {value: 'browse', label: 'Books'}], value: 'discover',
      onValueChange: (id: string) => actions.push(id)}, toolbar: 'Selection controls', body: 'Books', overlay: 'Item menu'}));
  const frame = tree.props.children[0];
  assert.equal(frame.props.grid, 'Books');
  assert.equal(frame.props.toolbar, 'Selection controls');
  assert.equal(tree.props.children[1], 'Item menu');
  frame.props.header.tabs.onValueChange('browse');
  frame.props.header.switcher.props.onValueChange('comics');
  frame.props.header.back.onPress();
  assert.deepEqual(actions, ['browse', 'comics', 'library picker']);
});
