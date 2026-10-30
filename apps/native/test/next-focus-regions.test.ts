import assert from 'node:assert/strict';
import test from 'node:test';
import {createFocusRegistry, focusTestID, type FocusAddress} from '../src-next/kit/focus/registry.ts';
import {reconcileRowSlots, indexRowSlots, visibleRowSlots, rowNeedsPage, type RowPage} from '../src-next/kit/layout/row-state.ts';

function fixture() {
  const registry = createFocusRegistry(), requests: string[] = [];
  const add = (regionId: string, childId: string) => registry.attach({regionId, childId}, {requestTVFocus: () => requests.push(`${regionId}/${childId}`)});
  const configure = (id: string, order: readonly string[]) => registry.configure({id, order, entry: order[0], neighbors: {next: 'below', previous: 'above'}, screenStart: {regionId: 'spotlight', childId: 'play'}});
  configure('row', ['a', 'b', 'c']); configure('below', ['x', 'y']); configure('above', ['z']); configure('spotlight', ['play', 'list', 'details']);
  for (const [r, ids] of [['row', ['a', 'b', 'c']], ['below', ['x', 'y']], ['above', ['z']], ['spotlight', ['play', 'list', 'details']]] as const) for (const id of ids) add(r, id);
  return {registry, requests, add, configure};
}
test('authority-fenced semantic IDs are deterministic and distinguish delimiters', () => {
  assert.equal(focusTestID('server/viewer/home/row', 'source/entity'), 'portico.focus.server%2Fviewer%2Fhome%2Frow.source%2Fentity');
  assert.notEqual(focusTestID('a/b', 'c'), focusTestID('a', 'b/c'));
  assert.notEqual(focusTestID('a.b', 'c'), focusTestID('a', 'b.c'));
  assert.notEqual(focusTestID('a%2Eb', 'c'), focusTestID('a.b', 'c'));
});
test('explicit Back-to-start does not erase spotlight or independent row memories', () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'spotlight', childId: 'list'});
  registry.didFocus({regionId: 'row', childId: 'c'});
  registry.didFocus({regionId: 'below', childId: 'y'});
  registry.focus({regionId: 'spotlight', childId: 'play'});
  registry.focusRegion('row'); registry.focusRegion('below'); registry.focusRegion('spotlight');
  assert.deepEqual(requests, ['spotlight/play', 'row/c', 'below/y', 'spotlight/list']);
});
test('capture/restore returns to the exact opener without changing memory or sending navigation', () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'row', childId: 'b'}); const opener = registry.capture();
  registry.didFocus({regionId: 'below', childId: 'x'});
  assert.equal(registry.restore(opener), true); assert.deepEqual(requests, ['row/b']);
});
test('a removed opener falls next, then previous at end, with no stale node request', () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'row', childId: 'b'}); const opener = registry.capture();
  registry.didFocus({regionId: 'below', childId: 'x'});
  registry.attach({regionId: 'row', childId: 'b'}, null);
  assert.equal(registry.restore(opener), true); assert.equal(requests.at(-1), 'row/c');
  registry.attach({regionId: 'row', childId: 'c'}, null);
  assert.equal(registry.restore(opener), true); assert.equal(requests.at(-1), 'row/a');
});
test('empty row restores neighboring region memory, then above, then screen start', () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'below', childId: 'y'});
  registry.didFocus({regionId: 'row', childId: 'b'}); const opener = registry.capture();
  registry.didFocus({regionId: 'spotlight', childId: 'details'});
  for (const childId of ['a', 'b', 'c']) registry.attach({regionId: 'row', childId}, null);
  registry.restore(opener); assert.equal(requests.at(-1), 'below/y');
  for (const childId of ['x', 'y']) registry.attach({regionId: 'below', childId}, null);
  registry.restore(opener); assert.equal(requests.at(-1), 'above/z');
  registry.attach({regionId: 'above', childId: 'z'}, null);
  registry.restore(opener); assert.equal(requests.at(-1), 'spotlight/play');
});
test('selected rail entry overrides its previous memory without acting on focused items', () => {
  const {registry, requests} = fixture();
  registry.configure({id: 'row', entry: 'a', entryPolicy: 'entry', order: ['a', 'b', 'c']});
  registry.didFocus({regionId: 'row', childId: 'c'}); registry.focusRegion('row');
  assert.deepEqual(requests, ['row/a']);
});
test('nested retained openers preserve row ownership until each idempotent release', () => {
  const {registry} = fixture(), address: FocusAddress = {regionId: 'row', childId: 'b'};
  registry.didFocus(address); const a = registry.retain(address), b = registry.retain(address);
  registry.didFocus({regionId: 'below', childId: 'x'});
  assert.equal(registry.isRegionActive('row'), true);
  a(); a(); assert.equal(registry.isRegionActive('row'), true);
  b(); assert.equal(registry.isRegionActive('row'), false);
});
test('late attach/metadata/configuration never request focus in an offscreen region', async () => {
  const {registry, requests, add} = fixture();
  registry.didFocus({regionId: 'below', childId: 'x'});
  add('row', 'late'); registry.configure({id: 'row', order: ['late', 'a']});
  registry.attach({regionId: 'row', childId: 'a'}, null);
  await Promise.resolve(); assert.deepEqual(requests, []);
});
test('removal recovery waits for commit and cannot steal focus after another child acquired it', async () => {
  const {registry, requests, add} = fixture();
  registry.didFocus({regionId: 'row', childId: 'b'});
  registry.attach({regionId: 'row', childId: 'b'}, null); add('row', 'b');
  await Promise.resolve(); assert.deepEqual(requests, [], 'same identity reattached during commit');
  registry.attach({regionId: 'row', childId: 'b'}, null);
  registry.didFocus({regionId: 'below', childId: 'x'});
  await Promise.resolve(); assert.deepEqual(requests, [], 'new native focus cancels removal recovery');
});
test('removing the genuinely focused host requests the next fallback once after commit', async () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'row', childId: 'b'});
  registry.attach({regionId: 'row', childId: 'b'}, null);
  assert.deepEqual(requests, []); await Promise.resolve(); assert.deepEqual(requests, ['row/c']);
});
test('child-to-child native blur/focus never briefly releases effective row ownership', async () => {
  const {registry} = fixture();
  registry.didFocus({regionId: 'row', childId: 'a'});
  registry.didBlur({regionId: 'row', childId: 'a'});
  registry.didFocus({regionId: 'row', childId: 'b'});
  await Promise.resolve(); assert.equal(registry.isRegionActive('row'), true);
  registry.didBlur({regionId: 'row', childId: 'b'});
  await Promise.resolve(); assert.equal(registry.isRegionActive('row'), false);
});

