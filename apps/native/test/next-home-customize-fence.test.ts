import assert from 'node:assert/strict';
import test from 'node:test';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved';
import type {HomeLayoutView} from '../../../packages/client-core/src/home';
import {NativeHomeCustomizeController} from '../src-next/screens/home/customize-controller';
import * as react from 'react';
import * as reactNative from 'react-native';
import {defaultI18n} from '../../../packages/i18n/src';
import * as personalSaved from '../../../packages/client-core/src/personal-saved';
import * as shellModel from '../src-next/shell/model';
import * as lifetime from '../src-next/screens/destinations/lifetime';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// The route module, with its React views stubbed and its controller, lifetime and authority checks real.
const loadRoute = () => screenModule(new URL('../src-next/screens/home/CustomizeRoute.tsx', import.meta.url), {
  react, 'react-native': reactNative, '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/personal-saved': personalSaved,
  '../../kit/states': {StateView: 'StateView'}, './HomeCustomize': {HomeCustomize: 'HomeCustomize'}, './customize-controller': {NativeHomeCustomizeController},
  './saved-view-summaries': {NativeHomeSavedViewSummaries: class {subscribe() {return () => {};} getSnapshot() {return undefined;} dispose() {} load() {} line() {return '';}}},
  './customize-intent': {takeHomeCustomizeView: () => undefined}, '../../shell/model': shellModel, '../destinations/lifetime': lifetime,
  '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
});

// Step 7 item 4, ruling C (lead, 5 Nov): a moved viewer fence (the viewer's access decided again) no longer closes
// Customize Home. Its layout writes pin the revision the server checks. Views added in this draft that are no longer
// offered drop out with a notice; views already in the server's layout stay and lose a stale binding; a Save or Reset
// that captured the old rows and was never accepted is dropped, so Save works again.
const layout = (rows: HomeLayoutView['rows'] = [], revision = 7): HomeLayoutView => ({revision, rowOrder: ['recent', ...rows.map(row => row.id)], hiddenRowIds: [], rows: [
  {id: 'recent', title: 'Recently added', kind: 'recent', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false}, ...rows]});
const view = (id: string, libraryId: string): PersonalResource => ({serverId: 'server', viewerFence: 'saved-fence', id, kind: 'view', name: 'View ' + id, summary: '',
  visibility: 'private', revision: 1, role: 'owner', entryCount: 1, actions: ['pin'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 1, shares: [],
  definition: {libraryId, pivot: 'movies', query: null, sort: [], presentation: 'grid'}} as PersonalResource);

test('C: a view added in the draft whose library is no longer visible drops with a count; a layout view stays, unbound, and Save sends the rest', async () => {
  const pinned = view('pinned', 'gone'), added = view('added', 'gone'), kept = view('kept', 'library');
  let visible = new Set(['library', 'gone']);const writes: unknown[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request',
    savedViews: {serverId: 'server', isCurrent: candidate => visible.has(candidate.definition!.libraryId)}, home: {
      loadLayout: async () => layout([{id: 'view:pinned', title: 'View pinned', kind: 'saved_view', libraryId: 'gone', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false}]),
      resetLayout: async () => {}, saveLayout: async (order, hidden, _id, revision) => {writes.push({order, hidden, revision});return layout();}}});
  await controller.load();
  controller.bindSavedViews([pinned]);
  assert.equal(controller.getSnapshot().rows.find(row => row.id === 'view:pinned')?.savedView, pinned);
  assert.equal(controller.addSavedView(added), true);assert.equal(controller.addSavedView(kept), true);
  visible = new Set(['library']);
  assert.equal(controller.dropUnoffered(), 1, 'only the added view the viewer can no longer see drops');
  assert.equal(controller.getSnapshot().noLongerAvailable, 1, 'the notice counts it');
  const rows = controller.getSnapshot().rows;
  assert.deepEqual(rows.map(row => row.id), ['recent', 'view:pinned', 'view:kept']);
  assert.equal(rows.find(row => row.id === 'view:pinned')?.savedView, undefined, 'the layout view stays, without its stale binding');
  assert.equal(controller.dropUnoffered(), 0, 'nothing more to drop');
  assert.equal(await controller.save(), true, 'Save is not blocked by the view that is no longer current');
  assert.deepEqual(writes, [{order: ['recent', 'view:pinned', 'view:kept'], hidden: [], revision: 7}]);
  assert.equal(controller.getSnapshot().noLongerAvailable, undefined, 'a successful Save clears the notice');
  controller.dispose();
});

