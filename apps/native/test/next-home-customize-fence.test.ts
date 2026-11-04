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
import {screenModule} from './helpers/screen-harness.mjs';

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
  const rows = controller.getSnapshot().rows;
  assert.deepEqual(rows.map(row => row.id), ['recent', 'view:pinned', 'view:kept']);
  assert.equal(rows.find(row => row.id === 'view:pinned')?.savedView, undefined, 'the layout view stays, without its stale binding');
  assert.equal(controller.dropUnoffered(), 0, 'nothing more to drop');
  assert.equal(await controller.save(), true, 'Save is not blocked by the view that is no longer current');
  assert.deepEqual(writes, [{order: ['recent', 'view:pinned', 'view:kept'], hidden: [], revision: 7}]);
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