type Item = {id: string; title?: string};
const key = (item: Item) => item.id;
const entries = (ids: string[]) => ids.map(id => ({id}));
const page = (id: string, overrides = {}): RowPage<Item> => ({hasMore: true, loading: true, waitingId: id, waitingItem: {id: `model-${id}`}, requestNext() {}, ...overrides});
test('focused row defers reranking, preserves positions under removals and appends without resetting', () => {
  let slots = reconcileRowSlots([], entries(['a', 'b', 'c']), key, false);
  slots = reconcileRowSlots(slots, entries(['c', 'b', 'a', 'd']), key, true);
  assert.deepEqual(slots.map(s => [s.id, s.position]), [['a', 0], ['b', 1], ['c', 2], ['d', 3]]);
  slots = reconcileRowSlots(slots, entries(['d', 'c', 'b']), key, true);
  assert.deepEqual(slots.map(s => [s.id, s.position]), [['b', 1], ['c', 2], ['d', 3]], 'removal before focused card keeps its drawn slot');
  slots = reconcileRowSlots(slots, entries(['d', 'c', 'b']), key, false);
  assert.deepEqual(slots.map(s => [s.id, s.position]), [['d', 0], ['c', 1], ['b', 2]]);
});
test('one stable cursor waiting slot fills at the same identity and position while held', () => {
  let slots = reconcileRowSlots([], entries(['a', 'b']), key, false, page('cursor-one'));
  assert.equal(slots.filter(s => s.waiting).length, 1); const waiting = slots[2]!;
  slots = reconcileRowSlots(slots, entries(['a', 'b']), key, true, page('cursor-one'));
  assert.deepEqual(slots[2], waiting);
  slots = reconcileRowSlots(slots, entries(['a', 'b', 'c', 'd']), key, true, page('cursor-two', {fulfilled: {waitingId: 'cursor-one', firstItemId: 'c'}}));
  assert.deepEqual(slots.slice(2).map(s => [s.id, s.focusId, s.position, s.waiting]), [['c', 'cursor-one', 2, false], ['d', 'd', 3, false], ['cursor-two', 'cursor-two', 4, true]]);
  const rerender = reconcileRowSlots(slots, entries(['a', 'b', 'c', 'd']), key, true, page('cursor-two'));
  assert.deepEqual(rerender, slots, 'ordinary render cannot remount or advance the cursor anchor');
});
test('true exhaustion/failure removes waiting target; short rows never invent a next page', () => {
  const slots = reconcileRowSlots([], entries(['a', 'b']), key, false, page('cursor'));
  assert.equal(reconcileRowSlots(slots, entries(['a', 'b']), key, true, page('cursor', {hasMore: false})).some(s => s.waiting), false);
  assert.equal(reconcileRowSlots(slots, entries(['a', 'b']), key, true, page('cursor', {failed: true})).some(s => s.waiting), false);
  assert.equal(reconcileRowSlots([], entries(['a']), key, false).length, 1);
  assert.throws(() => reconcileRowSlots([], entries(['a', 'a']), key, true), /unique/);
});
test('next-page admission starts about ten cards before loaded end and never loops on loading/error/end', () => {
  assert.equal(rowNeedsPage(9, 20, {hasMore: true, loading: false}), false);
  assert.equal(rowNeedsPage(10, 20, {hasMore: true, loading: false}), true);
  for (const p of [{hasMore: false, loading: false}, {hasMore: true, loading: true}, {hasMore: true, loading: false, failed: true}]) assert.equal(rowNeedsPage(20, 20, p), false);
  assert.equal(rowNeedsPage(0, 0, {hasMore: true, loading: false}), false);
});

