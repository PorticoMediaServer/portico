import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeDocument} from '@core/home.ts';
import type {HomeSnapshot} from '@core/home-service.ts';
import {HomeDocumentResources} from '../src/shell/home-resources.ts';

const document = (viewerFence: string): HomeDocument => ({viewerFence} as HomeDocument);
function source(initial?: HomeDocument, synchronous = false) {
  let snapshot: HomeSnapshot = {phase: 'idle', paging: new Set(), document: initial};
  const listeners = new Set<() => void>();
  return {listeners, getSnapshot: () => snapshot,
    subscribe(listener: () => void) {listeners.add(listener);if (synchronous) listener();return () => {listeners.delete(listener);};},
    publish(document?: HomeDocument, phase: HomeSnapshot['phase'] = 'ready') {snapshot = {phase, paging: new Set(), document};for (const listener of listeners) listener();},
  };
}
test('initial Home loading or failure does not invent a fence or construct media resources', () => {
  const home = source();let creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {creates++;return {value: {}, dispose() {}};}, changed() {}, failed: assert.fail});
  home.publish(undefined, 'loading');home.publish(undefined, 'error');
  assert.equal(resources.get(), undefined);assert.equal(creates, 0);
  resources.dispose();assert.equal(home.listeners.size, 0);
});
test('the first Home document creates once; a refresh and a moved viewer fence keep the same resource, and only retirement ends it', () => {
  const home = source(), first = document('actual-first');let creates = 0, disposed = 0, current = true;
  const value = {};
  const resources = new HomeDocumentResources(home, {isCurrent: () => current, create: actual => {assert.equal(actual, first);creates++;return {value, dispose: () => {disposed++;}};}, changed() {}, failed: assert.fail});
  home.publish(first);assert.equal(resources.get(), value);
  home.publish({...first});assert.equal(resources.get(), value);assert.equal(creates, 1);
  // The viewer's access was decided again: the newer Home answer is followed by the resource itself.
  home.publish(document('actual-replacement'));assert.equal(resources.get(), value);assert.equal(disposed, 0);
  home.publish(undefined, 'loading');assert.equal(resources.get(), value);
  home.publish(first);assert.equal(resources.get(), value);assert.equal(creates, 1);assert.equal(disposed, 0);
  current = false;home.publish(first);assert.equal(resources.get(), undefined);assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
  current = true;home.publish(document('after-retirement'));assert.equal(resources.get(), undefined);assert.equal(creates, 1);
  resources.dispose();assert.equal(disposed, 1);
});
test('an immediate subscription callback and an already-loaded document still create a single resource', () => {
  const home = source(document('actual-fence'), true);let creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => ({value: ++creates, dispose() {}}), changed() {}, failed: assert.fail});
  assert.equal(resources.get(), 1);assert.equal(creates, 1);resources.dispose();
});
test('retirement during creation disposes the uncommitted resource', () => {
  const home = source();let current = true, disposed = 0, creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => current, create: () => {
    creates++;current = false;
    return {value: {}, dispose: () => {disposed++;}};
  }, changed() {}, failed: assert.fail});
  home.publish(document('first'));
  assert.equal(creates, 1);assert.equal(resources.get(), undefined);assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
});
test('a moved viewer fence published during creation commits the one resource, which follows that newer document itself', () => {
  const home = source();let disposed = 0, creates = 0;
  const value = {};
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {
    creates++;home.publish(document('replacement'));
    return {value, dispose: () => {disposed++;}};
  }, changed() {}, failed: assert.fail});
  home.publish(document('first'));
  assert.equal(creates, 1);assert.equal(resources.get(), value);assert.equal(disposed, 0);assert.equal(home.listeners.size, 1);
  resources.dispose();assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
});
test('a media setup failure reports once and leaves the actual Home state available to the page', () => {
  const home = source(), error = new Error('actual_media_setup_failure'), failures: unknown[] = [];
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {throw error;}, changed() {}, failed: error => {failures.push(error);}});
  const actual = document('actual-fence');assert.doesNotThrow(() => home.publish(actual));
  assert.equal(home.getSnapshot().document, actual);assert.equal(resources.get(), undefined);assert.deepEqual(failures, [error]);assert.equal(home.listeners.size, 0);
});

