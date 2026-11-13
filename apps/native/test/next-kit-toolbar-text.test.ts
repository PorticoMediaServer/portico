/** The kit browse toolbar draws no bare text: React Native refuses a string outside <Text>. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {createTheme} from '../src-next/kit/theme.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

type Element = {type: unknown; props: Record<string, any>};
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
}
/** A string or number drawn straight into a View (or a fragment inside one), the child React Native refuses. */
const bareText = (node: Element): unknown[] => node.type === 'Text' ? [] : [node.props.children].flat(Infinity)
  .filter(child => typeof child === 'string' || typeof child === 'number');

test('a toolbar with a view toggle, a card size and Save draws no bare text between them', async () => {
  for (const width of [390, 820]) {
    const host = hooks(), theme = createTheme({width, isTV: false});
    const {BrowseToolbar} = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
      react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width})},
      '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
      '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
      '../controls': {Button: 'Button', Chip: 'Chip', Segmented: 'Segmented', Focusable: 'Focusable'}, '../overlays': {MenuRow: 'MenuRow', Popover: 'Popover'},
      '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, '../layout/TVRow': {TVRow: 'TVRow'}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
      'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
      './presentation': {alpha: (color: string) => color, browsePlatform: () => (width < 600 ? 'phone' : 'desktop'), browseType: () => ({})},
    });
    const tree = host.render(() => BrowseToolbar({model: {quick: [], applied: [], sorts: [], sort: {field: 'title', direction: 'asc', flippable: false}},
      labels: {sort: () => '', quick: () => ''}, resultCount: '8 books', onQuickChange() {}, onFilters() {}, onSortChange() {},
      view: {value: 'grid', onValueChange() {}}, cardSize: {value: 'medium', options: [], onValueChange() {}}, save: {canSave: true, onSave() {}}}));
    const nodes = elements(tree), drawn = nodes.map(node => node.type);
    assert.ok(drawn.some(type => (type as {name?: string})?.name === 'ViewToggle') && drawn.some(type => (type as {name?: string})?.name === 'CardSizeButton'), `${width}: both controls are drawn`);
    for (const node of nodes) assert.deepEqual(bareText(node), [], `${width}: ${String((node.type as {name?: string})?.name ?? node.type)} holds no bare text`);
  }
});

// 13 Nov iPad walk: the tablet toolbar's quick chips were squeezed to "U…", "In p…", "Fi…". A control's label is never cut:
// the chips scroll sideways in a rail whose end fades while more lie past it; nothing else in the toolbar gives way.
test('a tablet toolbar puts its quick chips in a sideways rail that fades at its end only while more chips lie past it', async () => {
  const width = 744, host = hooks(), theme = createTheme({width, isTV: false});
  const module = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
    react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
    '../controls': {Button: 'Button', Chip: 'Chip', Segmented: 'Segmented', Focusable: 'Focusable'}, '../overlays': {MenuRow: 'MenuRow', Popover: 'Popover'},
    '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, '../layout/TVRow': {TVRow: 'TVRow'}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    './presentation': {alpha: (color: string) => color, browsePlatform: () => 'desktop', browseType: () => ({})},
  });
  const quick = ['Unread', 'In progress', 'Finished', 'My List'].map((label, index) => ({id: String(index), active: false, label: {text: label}}));
  const toolbar = elements(host.render(() => module.BrowseToolbar({model: {quick, applied: [], sorts: [], sort: {field: 'title', direction: 'asc', flippable: false}},
    labels: {sort: () => '', quick: () => ''}, resultCount: '2 comics', onQuickChange() {}, onFilters() {}, onSortChange() {}})));
  const rail = toolbar.find(node => node.type === module.ChipRail);
  assert.ok(rail, 'the quick chips sit in the rail');
  assert.equal(elements(rail!.props.children).filter(node => (node.type as {name?: string})?.name === 'QuickToggle').length, 4);
  const draw = () => elements(host.render(() => module.ChipRail({children: 'chips'})));
  const endOpacity = (nodes: Element[]) => elements(nodes.find(node => node.type === 'MaskedView')!.props.maskElement).filter(node => node.type === 'Stop').at(-1)!.props.stopOpacity;
  let nodes = draw();
  assert.equal(endOpacity(nodes), 1, 'nothing measured yet: no fade');
  const scroll = nodes.find(node => node.type === 'ScrollView')!;
  assert.equal(scroll.props.horizontal, true);
  scroll.props.onLayout({nativeEvent: {layout: {width: 300}}}); scroll.props.onContentSizeChange(520, 34);
  nodes = draw(); assert.equal(endOpacity(nodes), 0, 'more chips past the end: the end fades');
  nodes.find(node => node.type === 'ScrollView')!.props.onScroll({nativeEvent: {contentOffset: {x: 220}}});
  assert.equal(endOpacity(draw()), 1, 'scrolled to the end: no fade');
  assert.deepEqual([rail!.props.children].flat().length > 0, true);
});