test('50,000-card focus traversal reads a bounded window plus remembered host', () => {
  const items = Array.from({length: 50_000}, (_, n) => ({id: `source-${n}`}));
  const index = indexRowSlots(reconcileRowSlots([], items, key, false));
  const visible = visibleRowSlots(index, 100, 105, 'source-100', 'source-49999');
  assert.equal(visible.length, 11); assert.ok(visible.some(slot => slot.id === 'source-49999'));
  assert.ok(visible.every(slot => slot.id === 'source-49999' || slot.position >= 98 && slot.position <= 107));
});

test('semantic focus IDs remain independent from raw selection/data keys through cursor fills', () => {
  const identity = (item: Item) => JSON.stringify(['movie', 'actual-library', item.id]);
  let slots = reconcileRowSlots([], entries(['a', 'b']), key, false, page('cursor'), identity);
  assert.equal(slots[0]!.id, 'a'); assert.equal(slots[0]!.focusId, identity({id: 'a'}));
  slots = reconcileRowSlots(slots, entries(['a', 'b', 'c']), key, true, page('cursor', {hasMore: false, fulfilled: {waitingId: 'cursor', firstItemId: 'c'}}), identity);
  assert.equal(slots[2]!.id, 'c'); assert.equal(slots[2]!.focusId, 'cursor');
  assert.equal(slots[2]!.sourceFocusId, identity({id: 'c'}));
});

test('nested sheet returns through a removed menu to its original page opener', () => {
  const {registry, requests} = fixture();
  registry.didFocus({regionId: 'row', childId: 'b'}); const page = registry.capture();
  registry.configure({id: 'menu', order: ['resume', 'file']});
  registry.registerReturn('menu', page);
  registry.attach({regionId: 'menu', childId: 'file'}, {requestTVFocus: () => requests.push('menu/file')});
  registry.didFocus({regionId: 'menu', childId: 'file'}); const menu = registry.capture();
  registry.didFocus({regionId: 'below', childId: 'x'});
  registry.attach({regionId: 'menu', childId: 'file'}, null);
  registry.attach({regionId: 'row', childId: 'b'}, null);
  assert.equal(registry.restore(menu), true); assert.deepEqual(requests, ['row/c']);
});

