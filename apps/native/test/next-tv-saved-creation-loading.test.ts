import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {isSlowLoading, showSkeleton, skeletonTiming} from '../src-next/kit/states/model.ts';
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const theme = {isTV: true, gutter: 200, layout: {gutter: {tvRight: 96}}, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', line: '#333', raised: '#222'}};
async function directory(view = 'collections') {
  const h = hooks(), react: any = {...h.react, useLayoutEffect() {}, useImperativeHandle() {}}; react.default = react;
  const focused: any[] = [], mutations: any[] = []; let retries = 0, fail = false, held: Promise<void> | undefined, arrived = true;
  let active = {visible: true, revision: 1, intent: 'enter'};
  let state: any = {phase: 'ready', personal: {resources: [], nextCursor: '', retryPending: false},
    saved: {projection: {actions: ['create_playlist'], sections: [{entries: [], totalCount: 0}], sorts: [], filters: [], query: {}}, pagination: {next: []}}};
  let original: any;
  const created = () => {state = {...state, personal: {...state.personal, retryPending: false, result: {resourceId: 'actual-id'}, resources: arrived ? [{id: 'actual-id', name: original.name}] : []},
    saved: {...state.saved, lastReceipt: {playlistId: 'actual-id'}, projection: {...state.saved.projection, sections: [{entries: arrived ? [{kind: 'playlist', id: 'actual-id', name: original.name}] : []}]}}};};
  const destination = {subscribe: () => () => {}, getSnapshot: () => state,
    mutatePersonal: async (input: any) => {mutations.push(input); original = input; await held;
      if (fail) {state = {...state, personal: {...state.personal, retryPending: true, mutationError: {code: 'transport'}}}; return false;} created(); return true;},
    retryPersonalMutation: async () => {retries++; created(); return true;},
    mutateSaved: async (input: any) => {mutations.push(input); original = input; await held; created();}};
  const {SavedTVDirectory} = await screenModule(new URL('../src-next/screens/saved/SavedTVDirectory.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Save failed'})}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/artwork': {AmbientArt: 'AmbientArt', CoverMosaic: 'CoverMosaic', MediaArt: 'MediaArt'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame'}, '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => ({focus: (address: any) => focused.push(address)})},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme},
    './SavedForms': {SavedNameSheet: 'SavedNameSheet'}, './SavedTVLoading': {SavedTVLoading: 'SavedTVLoading'}, './PlaylistToolbar': {PlaylistToolbar: 'PlaylistToolbar'},
    './presentation': Object.fromEntries(['savedCollectionCount', 'savedItemCount', 'savedLibraryName', 'savedTitleCount', 'savedViewCount', 'savedVisibility'].map(key => [key, String])),
  });
  const props: any = {view, destination, runtime: {}, libraries: [], enabled: true, activation: {getSnapshot: () => active, subscribe: () => () => {}}, regionId: 'directory', select() {}, openPlaylist() {}, openResource() {}};
  const render = () => nodes(h.render(() => SavedTVDirectory(props)));
  const form = () => render().find(node => node.type === 'SavedNameSheet');
  const open = () => render().find(node => node.type === 'CardFrame' && node.props.testID === `saved-tv-new-${view}`).props.onNavigate();
  return {render, form, open, mutations, focused, retries: () => retries, fail: (value: boolean) => {fail = value;}, arrive: (value: boolean) => {arrived = value;}, hold: (value: Promise<void>) => {held = value;},
    activate: (value: typeof active) => {active = value;}, revoke: () => {state = {...state, saved: {...state.saved, projection: {...state.saved.projection, actions: []}}};}};
}

