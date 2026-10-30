import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import {openNativeReadingBookEntry} from '../src-next/screens/read/reading-books-open.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
for (const kind of ['ebook', 'comic'] as const) test(`${kind} entry admits actual available versions and preserves its origin`, async () => {
  const navigation = new NativeShellNavigation(authority, () => authority), origin = navigation.getSnapshot();
  let resident = true, calls = 0;
  const context: any = {current: () => true, session: () => ({sessionFamilyId: 'family', viewer: {}}), authority: () => authority, navigation,
    api: {reading: () => ({versions: async (id: string) => {assert.equal(id, 'item'); calls++; return {chosenSourceId: 'actual-source', versions: [{sourceId: 'actual-source', format: kind === 'ebook' ? 'epub' : 'cbz', inventoryAvailable: true}]};}, dispose() {}})}};
  const entry = {id: 'item', libraryId: 'library', kind, title: 'Actual'};
  const opened = await openNativeReadingBookEntry(context, {libraryId: 'library', entry, origin: {route: origin.route, revision: origin.revision}, resident: () => resident});
  assert.equal(opened, true); assert.equal(calls, 2);
  const route = navigation.getSnapshot().route; assert.equal(route.kind, 'read-book');
  if (route.kind === 'read-book') assert.equal(route.item.sourceId, 'actual-source');
  navigation.back(authority); assert.equal(navigation.getSnapshot().route, origin.route);
  resident = false;
  assert.equal(await openNativeReadingBookEntry(context, {libraryId: 'library', entry, origin: {route: origin.route, revision: origin.revision}, resident: () => resident}), false);
  navigation.dispose();
});

test('a late versions response cannot navigate after the origin is replaced', async () => {
  const navigation = new NativeShellNavigation(authority, () => authority), origin = navigation.getSnapshot();
  let resolve!: (value: any) => void;
  const versions = new Promise<any>(done => {resolve = done;});
  const context: any = {current: () => true, session: () => ({sessionFamilyId: 'family', viewer: {}}), authority: () => authority, navigation,
    api: {reading: () => ({versions: () => versions, dispose() {}})}};
  const opening = openNativeReadingBookEntry(context, {libraryId: 'library', entry: {id: 'item', libraryId: 'library', kind: 'ebook', title: 'Actual'},
    origin: {route: origin.route, revision: origin.revision}, resident: () => navigation.getSnapshot() === origin});
  navigation.open({kind: 'notifications'}, authority);
  resolve({chosenSourceId: 'actual-source', versions: [{sourceId: 'actual-source', format: 'epub', inventoryAvailable: true}]});
  assert.equal(await opening, false); assert.equal(navigation.getSnapshot().route.kind, 'notifications');
  navigation.dispose();
});

test('failed and unavailable versions never open a Reading successor', async () => {
  for (const failure of [false, true]) {
    const navigation = new NativeShellNavigation(authority, () => authority), origin = navigation.getSnapshot();
    const context: any = {current: () => true, session: () => ({sessionFamilyId: 'family', viewer: {}}), authority: () => authority, navigation,
      api: {reading: () => ({versions: async () => {if (failure) throw new Error('offline'); return {versions: []};}, dispose() {}})}};
    const call = openNativeReadingBookEntry(context, {libraryId: 'library', entry: {id: 'item', libraryId: 'library', kind: 'ebook', title: 'Actual'},
      origin: {route: origin.route, revision: origin.revision}, resident: () => true});
    if (failure) await assert.rejects(call); else assert.equal(await call, false);
    assert.equal(navigation.getSnapshot(), origin); navigation.dispose();
  }
});
