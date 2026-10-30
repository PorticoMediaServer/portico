import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';

function events() {
  const listeners = new Set<() => void>();
  return {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    emit: () => {for (const listener of [...listeners]) listener();}, listeners};
}

async function fixture() {
  const route = events(), membership = events(), authorityEvents = events(), menus = events();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-only'};
  const entry = {id: 'actual', libraryId: 'movies', kind: 'movie', title: 'Actual', playback: {itemId: 'actual', kind: 'movie'}};
  let live = true, member = entry, navigationRevision = 1, visible = true, menuReady = false, activeEntry: unknown;
  let snapshot = {revision: navigationRevision, route: {kind: 'shell'}};
  const reads: any[] = [];
  let libraries = [{id: 'movies', kind: 'book'}];
  const starts: any[] = [], opened: any[] = [], navigated: any[] = [], errors: unknown[] = [], finish: (() => void)[] = [];
  const port = {subscribe: menus.subscribe, isOpen: (value: unknown) => value === activeEntry,
    open: async (value: unknown, current: () => boolean, anchor: unknown, select: unknown, extraActions: unknown) => {opened.push({value, current, anchor, select, extraActions}); activeEntry = value; menus.emit();}};
  const context = {current: () => live, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}),
    subscribeAuthority: authorityEvents.subscribe, libraries: () => libraries, navigation: {subscribe: route.subscribe, getSnapshot: () => snapshot},
    playback: {start: (input: unknown) => {starts.push(input); return new Promise<void>(resolve => {finish.push(resolve);});}}};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: hooks().react, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async (_context: unknown, target: unknown) => {reads.push(target); return new Promise<boolean>(() => {});}},
    '../native-screen-services': {nativeScreenServices: () => ({navigate: (value: unknown) => {navigated.push(value);}, reportError: (error: unknown) => {errors.push(error);}})},
    '../watch': {nativeWatchEntryMenu: (actual: unknown) => {assert.equal(actual, context); return menuReady ? port : undefined;}},
  });
  const actions = createNativeEntryActions(context, {current: () => visible, contains: (value: unknown) => value === member, subscribe: membership.subscribe});
  return {actions, entry, starts, reads, opened, navigated, errors, finish, membership, route, menus,
    setMenuReady: () => {menuReady = true;}, setLibraries: (next: typeof libraries) => {libraries = next; membership.emit();}, replace: () => {member = {...entry}; membership.emit();},
    navigate: () => {navigationRevision++; snapshot = {revision: navigationRevision, route: {kind: 'shell'}}; route.emit();}, retire: () => {live = false; authorityEvents.emit();},
    hide: () => {visible = false; route.emit();}, listeners: [route.listeners, membership.listeners, authorityEvents.listeners]};
}

test('nonHome playback uses the actual member and navigation lifetime without an old output fence', async () => {
  const f = await fixture();
  f.actions.open(f.entry); assert.deepEqual(f.navigated, [f.entry]);
  f.actions.play(f.entry);
  const first = f.starts[0];
  assert.equal(first.entry, f.entry); assert.equal(first.target, f.entry.playback); assert.equal(first.lifetime.isCurrent(), true);
  assert.equal('expectedFence' in first, false);
  f.replace(); assert.equal(first.lifetime.signal.aborted, true); assert.equal(first.lifetime.isCurrent(), false);
  f.actions.play(f.entry); assert.equal(f.starts.length, 1, 'a replaced loaded member cannot start playback');
  f.finish[0](); await Promise.resolve();
  f.actions.dispose(); assert.ok(f.listeners.every(set => set.size === 0));
});

