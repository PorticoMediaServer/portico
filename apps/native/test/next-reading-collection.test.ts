import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';

async function fixture() {
  let revision = 1, role = 'member', current = true;
  const route = {}, authority = {serverId: 's', accountId: 'a', profileId: 'p'}, opened: any[] = [], navigated: any[] = [];
  const context = {current: () => current, session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local', role}}), authority: () => authority,
    libraries: () => [{id: 'books', kind: 'book'}], subscribeAuthority: () => () => {}, navigation: {getSnapshot: () => ({route, revision}), subscribe: () => () => {}, open: (target: any) => {navigated.push(target); return true;}}, api: {}};
  class Collection {
    disposed = false; constructor(readonly options: any, readonly items: any[]) {opened.push(this);}
    getSnapshot = () => ({destination: {id: 'collection'}});
    subscribe = () => () => {}; load = async () => {}; dispose = () => {this.disposed = true;};
  }
  const {NativeReadingActions} = await screenModule(new URL('../src-next/screens/read/reading-actions.ts', import.meta.url), {
    react: {default: {}}, '../../shell/model': {sameAuthority: (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b)},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(16)}},
    '../../../../../packages/client-core/src/reading-collection': {ReadingCollection: Collection},
    '../destinations/bulk-media': {NativeBulkMedia: class {}},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 's', viewerId: 'member'}, current: () => current})},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'request'})},
  });
  const actions = new NativeReadingActions(context, (error: unknown) => {throw error;}); actions.arm();
  const offer = (id: string) => {const life = new AbortController(); return {item: {id, libraryId: 'books', title: id}, lifetime: {signal: life.signal, isCurrent: () => !life.signal.aborted}, open() {}, retire: () => life.abort()};};
  return {actions, opened, navigated, offer, route: () => {revision++; actions.reconcile();}, authority: () => {role = 'owner'; actions.reconcile();}, retire: () => {current = false; actions.reconcile();}};
}

test('member Select keeps its actions and Collection captures exactly the original Reading selection', async () => {
  const f = await fixture(), first = f.offer('one'), second = f.offer('two'); try {
    f.actions.start(); f.actions.toggle(first); f.actions.toggle(second); const rendered = f.actions.getSnapshot();
    assert.equal(f.actions.canCollect(rendered), true); assert.equal(f.actions.canEdit(rendered), false);
    f.actions.openCollection(rendered); const opened = f.opened[0];
    assert.deepEqual(opened.items, [{id: 'one', libraryId: 'books', title: 'one', kind: 'ebook'}, {id: 'two', libraryId: 'books', title: 'two', kind: 'ebook'}]);
    // A member's personal collections are their own, exactly as the Home picker already allows.
    assert.equal(opened.options.canCreate, true); assert.equal(opened.options.isCurrent(), true);
    f.actions.toggle(first); f.actions.cancel(); assert.equal(f.actions.getSnapshot().selected.length, 2);
    first.retire(); f.actions.reconcile(); assert.equal(opened.disposed, true); assert.equal(opened.options.isCurrent(), false);
    assert.equal(f.actions.getSnapshot().collection, undefined); assert.equal(f.actions.getSnapshot().selected.length, 1);
  } finally {f.actions.dispose();}
});

test('stale selection presses, route replacement and authority changes retire Collection', async () => {
  for (const boundary of ['route', 'authority', 'selection']) {
    const f = await fixture(); try {
      f.actions.toggle(f.offer('one')); const old = f.actions.getSnapshot();
      f.actions.toggle(f.offer('two')); f.actions.openCollection(old); assert.equal(f.opened.length, 0);
      f.actions.openCollection(f.actions.getSnapshot()); const opened = f.opened[0];
      if (boundary === 'route') f.route(); else if (boundary === 'authority') f.authority(); else f.retire();
      assert.equal(opened.disposed, true); assert.equal(opened.options.isCurrent(), false);
    } finally {f.actions.dispose();}
  }
});

test('native picker executes the rendered choice and presents the real partial receipt', async () => {
  const hook = hooks(), chosen: any[] = [], retried: any[] = [];
  const state = {phase: 'result', rows: [{id: 'c', name: 'Shelf', count: 10, canAdd: true, artwork: [], alreadyIn: 1}], query: '', next: false, previous: false,
    destination: {id: 'c', name: 'Shelf', entryCount: 11}, receipt: {added: 1, alreadyPresent: 0, failed: 1, unknown: 0, complete: true, pending: false, failures: [{itemId: 'two', code: 'not_found'}]}};
  const controller = {items: [{id: 'one', title: 'First'}, {id: 'two', title: 'Second'}], getSnapshot: () => state, subscribe: () => () => {},
    choose: async (...args: any[]) => {chosen.push(args);}, retry: async () => {retried.push(true);}};
  const {ReadingCollectionPicker} = await screenModule(new URL('../src-next/screens/read/ReadingCollectionPicker.tsx', import.meta.url), {
    react: hook.react, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Unavailable'})},
    '../../kit/selection': {CollectionPicker: 'Picker'}, '../../kit/artwork': {CoverMosaic: 'Mosaic'},
  });
  const view = hook.render(() => ReadingCollectionPicker({controller, runtime: {artwork: () => undefined, reportError: (error: unknown) => {throw error;}}, onClose() {}}));
  assert.equal(view.type, 'Picker'); assert.equal(view.props.result.inserted, 1); assert.equal(view.props.result.failed, 1);
  assert.deepEqual(view.props.result.failures, [{id: 'two', title: 'Second', reason: 'Unavailable'}]);
  view.props.onAdd('c'); await Promise.resolve(); assert.deepEqual(chosen, [[state, 'c']]);
  view.props.onRetry(['two']); await Promise.resolve(); assert.equal(retried.length, 1);
});


test('the confirmed collection View action opens the real Saved resource route', async () => {
  const f = await fixture(); try {
    f.actions.toggle(f.offer('one')); f.actions.openCollection(f.actions.getSnapshot());
    const opening = f.actions.getSnapshot().collection;
    f.actions.viewCollection(opening);
    assert.deepEqual(f.navigated, [{kind: 'saved-view', view: {view: 'resource', resourceId: 'collection'}}]);
    assert.equal(f.opened[0].disposed, true); assert.equal(f.actions.getSnapshot().collection, undefined);
    f.actions.viewCollection(opening); assert.equal(f.navigated.length, 1);
  } finally {f.actions.dispose();}
});