test('F02 appended siblings refresh active order before capture and direct removal', async () => {
  const registry = createFocusRegistry(), requests: string[] = [];
  for (const id of ['a', 'b', 'c']) registry.attach({regionId: 'row', childId: id}, {requestTVFocus: () => requests.push(id)});
  registry.configure({id: 'row', order: ['a', 'b']}); registry.didFocus({regionId: 'row', childId: 'b'});
  registry.configure({id: 'row', order: ['a', 'b', 'c']});
  const opener = registry.capture(); assert.deepEqual(opener?.order, ['a', 'b', 'c']);
  registry.configure({id: 'row', order: ['a', 'c']}); registry.attach({regionId: 'row', childId: 'b'}, null);
  await Promise.resolve(); assert.deepEqual(requests, ['c']);
  registry.configure({id: 'row', order: ['c', 'a']}); registry.restore(opener);
  assert.deepEqual(requests, ['c', 'c'], 'saved opener keeps its pre-removal relative order');
});
test('F03 waiting position stays stationary through unrelated removals and appends', () => {
  const first = reconcileRowSlots([], entries(['a', 'b']), key, false, page('pending'));
  const removed = reconcileRowSlots(first, entries(['a']), key, true, page('pending'));
  assert.equal(removed.find(s => s.waiting)?.position, 2);
  const appended = reconcileRowSlots(removed, entries(['a', 'x']), key, true, page('pending'));
  assert.equal(appended.find(s => s.waiting)?.position, 2); assert.equal(appended.find(s => s.id === 'x')?.position, 3);
});
test('F04 only explicitly fulfilled cursor response consumes the waiting alias', () => {
  let slots = reconcileRowSlots([], entries(['a', 'b']), key, false, page('pending'));
  slots = reconcileRowSlots(slots, entries(['a', 'b', 'subscription']), key, true, page('pending'));
  assert.equal(slots.find(s => s.waiting)?.focusId, 'pending'); assert.equal(slots.find(s => s.id === 'subscription')?.focusId, 'subscription');
  slots = reconcileRowSlots(slots, entries(['a', 'b', 'subscription', 'response']), key, true,
    page('next', {fulfilled: {waitingId: 'wrong-request', firstItemId: 'response'}}));
  assert.equal(slots.find(s => s.id === 'response')?.focusId, 'response');
});
test('empty/filtered response advances backend cursor while preserving the waiting host and position', () => {
  const first = reconcileRowSlots([], entries(['a']), key, false, page('one'));
  const second = reconcileRowSlots(first, entries(['a']), key, true, page('two', {fulfilled: {waitingId: 'one'}}));
  assert.equal(second[1]?.focusId, 'one'); assert.equal(second[1]?.requestId, 'two'); assert.equal(second[1]?.position, 1);
  const third = reconcileRowSlots(second, entries(['a', 'b']), key, true, page('three', {fulfilled: {waitingId: 'two', firstItemId: 'b'}}));
  assert.equal(third[1]?.focusId, 'one'); assert.equal(third[1]?.id, 'b'); assert.equal(third[1]?.position, 1);
});
test('F05 virtualized removal pins the surviving predecessor before recovery and keeps its real host', async () => {
  const old = indexRowSlots(reconcileRowSlots([], entries(Array.from({length: 100}, (_, i) => `${i}`)), key, false));
  const next = indexRowSlots(reconcileRowSlots(old.slots, entries(Array.from({length: 90}, (_, i) => `${i}`)), key, true));
  const registry = createFocusRegistry(), requests: string[] = [];
  registry.configure({id: 'row', order: old.order}); registry.attach({regionId: 'row', childId: '99'}, {requestTVFocus() {}});
  registry.didFocus({regionId: 'row', childId: '99'});
  const visible = visibleRowSlots(next, 97, 89, '99', '99', registry.recovery('row'));
  assert.deepEqual(visible.map(slot => slot.focusId), ['89']);
  registry.attach({regionId: 'row', childId: '99'}, null);
  registry.attach({regionId: 'row', childId: '89'}, {requestTVFocus: () => requests.push('89')});
  registry.configure({id: 'row', order: next.order}); await Promise.resolve(); assert.deepEqual(requests, ['89']);
});
test('F08 nested overlay retains every distinct opener region until the dependent surface closes', () => {
  const {registry} = fixture(); registry.didFocus({regionId: 'row', childId: 'b'});
  const page = registry.capture()!, releaseMenu = registry.retain(page);
  const unregister = registry.registerReturn('menu', page);
  registry.didFocus({regionId: 'menu', childId: 'file'});
  const releaseSheet = registry.retain(registry.capture()!);
  registry.didFocus({regionId: 'sheet', childId: 'close'}); unregister(); releaseMenu();
  assert.equal(registry.isRegionActive('row'), true); assert.equal(registry.isRegionActive('menu'), true);
  releaseSheet(); releaseSheet(); assert.equal(registry.isRegionActive('row'), false); assert.equal(registry.isRegionActive('menu'), false);
});
test('F12 region teardown bounds re-entry memory and preserves live/retained regions', async () => {
  const registry = createFocusRegistry();
  for (let i = 0; i < 1000; i++) {
    const id = `page/${i}`, owner = {}, release = registry.mountRegion(id, owner);
    registry.configure({id, order: Array.from({length: 100}, (_, j) => `child/${j}`)});
    registry.attach({regionId: id, childId: 'child/0'}, {requestTVFocus() {}}, owner);
    registry.didFocus({regionId: id, childId: 'child/0'});
    registry.attach({regionId: id, childId: 'child/0'}, null, owner); release();
  }
  await Promise.resolve(); await Promise.resolve(); assert.deepEqual(registry.size(), {regions: 0, remembered: 64});
  const release = registry.mountRegion('retained', {}); registry.didFocus({regionId: 'retained', childId: 'x'});
  const unretain = registry.retain(registry.capture()!); release(); await Promise.resolve();
  assert.ok(registry.config('retained')); unretain(); await Promise.resolve(); await Promise.resolve();
  assert.equal(registry.config('retained'), undefined);
});
test('F13 duplicate live host owners are diagnosed and stale owner cleanup cannot detach another target', () => {
  const registry = createFocusRegistry(), address = {regionId: 'r', childId: 'a'}, ownerA = {}, ownerB = {};
  const host = {requestTVFocus() {}}; registry.attach(address, host, ownerA);
  assert.throws(() => registry.attach(address, host, ownerB), /Duplicate live focus child/);
  registry.attach(address, null, ownerB); assert.equal(registry.child(address), host);
  registry.attachRegion('r', host, ownerA); assert.throws(() => registry.attachRegion('r', {focus() {}}, ownerB), /Duplicate/);
  registry.attachRegion('r', null, ownerB); assert.equal(registry.host('r'), host);
  const release = registry.mountRegion('r', ownerA); assert.throws(() => registry.mountRegion('r', ownerB), /Duplicate/); release();
});


