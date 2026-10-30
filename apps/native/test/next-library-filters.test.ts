import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {browseToolbarModel} from '../../../packages/client-core/src/presentation/kit-model';
import {libraryFilterChange, libraryFilterField} from '../src-next/screens/library/filter-controls';
import {nativeLibraryCanSave} from '../src-next/screens/library/model';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

function nodes(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  return node && typeof node === 'object' && 'props' in node ? [node, ...nodes(node.props.children)] : [];
}
async function fixture(isTV: boolean, width = isTV ? 1920 : 390) {
  const host = hooks(), calls: any[] = [], theme = createTheme({width, isTV});
  const field = {id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
  const capability = {library: {id: 'actual-library', kind: 'movie'}, fields: [field], sorts: [], quickFilters: [], resolvedPivot: null};
  const original = {field: 'genre', operator: 'contains-any', value: ['Drama']};
  const staged = {field: 'genre', operator: 'contains-any', value: ['Comedy']};
  const state = {open: true, predicates: [staged], count: {phase: 'ready', value: {total: 19, totalIsLowerBound: false}}, facets: {}};
  const source = {phase: 'ready', library: capability.library, capabilities: capability, predicates: [original], sort: {field: 'title', direction: 'asc'},
    defaultSort: {field: 'title', direction: 'asc'}, tab: {id: 'browse', pivot: 'movies'}};
  const draft = {getSnapshot: () => state, subscribe() {}, setPredicates: (value: unknown) => calls.push(['predicates', value]), apply: () => {calls.push('apply'); return true;}, clear: () => calls.push('clear'), search() {}, retryFacet() {}};
  const runtime = {text: {t: defaultI18n.t}, getSnapshot: () => ({authority: {serverId: 'server', epoch: 'epoch'}}), reportError: (error: unknown) => {throw error;}};
  const {LibraryFilters} = await screenModule(new URL('../src-next/screens/library/LibraryFilters.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/browse': {FilterBuilder: 'FilterBuilder'}, '../../kit/controls': {Button: 'Button'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/focus': {useFocusRegistry: () => ({focus: (address: unknown) => calls.push(['focus', address])})},
    '../../kit/overlays': {BottomSheet: 'BottomSheet', SidePanel: 'SidePanel'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme}, './filter-controls': {libraryFilterChange, libraryFilterField},
    './model': {nativeLibraryCanSave},
  });
  const tree = host.render(() => LibraryFilters({draft, source, runtime, labels: {field: () => 'Genre'}, owner: true, onClose: () => calls.push('close'), onSave: (value: unknown) => calls.push(['save', value])}));
  return {tree, calls, state, source};
}

test('phone filter-sheet detents preserve the existing tablet defaults and TV panel', async () => {
  const phone = await fixture(false, 390);
  assert.equal(phone.tree.type, 'BottomSheet');
  assert.deepEqual(phone.tree.props.detents, [580, 698]);
  assert.equal(phone.tree.props.defaultDetent, 1);
  const tablet = await fixture(false, 768);
  assert.equal(tablet.tree.type, 'BottomSheet');
  assert.deepEqual(tablet.tree.props.detents, [580, 780]);
  assert.equal(tablet.tree.props.defaultDetent, undefined, 'tablet retains the BottomSheet default detent zero');
  const tv = await fixture(true);
  assert.equal(tv.tree.type, 'SidePanel');
  assert.equal(tv.tree.props.width, 680);
  assert.equal(tv.tree.props.detents, undefined);
});

test('TV Library filters enter the actual first field and Apply returns through the real staged controller', async () => {
  const {tree, calls, state} = await fixture(true);
  assert.equal(tree.type, 'SidePanel'); assert.equal(tree.props.width, 680); assert.equal(tree.props.topInset, 0);
  const builder = nodes(tree).find(node => node.type === 'FilterBuilder');
  tree.props.initialFocusRef.current.focus();
  assert.deepEqual(calls[0], ['focus', {regionId: tree.props.testID, childId: JSON.stringify([JSON.stringify([builder.props.focusPrefix, 'field', 'genre']), 'open'])}]);
  const apply = nodes(tree.props.footer).find(node => node.props.focusId === 'apply');
  assert.equal(apply.props.children[0], 'Show 19 movies'); apply.props.onPress(); assert.deepEqual(calls.slice(1), ['apply', 'close']);
  builder.props.onFieldChange('genre', {kind: 'values', included: ['Drama'], excluded: [], mode: 'any'});
  assert.deepEqual(calls.at(-1), ['predicates', [{field: 'genre', operator: 'contains-any', value: ['Drama']}]]);
  state.count.phase = 'loading';
});

test('Phone Library filters put Clear all in the heading and save the actual staged query, not the mounted grid query', async () => {
  const {tree, calls, source, state} = await fixture(false);
  assert.equal(tree.type, 'BottomSheet');
  const clear = tree.props.headerAction; assert.equal(clear.props.focusId, 'clear'); clear.props.onPress(); assert.equal(calls[0], 'clear');
  const save = nodes(tree.props.footer).find(node => node.props.focusId === 'save-view'); assert.equal(save.props.disabled, false); save.props.onPress();
  assert.deepEqual(calls[1], ['save', {predicates: state.predicates, sort: source.sort}]);
  assert.notDeepEqual(state.predicates, source.predicates);
});