// "Home selection closes for good": a moved viewer fence used to close Home's selection until the shell was
// assembled again. The resource now stays, and the controller adopts the newer Home answer the way the shell's
// sync() does (selection.tsx), so the next selection works on the rows that answer offers.
test('Home selection stays usable after a moved viewer fence: the same controller selects from the newer Home answer', async () => {
  const {HomeSelectionController, homeSelectionContext} = await import('@core/home-selection/index.ts');
  const {homeFixtures} = await import('../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts');
  const {withQueries} = await import('../../packages/client-core/test/fixtures/viewer-queries.ts');
  type Controller = InstanceType<typeof HomeSelectionController>;
  const offers = {play: true, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, played: false,
    rating: false, editMetadata: false, refreshMetadata: false, delete: false, download: false, details: true,
    selection: {play: true, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, download: false, edit: false, refresh: false, delete: false},
    state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 0, containerRevision: 0}};
  const homeDocument = (viewerFence: string, ids: readonly string[]): HomeDocument => {
    const row = {id: 'recent', viewerFence, title: homeFixtures.libraryPrefix, kind: 'media', artworkShape: 'poster', endpoint: '', privacySensitivity: '', policyState: '', priority: 0,
      required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true, entries: ids.map(id => ({id, libraryId: 'library', kind: 'movie', title: homeFixtures.movie, homeActions: offers})),
      total: ids.length, start: 0, limit: ids.length, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}};
    return {serverId: 'server', viewerFence, rows: [row], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T00:00:00Z'} as unknown as HomeDocument;
  };
  const scope = {serverId: 'server', authority: 'local', accountId: 'viewer', profileId: 'profile', viewId: 'home'} as const;
  const home = source();let creates = 0, disposed = 0;
  const unexpected = async (): Promise<never> => {throw new Error('unexpected');};
  const resources = new HomeDocumentResources<Controller>(home, {isCurrent: () => true, changed() {}, failed: assert.fail, create: document => {
    creates++;
    const controller = new HomeSelectionController(withQueries({api: {request: unexpected}, home: home as unknown as HomeService, context: homeSelectionContext(scope, document),
      events: {on: () => () => {}}, environment: {platform: 'desktop-web', isOwner: false, canEditMetadata: false}, isCurrent: () => true, requestId: async () => crypto.randomUUID(),
      playback: {play: unexpected, enqueue: unexpected, playChannel: unexpected}, routes: {openEntry: unexpected, openCollection: unexpected, openDestination: unexpected}, reportError: error => {throw error;}}));
    const off = home.subscribe(() => {const next = home.getSnapshot().document;if (next) controller.setContext(homeSelectionContext(scope, next));});
    return {value: controller, dispose: () => {disposed++;off();controller.dispose();}};
  }});
  const select = (document: HomeDocument) => {
    const controller = resources.get();assert.ok(controller, 'Home selection is available');
    const registered = controller.registerWindow({id: 'home:recent', row: document.rows[0]!, sectionOrder: 0});assert.ok(registered.ok);
    controller.selectLoaded();return controller;
  };
  const first = homeDocument('fence-before', ['one', 'two']);home.publish(first);
  const controller = select(first);assert.equal(controller.getSnapshot().selection.selectedKeys.length, 2);
  const moved = homeDocument('fence-after', ['two', 'three', 'four']);home.publish(moved);
  assert.equal(resources.get(), controller, 'a moved fence keeps the one Home selection');
  assert.equal(controller.getSnapshot().context.scope.privacyFence, 'fence-after');
  assert.equal(select(moved), controller);
  assert.equal(controller.getSnapshot().selection.selectedKeys.length, 3, 'the selection follows the rows the newer answer offers');
  assert.equal(creates, 1);assert.equal(disposed, 0);
  resources.dispose();assert.equal(disposed, 1);
});
