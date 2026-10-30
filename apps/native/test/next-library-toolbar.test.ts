import test from 'node:test';
import assert from 'node:assert/strict';
import {createTheme} from '../src-next/kit/theme.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

type Element = {type: unknown; props: Record<string, any>};
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
}
async function fixture(width: number, isTV = false) {
  const host = hooks(), calls: unknown[] = [], theme = createTheme({width, isTV});
  const {LibraryToolbar} = await screenModule(new URL('../src-next/screens/library/LibraryToolbar.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View', ScrollView: 'ScrollView', useWindowDimensions: () => ({width})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip'}, '../../kit/focus': {FocusItem: 'FocusItem'},
    '../../kit/browse': {FiltersButton: 'FiltersButton', QuickToggle: 'QuickToggle', SaveViewButton: 'SaveViewButton'},
    '../../kit/theme': {useTheme: () => theme}, './LibrarySortMenu': {LibrarySortMenu: 'LibrarySortMenu'},
  });
  const start = () => calls.push('actual-library-context');
  const props = {focusScope: 'library:movies', model: {quick: [], filterQuick: [], groups: [], applied: [],
    sorts: [], sort: {field: 'title', direction: 'asc', flippable: false, reshufflable: false}},
    labels: {field: () => '', sort: () => '', quick: () => ''}, filterCount: 1, filtersOpen: true, canSave: false,
    onFilters: () => calls.push('filters'), onSort: () => {}, onReshuffle: () => {}, onQuick: () => {}, onSave: () => {},
    onJump: () => {}, jumpEnabled: false, jumpAnchor: {current: null}, onSelect: start, selecting: false};
  return {calls, start, render: (patch = {}) => host.render(() => LibraryToolbar({...props, ...patch}))};
}

test('the library row never draws its own Select and keeps its drawn controls exact', async () => {
  // Select belongs to the drawn LibraryScreen header on tablet4/5: the medium header
  // below 1024 and the expanded header from 1024 carry `library-select-header`
  // (LibraryScreen.tsx:365,373,375), so this row must never repeat it — not at a
  // tablet width, not while selecting, and not when the caller supplies no port. On
  // the phone the compact header draws no Select either; the intended entry there is
  // the card/list menu forwarding selection.start through
  // `actions.menu(entry, anchor, onSelect)`, so this row still invents none.
  for (const width of [390, 599, 600, 820, 1180]) {
    const f = await fixture(width), tree = f.render(), nodes = elements(tree);
    assert.equal(nodes.some(node => node.props.testID === 'library-select'), false, 'the row never draws a second Select');
    assert.deepEqual(f.calls, [], 'the row never invokes the header action by itself');
    assert.equal(nodes.find(node => node.type === 'FiltersButton')?.props.open, true, 'actual draft open state reaches the control');
    assert.equal(tree.type, width < 600 ? 'ScrollView' : 'View', width < 600 ? 'the phone keeps the scrolling strip' : 'the drawn tablet row replaces the strip');
    // 'Button' stays in the filter so an unexpected row button fails this order instead
    // of escaping it; the expected shape is the drawn filters control, then sort.
    const controls = nodes.filter(node => ['Button', 'FiltersButton', 'LibrarySortMenu'].includes(node.type as string)).map(node => node.type);
    assert.deepEqual(controls, ['FiltersButton', 'LibrarySortMenu']);
    assert.equal(elements(f.render({onSelect: undefined})).some(node => node.props.testID === 'library-select'), false, 'a missing real port never creates a dummy action');
    assert.equal(elements(f.render({selecting: true})).some(node => node.props.testID === 'library-select'), false);
  }
  const tv = await fixture(1920, true), nodes = elements(tv.render());
  assert.equal(nodes.some(node => node.props.testID === 'library-select'), false);
  assert.deepEqual(nodes.filter(node => ['Button', 'LibrarySortMenu', 'FiltersButton', 'Chip'].includes(node.type as string)).map(node => node.type), ['LibrarySortMenu', 'FiltersButton', 'Chip']);
});