test('compound sections remember the exact descendant region and child through native guide entry', () => {
  const registry = createFocusRegistry(), requests: string[] = [];
  registry.configure({id: 'episodes', entryRegion: 'tiles'});
  registry.configure({id: 'tiles', parent: 'episodes', entry: 'episode-3', order: ['episode-1', 'episode-2', 'episode-3']});
  registry.configure({id: 'seasons', parent: 'episodes', entry: 'season-2'});
  for (const [regionId, childId] of [['tiles', 'episode-1'], ['tiles', 'episode-3'], ['seasons', 'season-2']]) {
    registry.attach({regionId, childId}, {requestTVFocus: () => requests.push(`${regionId}/${childId}`)});
  }
  assert.equal(registry.focusRegion('episodes'), true);
  assert.deepEqual(requests, ['tiles/episode-3']);
  registry.didFocus({regionId: 'seasons', childId: 'season-2'});
  registry.focusRegion('episodes');
  assert.equal(requests.at(-1), 'seasons/season-2');
  registry.didFocus({regionId: 'tiles', childId: 'episode-1'});
  registry.focusRegion('episodes');
  assert.equal(requests.at(-1), 'tiles/episode-1');
  const before = requests.length;
  registry.configure({id: 'tiles', parent: 'episodes', entry: 'episode-3', order: ['episode-1', 'episode-2', 'episode-3', 'episode-4']});
  assert.equal(requests.length, before, 'data and configuration never issue a focus request');
  registry.focusRegion('episodes'); assert.equal(requests.at(-1), 'tiles/episode-1');
});

test('a removed compound descendant falls back to a surviving entry, never a disabled child region', () => {
  const registry = createFocusRegistry(), calls: string[] = [];
  registry.configure({id: 'section', entryRegion: 'row'});
  registry.configure({id: 'row', parent: 'section', entry: 'a', order: ['a', 'b']});
  for (const childId of ['a', 'b']) registry.attach({regionId: 'row', childId}, {requestTVFocus: () => calls.push(childId)});
  registry.didFocus({regionId: 'row', childId: 'b'});
  registry.attach({regionId: 'row', childId: 'b'}, null);
  registry.focusRegion('section'); assert.equal(calls.at(-1), 'a');
  registry.configure({id: 'row', parent: 'section', entry: 'a', enabled: false});
  assert.equal(registry.focusRegion('section'), false);
  registry.configure({id: 'section', entryRegion: 'section'});
  assert.equal(registry.focusRegion('section'), false, 'a malformed cycle cannot recurse');
});
test('a rows region whose declared ids match no host enters at its first mounted row, not the one id that matches', () => {
  const registry = createFocusRegistry(), requests: string[] = [];
  const add = (childId: string) => registry.attach({regionId: 'rows', childId}, {requestTVFocus: () => requests.push(childId)});
  // Settings names rows 'profiles' and 'signOut' but the panel hosts itself as 'settings-panel-profiles'.
  registry.configure({id: 'rows', entryPolicy: 'entry', orientation: 'vertical', order: ['profiles', 'signOut'], entry: 'profiles'});
  add('settings-panel-profiles'); add('signOut');
  registry.focusRegion('rows');
  assert.deepEqual(requests, ['signOut'], 'declaring unhosted ids is the bug');
  requests.length = 0;
  registry.configure({id: 'rows', entryPolicy: 'entry', orientation: 'vertical'});
  registry.focusRegion('rows');
  assert.deepEqual(requests, ['settings-panel-profiles']);
});
