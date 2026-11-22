import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];

test('Book keeps the chapter and details panes on a wide tablet and offers More on TV', async () => {
  const host = hooks();
  const calls: string[] = [], reading = () => calls.push('read'), more = () => calls.push('more');
  let form = 'expanded';
  const anchor = {current: null};
  const view = {id: 'book', title: 'Book', primary: {label: 'Read', progress: .4, onPress: reading},
    contents: {rows: [{id: 'chapter'}], onPlay: (id: string) => calls.push(id)}, tabs: {value: 'contents'}, tab: 'contents',
    details: [{label: 'Format', values: [{text: 'EPUB'}]}], actions: [{icon: 'i-dl', anchorRef: anchor}],
    menu: {open: false, items: []}, versions: {open: false, items: []}, more};
  const {ReadBookPage} = await screenModule(new URL('../src-next/pages/touch/read/ReadBookPage.tsx', import.meta.url), {
    react: host.react, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
    '../../../kit/frames': {AboutTable: 'AboutTable', musicScroller: () => undefined, PanelBlock: 'PanelBlock',
      PanelHeading: 'PanelHeading', TitleFrame: 'TitleFrame', TitleSection: 'TitleSection'},
    '../../../kit/layout': {HeroMetadata: 'HeroMetadata'}, '../../../kit/music/TrackWindow': {TrackWindow: 'TrackWindow'},
    '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/theme': {useTheme: () => ({form, isTV: form === 'tv'})},
    '../../shared/read/useReadBook': {useReadBook: () => view},
  });
  const render = () => nodes(ReadBookPage({onBack() {}})).find(node => node.type === 'TitleFrame');
  const tablet = render();
  assert.equal(tablet.props.tabs, undefined);
  assert.ok(nodes(tablet.props.aside).some(node => node.type === 'AboutTable'));
  const chapter = nodes(tablet).find(node => node.type === 'TrackWindow');
  chapter.props.onPlay('chapter');
  assert.equal(tablet.props.actions[0].anchorRef, anchor);
  form = 'medium';
  const upright = render();
  assert.equal(upright.props.tabs.value, 'contents');
  assert.equal(upright.props.aside, undefined);
  form = 'tv';
  const tv = render();
  assert.equal(tv.props.topAction, undefined);
  tv.props.actions.find((action: any) => action.testID === 'read-book-more').onPress();
  tv.props.primary.onPress();
  assert.deepEqual(calls, ['chapter', 'more', 'read']);
  // The host's Select is for a series' volumes: a book with no volumes row draws none (iPad, 22 Nov: a lone Select
  // floated above Contents); with the row it is drawn.
  form = 'expanded';
  const bar = 'SelectionBar';
  assert.equal(nodes(ReadBookPage({onBack() {}, selectionBar: bar})).find(node => node.type === 'TitleFrame').props.selectionBar, undefined);
  Object.assign(view, {shelf: {title: 'Series', items: []}});
  assert.equal(nodes(ReadBookPage({onBack() {}, selectionBar: bar})).find(node => node.type === 'TitleFrame').props.selectionBar, bar);
});