test('C: a Save refused because a view stopped being current is dropped by the newer answer, so Save works again', async () => {
  const added = view('added', 'gone');let visible = true, writes = 0;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request',
    savedViews: {serverId: 'server', isCurrent: () => visible}, home: {
      loadLayout: async () => layout(), resetLayout: async () => {}, saveLayout: async () => {writes++;return layout();}}});
  await controller.load();
  assert.equal(controller.addSavedView(added), true);controller.toggle('recent');
  visible = false;
  assert.equal(await controller.save(), false);assert.equal(controller.getSnapshot().retry, 'save', 'before: only Try again is offered');
  assert.equal(await controller.save(), false, 'and Try again re-fails on the captured rows');
  assert.equal(controller.dropUnoffered(), 1);
  assert.equal(controller.getSnapshot().retry, undefined);assert.equal(controller.getSnapshot().error, undefined);
  assert.equal(await controller.save(), true);assert.equal(writes, 1);
  controller.dispose();
});

test('C: a moved viewer fence and a smaller library list keep Customize Home open', async () => {
  const {createNativeHomeCustomization} = await loadRoute();
  const route = {kind: 'home-customize'} as never, authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'a'.repeat(64), privacyFence: 'privacy'};
  let document = {serverId: 'server', viewerFence: 'a'.repeat(64)}, libraries = [{id: 'library'}, {id: 'gone'}], backs = 0;
  const homeListeners = new Set<() => void>();
  const context = {current: () => true, authority: () => authority, subscribeAuthority: () => () => {},
    session: () => ({sessionFamilyId: 'family', viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'}}),
    navigation: {getSnapshot: () => ({route}), subscribe: () => () => {}, back: () => {backs++;}},
    home: {getSnapshot: () => ({document}), subscribe: (listener: () => void) => {homeListeners.add(listener);return () => homeListeners.delete(listener);},
      loadLayout: async () => layout(), saveLayout: async () => layout(), resetLayout: async () => {}},
    libraries: () => libraries, api: {request: async () => {throw new Error('no request expected');}}} as never;
  const screen = createNativeHomeCustomization(context);
  try {
    screen.reconcile();assert.equal(screen.owns(route), true);
    document = {serverId: 'server', viewerFence: 'b'.repeat(64)};libraries = [{id: 'library'}];
    screen.reconcile();
    assert.equal(backs, 0, 'Customize does not close');assert.equal(screen.owns(route), true);
  } finally {screen.dispose();}
});

// Review (#751): the access change lands while a Save is in flight. The drop is owed until that command settles; when
// the server refuses the Save, the added view drops, the notice counts it, and Save works again.
test('C: an access change during an in-flight Save that the server refuses drops the view once the Save settles', async () => {
  const added = view('added', 'gone');let visible = true, attempts = 0;
  const refusal = {resolve: (_: unknown) => {}};
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request',
    savedViews: {serverId: 'server', isCurrent: () => visible}, home: {
      loadLayout: async () => layout(), resetLayout: async () => {},
      saveLayout: async () => {attempts++;if (attempts === 1) {await new Promise(resolve => {refusal.resolve = resolve;});throw {status: 403, code: 'forbidden'};}return layout();}}});
  await controller.load();
  assert.equal(controller.addSavedView(added), true);
  const saving = controller.save();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(controller.getSnapshot().pending, true);
  visible = false;
  assert.equal(controller.dropUnoffered(), 0, 'nothing drops under an in-flight Save');
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['recent', 'view:added']);
  refusal.resolve(undefined);
  assert.equal(await saving, false);
  const settled = controller.getSnapshot();
  assert.deepEqual(settled.rows.map(row => row.id), ['recent'], 'the owed drop ran when the Save settled');
  assert.equal(settled.noLongerAvailable, 1);assert.equal(settled.retry, undefined);assert.equal(settled.pending, false);
  controller.toggle('recent');
  assert.equal(await controller.save(), true, 'Save works again');assert.equal(attempts, 2);
  controller.dispose();
});

test('C: a Saved intent that could not be added never hides the views-dropped notice', async () => {
  const {customizeNotice} = await loadRoute();
  assert.equal(customizeNotice(undefined, undefined), undefined);
  assert.equal(customizeNotice('Unavailable.', undefined), 'Unavailable.');
  assert.equal(customizeNotice(undefined, '1 saved view is no longer available.'), '1 saved view is no longer available.');
  assert.equal(customizeNotice('Unavailable.', '1 saved view is no longer available.'), 'Unavailable. 1 saved view is no longer available.');
  assert.equal(defaultI18n.t('next.home.viewsNoLongerAvailable', {count: 2}), '2 saved views are no longer available.');
});