test('empty TV collections offer the leading create tile and uncertain creation retries its original command', async () => {
  const f = await directory();
  assert.equal(f.render().find(node => node.props.id === 'directory.cards').props.grid.count, 1);
  f.open(); let form = f.form(); assert.equal(form.props.open, true);
  assert.equal(f.render().find(node => node.props.id === 'directory.cards').props.enabled, false);
  f.fail(true); const draft = {name: 'Created', summary: 'Real summary', visibility: 'private'};
  form.props.onSave(draft); form.props.onSave(draft); await tick(); assert.equal(f.mutations.length, 1);
  form = f.form(); assert.equal(form.props.locked, true); assert.equal(form.props.action, 'action.tryAgain');
  form.props.onSave(draft); await tick(); assert.equal(f.retries(), 1); assert.equal(f.mutations.length, 1);
  assert.deepEqual(f.mutations[0], {action: 'create', kind: 'collection', ...draft});
  form = f.form(); assert.equal(form.props.open, false); form.props.returnFocusRef.current.requestTVFocus();
  assert.deepEqual(f.focused, [{regionId: 'directory.cards', childId: 'saved-tv-collections-actual-id'}]);
});

test('creation uses the returned sorted page, refuses retired sheet callbacks and rechecks playlist permission', async () => {
  const f = await directory('playlists'); f.open(); const old = f.form(); old.props.onClose(); f.open();
  old.props.onSave({name: 'stale', summary: '', visibility: 'private'}); await tick(); assert.equal(f.mutations.length, 0);
  f.revoke(); f.form().props.onSave({name: 'revoked', summary: '', visibility: 'private'}); await tick(); assert.equal(f.mutations.length, 0);
  const collection = await directory(); collection.arrive(false); collection.open();
  collection.form().props.onSave({name: 'Not on this sorted page', summary: '', visibility: 'private'}); await tick();
  collection.form().props.returnFocusRef.current.requestTVFocus();
  assert.deepEqual(collection.focused, [{regionId: 'directory.cards', childId: 'saved-tv-new-collections'}]);
});

test('a create completion cannot restore focus after its actual activation leaves and returns', async () => {
  const f = await directory(); let release!: () => void;
  f.hold(new Promise<void>(resolve => {release = resolve;})); f.open(); const original = f.form();
  original.props.onSave({name: 'Old opening', summary: '', visibility: 'private'}); await tick();
  f.activate({visible: true, revision: 3, intent: 'restore'}); f.render(); release(); await tick();
  original.props.returnFocusRef.current.requestTVFocus(); assert.deepEqual(f.focused, []);
});

test('Saved loading uses 300ms geometry and an 8s polite status, with cleanup and no fabricated error', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const h = hooks(), effects: (() => (() => void))[] = [], cleanups: (() => void)[] = [], react: any = {...h.react}; react.default = react;
  react.useEffect = (effect: () => (() => void), deps: any[]) => {const before = h.react.useRef(undefined); if (!before.current) {before.current = deps; effects.push(effect);}};
  const {SavedTVLoading} = await screenModule(new URL('../src-next/screens/saved/SavedTVLoading.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/states': {GridSkeleton: 'GridSkeleton', SkeletonBlock: 'SkeletonBlock', isSlowLoading, showSkeleton, skeletonTiming},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme},
  });
  let shape = 'views'; const render = () => {const tree = nodes(h.render(() => SavedTVLoading({shape}))); effects.splice(0).forEach(effect => cleanups.push(effect())); return tree;};
  assert.equal(render().some(node => node.type === 'SkeletonBlock'), false);
  t.mock.timers.tick(299); assert.equal(render().some(node => node.type === 'SkeletonBlock'), false);
  t.mock.timers.tick(1); let tree = render();
  assert.deepEqual(tree.filter(node => node.type === 'SkeletonBlock').map(node => [node.props.width, node.props.height]), [[520, 300], [520, 300], [520, 300]]);
  shape = 'squares'; tree = render(); const grid = tree.find(node => node.type === 'GridSkeleton');
  assert.equal(grid.props.elapsedMs, 300); assert.equal(grid.props.cardWidth, 236); assert.equal(grid.props.gap, 34);
  t.mock.timers.tick(7700); tree = render(); assert.ok(tree.some(node => node.type === 'Text' && node.props.accessibilityLiveRegion === 'polite'));
  assert.equal(tree.some(node => node.type === 'StateView'), false); cleanups.forEach(cleanup => cleanup());
});
