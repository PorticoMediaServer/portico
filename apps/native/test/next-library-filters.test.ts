import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {parseBrowseNode, type BrowseCapabilities, type BrowseFieldCapability} from '../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../packages/client-core/src/presentation/kit-model';
import {libraryFilterChange, libraryFilterField} from '../src-next/screens/library/filter-controls';
import {nativeLibraryCanSave} from '../src-next/screens/library/model';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

function nodes(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  return node && typeof node === 'object' && 'props' in node ? [node, ...nodes(node.props.children)] : [];
}
async function fixture(isTV: boolean, width = isTV ? 1920 : 390, loading = false, onExited?: () => void, frame?: {live: boolean}) {
  const host = hooks(), calls: any[] = [], theme = createTheme({width, isTV});
  const field = {id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
  const capability = {library: {id: 'actual-library', kind: 'movie'}, fields: [field], sorts: [], quickFilters: [], resolvedPivot: null};
  const original = {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Drama']};
  const staged = {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Comedy']};
  const state = {open: true, predicates: [staged], count: {phase: 'ready', value: {total: 19, totalIsLowerBound: false}}, facets: {}};
  const source = {phase: 'ready', library: capability.library, capabilities: capability, predicates: [original], sort: {field: 'title', direction: 'asc'},
    defaultSort: {field: 'title', direction: 'asc'}, tab: {id: 'browse', pivot: 'movies'},
    // A library just switched to: its capabilities and sort have not arrived yet.
    ...(loading ? {phase: 'loading', library: {id: 'next-library', kind: 'movie'}, capabilities: undefined, sort: undefined} : {})};
  const draft = {getSnapshot: () => state, subscribe() {}, setPredicates: (value: unknown) => calls.push(['predicates', value]), apply: () => {calls.push('apply'); return true;}, clear: () => calls.push('clear'), search() {}, retryFacet() {},
    dispose: () => calls.push('dispose'), applyLive: () => calls.push('live'), refresh() {}, setOpenFields() {}};
  const runtime = {text: {t: defaultI18n.t}, getSnapshot: () => ({authority: {serverId: 'server', epoch: 'epoch'}}), reportError: (error: unknown) => {throw error;}};
  const {LibraryFilters, useLibraryFrameFilters} = await screenModule(new URL('../src-next/screens/library/LibraryFilters.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/browse': {FilterBuilder: 'FilterBuilder'}, '../../kit/controls': {Button: 'Button'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/focus': {useFocusRegistry: () => ({focus: (address: unknown) => calls.push(['focus', address])})},
    '../../kit/overlays': {BottomSheet: 'BottomSheet', SidePanel: 'SidePanel'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme}, './filter-controls': {libraryFilterChange, libraryFilterField},
    './model': {nativeLibraryCanSave},
  });
  if (frame) {
    const filters = host.render(() => useLibraryFrameFilters({draft, generation: 1, source, runtime, labels: {field: () => 'Genre'}, owner: true, live: frame.live, onSave: (value: unknown) => calls.push(['save', value])}));
    return {tree: undefined, fields: undefined, filters, calls, state, source};
  }
  // The fields are their own component (the frame's Filters draws the same ones): render them in the same pass.
  const {tree, fields} = host.render(() => {
    const tree = LibraryFilters({draft, source, runtime, labels: {field: () => 'Genre'}, owner: true, onClose: () => calls.push('close'), onExited, onSave: (value: unknown) => calls.push(['save', value])});
    const element = nodes(tree).find(node => typeof node.type === 'function' && node.props.panelId);
    return {tree, fields: element && element.type(element.props)};
  });
  return {tree, fields, calls, state, source};
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
  const {tree, fields, calls, state} = await fixture(true);
  assert.equal(tree.type, 'SidePanel'); assert.equal(tree.props.width, 680); assert.equal(tree.props.topInset, 0);
  const builder = nodes(fields).find(node => node.type === 'FilterBuilder');
  tree.props.initialFocusRef.current.focus();
  assert.deepEqual(calls[0], ['focus', {regionId: tree.props.testID, childId: JSON.stringify([JSON.stringify([builder.props.focusPrefix, 'field', 'genre']), 'open'])}]);
  const apply = nodes(tree.props.footer).find(node => node.props.focusId === 'apply');
  assert.equal(apply.props.children[0], 'Show 19 movies'); apply.props.onPress(); assert.deepEqual(calls.slice(1), ['apply', 'close']);
  builder.props.onFieldChange('genre', {kind: 'values', included: ['Drama'], excluded: [], mode: 'any'});
  assert.deepEqual(calls.at(-1), ['predicates', [{variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Drama']}]]);
  state.count.phase = 'loading';
});

test('Library filters over a library that is still loading draw nothing instead of taking the screen down (W3 F2)', async () => {
  for (const tv of [false, true]) {
    const {tree} = await fixture(tv, undefined, true);
    assert.equal(tree, null);
  }
  const exited: string[] = [], {tree} = await fixture(false, 390, false, () => exited.push('exited'));
  tree.props.onExited(); assert.deepEqual(exited, ['exited'], 'the phone sheet tells its host when it has closed, so the host drops the draft');
});

test('On the library frame, Filters hands the frame its fields, count and actions: Done applies on a phone, or closes a tablet pane that applied each change live', async () => {
  for (const live of [false, true]) {
    const {filters, calls, state, source} = await fixture(false, live ? 1180 : 390, false, undefined, {live});
    assert.equal(filters.open, true); assert.equal(filters.count, '19 movies');
    assert.equal(filters.children.type.name, 'LibraryFilterFields'); assert.equal(filters.children.props.key, 1, 'each draft opened starts its fields afresh');
    filters.onDone(); assert.deepEqual(calls.at(-1), live ? 'dispose' : 'apply');
    filters.onClear(); assert.deepEqual(calls.slice(-2), live ? ['clear', 'live'] : ['apply', 'clear']);
    filters.onClose(); assert.equal(calls.at(-1), 'dispose', 'closing disposes the draft; the frame plays the exit');
    assert.equal(filters.save.canSave, true); filters.save.onSave();
    assert.deepEqual(calls.at(-1), ['save', {predicates: state.predicates, sort: source.sort}], 'Save view keeps the staged query');
  }
  const {filters} = await fixture(false, 390, true, undefined, {live: false});
  assert.equal(filters, undefined, 'a library still loading has no Filters yet');
});

test('Phone Library filters put Clear all in the heading and save the actual staged query, not the mounted grid query', async () => {
  const {tree, calls, source, state} = await fixture(false);
  assert.equal(tree.type, 'BottomSheet');
  const clear = tree.props.headerAction; assert.equal(clear.props.focusId, 'clear'); clear.props.onPress(); assert.equal(calls[0], 'clear');
  const save = nodes(tree.props.footer).find(node => node.props.focusId === 'save-view'); assert.equal(save.props.disabled, false); save.props.onPress();
  assert.deepEqual(calls[1], ['save', {predicates: state.predicates, sort: source.sort}]);
  assert.notDeepEqual(state.predicates, source.predicates);
});

for (const [name, operators, expected] of [
  ['between', ['between', 'at-least', 'at-most'], [{variant: 'predicate', field: 'durationMs', operator: 'between', value: [1800000, 3600000]}]],
  ['at-least/at-most fallback', ['at-least', 'at-most'], [
    {variant: 'predicate', field: 'durationMs', operator: 'at-least', value: 1800000},
    {variant: 'predicate', field: 'durationMs', operator: 'at-most', value: 3600000},
  ]],
] as const) {
  test(`Library range preset emits strict tagged predicates through ${name}`, () => {
    const field: BrowseFieldCapability = {id: 'durationMs', labelKey: 'filter.field.duration', type: 'number',
      operators, controlHint: 'number-range', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
    const capabilities: BrowseCapabilities = {
      library: {id: 'movies', name: 'Movies', kind: 'movie', defaultView: 'grid', pinned: false},
      pivots: [], resolvedPivot: null, fields: [field], sorts: [], quickFilters: [],
      queryLimits: {maximumDepth: 8, maximumClauses: 100, maximumBytes: 65536, maximumSorts: 4, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 60000},
    };
    const result = libraryFilterChange(capabilities, field, [], {kind: 'range', value: '30-60'}, [], new Date('2026-10-30T12:00:00Z'));
    assert.deepEqual(result, expected);
    for (const node of result) {
      assert.equal(node.variant, 'predicate');
      assert.deepEqual(parseBrowseNode(node), node);
    }
  });
}

test('a one-value filter (a collection) keeps only the latest pick', () => {
  const field: BrowseFieldCapability = {id: 'collection', labelKey: 'browse.field.collection', type: 'identity-set',
    operators: ['equals'], controlHint: 'facet-multi-select', complexity: 'standard', cost: 'indexed', applicableKinds: []};
  const capabilities: BrowseCapabilities = {
    library: {id: 'movies', name: 'Movies', kind: 'movie', defaultView: 'grid', pinned: false},
    pivots: [], resolvedPivot: null, fields: [field], sorts: [], quickFilters: [],
    queryLimits: {maximumDepth: 8, maximumClauses: 100, maximumBytes: 65536, maximumSorts: 4, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 60000},
  };
  const result = libraryFilterChange(capabilities, field, [], {kind: 'values', included: ['itm_marvel', 'itm_pixar'], excluded: [], mode: 'any'} as never, [], new Date('2026-11-18T12:00:00Z'));
  assert.deepEqual(result.map(node => [(node as {field: string}).field, (node as {operator: string}).operator, (node as {value: unknown}).value]), [['collection', 'equals', 'itm_pixar']]);
});