// TV: the dropped view's row unmounts while it holds focus. Focus goes to the row now in its place (here the row
// above) inside the side panel, never out of it; focus already elsewhere in the panel stays where it is.
test('C: on TV, focus on a dropped view moves to its neighbour inside the side panel', async () => {
  const owner = hooks(), effects: (() => void | (() => void))[] = [], focused: unknown[] = [];
  // Effects run after each render when their dependencies change, as React runs them.
  const react: any = {...owner.react, useLayoutEffect: () => {}, useId: () => 'customize',
    useEffect: (effect: () => void | (() => void), deps?: readonly unknown[]) => {
      const held: {current: readonly unknown[] | undefined} = owner.react.useRef(undefined);
      if (!held.current || !deps || deps.some((value, index) => value !== held.current![index])) {held.current = deps;effects.push(effect);}
    }};
  react.default = react;
  const frames: (() => void)[] = [];
  const raf = globalThis.requestAnimationFrame, caf = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = ((callback: () => void) => {frames.push(callback);return frames.length;}) as never;
  globalThis.cancelAnimationFrame = (() => {}) as never;
  let focus: {regionId: string; childId: string} | undefined;
  const registry = {capture: () => focus, attachedFocus: () => undefined};
  const theme = {isTV: true, platform: 'tv', colors: {text: '#fff', textSecondary: '#aaa', textTertiary: '#777', raisedHigh: '#222', accentStrong: '#0af', shadow: '#000', line: '#333'},
    textScale: 1, type: {title: {}, body: {}, bodyStrong: {}, caption: {}, captionStrong: {}}};
  const {HomeCustomize} = await screenModule(new URL('../src-next/screens/home/HomeCustomize.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', TVEventHandler: {addListener: () => ({remove() {}})}, findNodeHandle: () => 1},
    'react-native-gesture-handler': {Gesture: {Native: () => ({})}, GestureDetector: 'GestureDetector'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_i18n: unknown, text: unknown, fallback: string) => text ?? fallback},
    '../../../../../packages/client-core/src/presentation/home-row-reason': {homeRowReasonParts: () => undefined},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'}, '../../kit/focus': {useFocusRegistry: () => registry},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/browse/presentation': {alpha: (value: string) => value},
    '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', SidePanel: 'SidePanel'}, '../../kit/overlays/OverlayHost': {focusTarget: (node: unknown) => {focused.push(node);}},
    '../../kit/theme': {nativeType: () => ({}), useTheme: () => theme}, './customize-controller': {}, './customize-drag': {},
    '../../platform/tv-key-source': {tvKeySource: 'native'}, '../../platform/tv-key-adapter': {createTVKeyAdapter: () => ({read: () => undefined})},
  });
  const recent = {id: 'recent', title: 'Recently added', kind: 'recent', reorderable: true, hideable: true, required: false, hidden: false};
  const added = {id: 'view:added', title: 'View added', kind: 'saved_view', savedView: view('added', 'gone'), reorderable: true, hideable: true, required: false, hidden: false};
  let state: any = {phase: 'ready', pending: false, dirty: true, conflict: false, showConflict: false, rows: [recent, added]};
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, isCurrent: () => true};
  const props = {controller, copy: {save: 'Save', addView: 'Add view', title: 'Customize'}, i18n: {t: (key: string) => key}, onClose() {}, onAddView() {}, summary: () => '', testID: 'customize'};
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children), ...all(node.props.footer)] : [];
  const draw = () => {effects.length = 0;const tree = owner.render(() => HomeCustomize(props));
    for (const node of all(tree)) if (typeof node.props.ref === 'function') node.props.ref({id: node.props.focusId});
    const add = all(tree).find(node => node.props.focusId === 'add-view');if (add) add.props.ref.current = {id: 'add-view'};
    for (const effect of effects) effect();return tree;};
  try {
    assert.equal(all(draw()).some(node => node.type === 'SidePanel'), true);
    frames.length = 0;focused.length = 0;
    focus = {regionId: 'customize', childId: 'view:added'};
    state = {...state, rows: [recent], noLongerAvailable: 1};
    const after = draw();
    for (const frame of frames.splice(0)) frame();
    assert.deepEqual(focused, [{id: 'recent'}], 'focus moves to the row above, inside the panel');
    assert.equal(all(after).some(node => node.type === 'SidePanel' && node.props.open === true), true, 'the panel stays open');
    focus = {regionId: 'customize', childId: 'recent:visibility'};focused.length = 0;
    state = {...state, rows: [recent, added]};draw();for (const frame of frames.splice(0)) frame();
    state = {...state, rows: [recent]};draw();for (const frame of frames.splice(0)) frame();
    assert.deepEqual(focused, [], 'focus already elsewhere in the panel stays');
  } finally {globalThis.requestAnimationFrame = raf;globalThis.cancelAnimationFrame = caf;}
});
