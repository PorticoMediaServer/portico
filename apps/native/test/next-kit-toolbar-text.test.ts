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
      '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, '../layout/TVRow': {TVRow: 'TVRow'},
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