test('entry menus borrow the registered module at event time and keep the actual anchor and live expanded state', async () => {
  const f = await fixture(), observed: number[] = [], anchor = {current: {actual: 'card'}};
  const off = f.actions.menuState.subscribe(() => {observed.push(f.actions.menuState.getSnapshot());});
  f.setMenuReady(); f.actions.menu(f.entry, anchor);
  assert.equal(f.opened.length, 1); assert.equal(f.opened[0].value, f.entry); assert.equal(f.opened[0].anchor, anchor);
  assert.equal(f.actions.isMenuOpen(f.entry), true); assert.equal(observed.length, 1);
  assert.equal(f.opened[0].current(), true); f.navigate(); assert.equal(f.opened[0].current(), false);
  f.actions.play(f.entry); const second = f.starts[0];
  f.hide(); assert.equal(second.lifetime.signal.aborted, true);
  off(); assert.equal(f.menus.listeners.size, 0);
  f.retire(); f.actions.open(f.entry); assert.equal(f.navigated.length, 0);
  f.finish[0](); f.actions.dispose();
});


test('song primary activation dispatches the offered playback with the original membership fence', async () => {
  const f = await fixture();
  Object.assign(f.entry, {kind: 'song'});
  f.actions.open(f.entry);
  assert.equal(f.navigated.length, 0);
  assert.equal(f.starts.length, 1);
  assert.equal(f.starts[0].target, f.entry.playback);
  f.replace();
  assert.equal(f.starts[0].lifetime.isCurrent(), false);
  f.finish[0](); f.actions.dispose();
});


test('Reading opening captures the offered member and retires on replacement or navigation', async () => {
  const f = await fixture();
  Object.assign(f.entry, {kind: 'ebook'});
  f.actions.open(f.entry);
  assert.equal(f.reads.length, 1);
  assert.equal(f.reads[0].libraryId, 'movies');
  assert.equal(f.reads[0].resident(), true);
  f.replace();
  assert.equal(f.reads[0].signal.aborted, true);
  assert.equal(f.reads[0].resident(), false);
  f.actions.dispose();
});


test('Reading is refused when its exact authorized library is absent or wrong kind', async () => {
  const f = await fixture(); Object.assign(f.entry, {kind: 'comic'});
  f.actions.open(f.entry); assert.equal(f.reads.length, 0); assert.equal(f.errors.length, 1);
  f.setLibraries([{id: 'movies', kind: 'comic'}]); f.actions.open(f.entry);
  assert.equal(f.reads.length, 1); assert.equal(f.reads[0].resident(), true);
  f.setLibraries([]); assert.equal(f.reads[0].resident(), false); f.actions.dispose();
});

test('navigation replacement aborts Reading and Back cannot revive it', async () => {
  const f = await fixture(); Object.assign(f.entry, {kind: 'ebook'});
  f.actions.open(f.entry); const first = f.reads[0];
  f.navigate(); assert.equal(first.signal.aborted, true); assert.equal(first.resident(), false);
  f.navigate(); assert.equal(first.resident(), false); f.actions.dispose();
});

test('audiobook file primary activation uses its offered playback', async () => {
  const f = await fixture(); Object.assign(f.entry, {kind: 'audiobook_file'});
  f.actions.open(f.entry); assert.equal(f.starts.length, 1); assert.equal(f.starts[0].target, f.entry.playback);
  f.finish[0](); f.actions.dispose();
});


test('composed menu actions keep exact membership and navigation custody and disabled actions stay inert', async () => {
  const f = await fixture(); f.setMenuReady();
  let calls = 0;
  const extra = [{id: 'not-interested', label: 'Not interested', onSelect: () => calls++},
    {id: 'disabled', label: 'Unavailable', disabled: true, onSelect: () => calls++}];
  f.actions.menu(f.entry, undefined, undefined, extra);
  const first = f.opened[0].extraActions;
  assert.notEqual(first, extra);
  first[0].onSelect(); first[1].onSelect(); assert.equal(calls, 1);
  f.navigate(); first[0].onSelect(); assert.equal(calls, 1, 'an old menu cannot borrow a new route revision');
  f.actions.menu(f.entry, undefined, undefined, extra);
  const second = f.opened[1].extraActions;
  f.replace(); second[0].onSelect(); assert.equal(calls, 1, 'replacement with the same item ID is not the original member');
  f.actions.dispose(); second[0].onSelect(); assert.equal(calls, 1);
});
