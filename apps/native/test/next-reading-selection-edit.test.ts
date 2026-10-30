import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// MOB-055 (P2-42): an owner's Reading selection opens the shared bulk editor over exactly
// the selected publications of one kind; members never get Edit details.
async function fixture(role: 'owner' | 'member') {
  const route = {kind: 'shell', destination: {kind: 'read'}}, authority = {serverId: 'server', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'f'};
  const listeners = new Set<() => void>(), errors: unknown[] = [], opened: any[] = [];
  const libraries = [{id: 'books', kind: 'book'}, {id: 'comics', kind: 'comic'}, {id: 'films', kind: 'movie'}];
  const context = {session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local', role}}),
    authority: () => authority, current: () => true, libraries: () => libraries,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    navigation: {getSnapshot: () => ({route, revision: 1}), subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    api: {reading: () => ({dispose() {}})},
  };
  class FakeBulk {
    disposed = false; listeners = new Set<() => void>();
    constructor(readonly options: any, readonly entries: any[]) {opened.push(this);}
    subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
    getSnapshot = () => ({phase: 'loading'});
    openEditor = async () => {};
    dispose = () => {this.disposed = true;};
  }
  const {NativeReadingActions} = await screenModule(new URL('../src-next/screens/read/reading-actions.ts', import.meta.url), {
    '../../../../../packages/client-core/src/reading-collection': {ReadingCollection: class {constructor() {throw new Error('unexpected collection opening');}}},
    react: {default: {}}, '../../shell/model': {sameAuthority: (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b)},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(16)}},
    '../destinations/bulk-media': {NativeBulkMedia: FakeBulk},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'request'})},
  });
  const actions = new NativeReadingActions(context, (error: unknown) => errors.push(error));
  assert.equal(actions.arm(), true);
  const offer = (id: string, libraryId: string) => {
    const life = new AbortController(); let current = true;
    return {offer: {item: {id, libraryId, title: id.toUpperCase()}, lifetime: {signal: life.signal, isCurrent: () => current}, open() {}},
      retire: () => {current = false; life.abort();}};
  };
  return {actions, offer, opened, errors, listeners};
}

test('members never get Edit details', async () => {
  const f = await fixture('member'), one = f.offer('one', 'books');
  try {
    f.actions.start(); f.actions.toggle(one.offer);
    assert.equal(f.actions.owner(), false); assert.equal(f.actions.canEdit(f.actions.getSnapshot()), false);
    f.actions.openEditor(f.actions.getSnapshot()); assert.equal(f.opened.length, 0);
  } finally {f.actions.dispose();}
});

test('an owner edits exactly the selected books, as ebooks, and one kind at a time', async () => {
  const f = await fixture('owner'), one = f.offer('one', 'books'), two = f.offer('two', 'books'), comic = f.offer('nemo', 'comics');
  try {
    f.actions.start();
    assert.equal(f.actions.canEdit(f.actions.getSnapshot()), false, 'nothing selected');
    f.actions.toggle(one.offer); f.actions.toggle(two.offer);
    assert.equal(f.actions.owner(), true); assert.equal(f.actions.canEdit(f.actions.getSnapshot()), true);
    f.actions.toggle(comic.offer);
    assert.equal(f.actions.canEdit(f.actions.getSnapshot()), false, 'books and comics are not edited together');
    f.actions.toggle(comic.offer);
    const rendered = f.actions.getSnapshot();
    f.actions.openEditor(rendered);
    assert.equal(f.opened.length, 1);
    assert.deepEqual(f.opened[0].entries, [{id: 'one', libraryId: 'books', kind: 'ebook', title: 'ONE'}, {id: 'two', libraryId: 'books', kind: 'ebook', title: 'TWO'}]);
    assert.equal(f.actions.getSnapshot().edit, f.opened[0]);
    assert.equal(f.opened[0].options.isCurrent(), true);
    // While the editor is open the selection is fixed and nothing else runs.
    f.actions.toggle(comic.offer); assert.equal(f.actions.getSnapshot().selected.length, 2);
    f.actions.cancel(); assert.equal(f.actions.getSnapshot().selecting, true);
    f.actions.openEditor(f.actions.getSnapshot()); assert.equal(f.opened.length, 1, 'one editor at a time');
    f.actions.closeEditor(f.opened[0]);
    assert.equal(f.opened[0].disposed, true); assert.equal(f.actions.getSnapshot().edit, undefined);
    assert.equal(f.opened[0].options.isCurrent(), false, 'a closed editor is no longer current');
    assert.deepEqual(f.errors, []);
  } finally {f.actions.dispose();}
});

test('a comic selection edits as comics, and a retired row closes the editor', async () => {
  const f = await fixture('owner'), one = f.offer('nemo-1', 'comics'), two = f.offer('nemo-2', 'comics');
  try {
    f.actions.start(); f.actions.toggle(one.offer); f.actions.toggle(two.offer);
    f.actions.openEditor(f.actions.getSnapshot());
    assert.deepEqual(f.opened[0].entries.map((entry: any) => entry.kind), ['comic', 'comic']);
    two.retire(); f.actions.reconcile();
    assert.equal(f.opened[0].disposed, true); assert.equal(f.actions.getSnapshot().edit, undefined);
    assert.equal(f.actions.getSnapshot().selected.length, 1);
  } finally {f.actions.dispose();}
});

test('a selection outside a Reading library offers no edit', async () => {
  const f = await fixture('owner'), film = f.offer('film', 'films');
  try {
    f.actions.start(); f.actions.toggle(film.offer);
    assert.equal(f.actions.canEdit(f.actions.getSnapshot()), false);
  } finally {f.actions.dispose();}
});
