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

test('a toolbar with a view toggle and Save draws no bare text between them', async () => {
  for (const width of [390, 820]) {
    const host = hooks(), theme = createTheme({width, isTV: false});
    const {BrowseToolbar} = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
      react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width})},
      '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
      '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
      '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, '../controls/control-model': {chipGeometry: () => ({height: 34})},
    '../controls': {Button: 'Button', Chip: 'Chip', Segmented: 'Segmented', Focusable: 'Focusable'}, '../overlays': {MenuRow: 'MenuRow', Popover: 'Popover'},
      '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, '../layout/TVRow': {TVRow: 'TVRow'}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
      'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
      './presentation': {alpha: (color: string) => color, browsePlatform: () => (width < 600 ? 'phone' : 'desktop'), browseType: () => ({})},
    });
    const tree = host.render(() => BrowseToolbar({model: {quick: [], applied: [], sorts: [], sort: {field: 'title', direction: 'asc', flippable: false}},
      labels: {sort: () => '', quick: () => ''}, resultCount: '8 books', onQuickChange() {}, onFilters() {}, onSortChange() {},
      view: {value: 'grid', onValueChange() {}}, save: {canSave: true, onSave() {}}}));
    const nodes = elements(tree), drawn = nodes.map(node => node.type);
    assert.ok(drawn.some(type => (type as {name?: string})?.name === 'ViewToggle'), `${width}: the view toggle is drawn`);
    // Owner, 18 Nov: a phone or tablet toolbar is one row (Filters, sort, the chips' rail, grid / list): no Save view, no count line.
    assert.ok(!drawn.some(type => (type as {name?: string})?.name === 'SaveViewButton'), `${width}: no Save view on a touch toolbar`);
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
    '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, '../controls/control-model': {chipGeometry: () => ({height: 34})},
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

test('SMALL-FAULTS 29: the chip rail fades out from the end of the last whole chip, so a partly visible chip is never drawn', async () => {
  const {chipRailCut} = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
    react: hooks().react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
    '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, '../controls/control-model': {chipGeometry: () => ({height: 34})},
    '../controls': {}, '../overlays': {}, '../Icon': {}, '../theme': {useTheme: () => createTheme({width: 390, isTV: false})}, '../layout/TVRow': {}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, './presentation': {},
  });
  // Chips end at 90, 190, 300 in a 140-wide rail: only the first fits whole, so the rail is clear from 90 on.
  assert.equal(chipRailCut(140, 0, 300, [90, 190, 300]), 90);
  assert.equal(chipRailCut(40, 0, 300, [90, 190, 300]), 0, 'a rail narrower than its first chip draws none (the Pixel\'s stray "(")');
  assert.equal(chipRailCut(140, 60, 300, [90, 190, 300]), 130, 'scrolled: the cut follows the chips');
  assert.equal(chipRailCut(400, 0, 300, [90, 190, 300]), undefined, 'every chip in view: no fade');
  assert.equal(chipRailCut(140, 160, 300, [90, 190, 300]), undefined, 'scrolled to the end: no fade');
});

test('the native chip rail is drawn only with room for its first chip whole (lead, 19 Nov)', async () => {
  const host = hooks();
  const module = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
    react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
    '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, '../controls/control-model': {chipGeometry: () => ({height: 34})},
    '../controls': {}, '../overlays': {}, '../Icon': {}, '../theme': {useTheme: () => createTheme({width: 390, isTV: false})}, '../layout/TVRow': {TVRow: 'TVRow'}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, './presentation': {},
  });
  const draw = () => elements(host.render(() => module.ChipRail({children: ['Unplayed', 'In progress']})));
  let nodes = draw();
  nodes.find(node => node.type === 'ScrollView')!.props.onLayout({nativeEvent: {layout: {width: 40}}});
  const cells = draw().filter(node => node.type === 'View' && node.props.onLayout);
  cells[0]!.props.onLayout({nativeEvent: {layout: {x: 0, width: 96}}}); cells[1]!.props.onLayout({nativeEvent: {layout: {x: 104, width: 110}}});
  nodes = draw();
  const rail = nodes.find(node => node.props.testID === 'browse-chip-rail')!;
  assert.equal(rail.props.style.opacity, 0, 'no room for the first chip: not drawn');
  assert.equal(rail.props.pointerEvents, 'none'); assert.equal(rail.props.importantForAccessibility, 'no-hide-descendants');
  assert.equal(nodes.find(node => node.type === 'ScrollView')!.props.scrollEnabled, false, 'no hidden swipe area');
  nodes.find(node => node.type === 'ScrollView')!.props.onLayout({nativeEvent: {layout: {width: 140}}});
  assert.equal(draw().find(node => node.props.testID === 'browse-chip-rail')!.props.style.opacity, 1, 'room for the first chip: drawn');
});

test('two chip rails on screen at once each fade with their own gradient (a rail under See all lent it its fade)', async () => {
  const host = hooks();
  const module = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {
    react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 820})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {sortDirectionKind: () => 'alpha'},
    '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, '../controls/control-model': {chipGeometry: () => ({height: 34})},
    '../controls': {}, '../overlays': {}, '../Icon': {}, '../theme': {useTheme: () => createTheme({width: 390, isTV: false})}, '../layout/TVRow': {TVRow: 'TVRow'}, '@react-native-masked-view/masked-view': {default: 'MaskedView'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, './presentation': {},
  });
  const ids = [hooks(), hooks()].map(h => {
    const mod = h.render(() => module.ChipRail({children: ['A']}));
    return elements(elements(mod).find(node => node.type === 'MaskedView')!.props.maskElement).find(node => node.type === 'LinearGradient')!.props.id;
  });
  assert.notEqual(ids[0], ids[1]);
});
