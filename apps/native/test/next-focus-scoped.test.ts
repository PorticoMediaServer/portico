import assert from 'node:assert/strict';
import test from 'node:test';
import {createFocusHandleResolver, createFocusRegistry} from '../src-next/kit/focus/registry.ts';

test('a focus move notifies only the old/new region among hundreds of mounted regions', () => {
  const registry = createFocusRegistry(), changes: string[] = [], releases: (() => void)[] = [];
  for (let index = 0; index < 400; index++) {
    const id = `row-${index}`;
    registry.configure({id, order: ['first', 'second']});
    for (const childId of ['first', 'second']) registry.attach({regionId: id, childId}, {requestTVFocus() {}});
    releases.push(registry.subscribeRegion(id, () => changes.push(id)));
  }
  registry.didFocus({regionId: 'row-17', childId: 'first'});
  changes.length = 0;
  const unrelated = registry.regionSnapshot('row-399');
  registry.didFocus({regionId: 'row-17', childId: 'second'});
  assert.deepEqual(changes, ['row-17']);
  assert.equal(registry.regionSnapshot('row-399'), unrelated);
  changes.length = 0;
  registry.didFocus({regionId: 'row-18', childId: 'first'});
  assert.deepEqual(changes, ['row-17', 'row-18']);
  for (const release of releases) release();
});

test('compound guides follow remembered descendants and entry-region dependencies', () => {
  const registry = createFocusRegistry(), changed = new Set<string>();
  registry.configure({id: 'page'});
  registry.configure({id: 'row', parent: 'page', order: ['a', 'b']});
  registry.configure({id: 'entry-guide', entryRegion: 'row'});
  registry.configure({id: 'unrelated'});
  const a = {requestTVFocus() {}}, b = {requestTVFocus() {}};
  registry.attach({regionId: 'row', childId: 'a'}, a);
  registry.attach({regionId: 'row', childId: 'b'}, b);
  for (const id of ['page', 'row', 'entry-guide', 'unrelated']) registry.subscribeRegion(id, () => changed.add(id));
  registry.didFocus({regionId: 'row', childId: 'b'});
  assert.deepEqual([...changed].sort(), ['entry-guide', 'page', 'row']);
  assert.equal(registry.entryTarget('page'), b);
  assert.equal(registry.entryTarget('entry-guide'), b);
  changed.clear();
  registry.attach({regionId: 'row', childId: 'b'}, null);
  assert.deepEqual([...changed].sort(), ['entry-guide', 'page', 'row']);
  assert.equal(registry.entryTarget('entry-guide'), a);
});

test('edge subscribers see late/replaced hosts once per event without unrelated invalidation', () => {
  const registry = createFocusRegistry();
  registry.configure({id: 'row', edges: {left: 'rail', up: 'header'}});
  let changes = 0;
  const edges = ['rail', 'header', 'rail'];
  const release = registry.subscribeTarget('row', edges, () => changes++);
  const before = registry.targetSnapshot('row', edges);
  const first = {requestTVFocus() {}}, replacement = {requestTVFocus() {}};
  registry.attachRegion('rail', first);
  assert.equal(changes, 1);
  assert.ok(registry.targetSnapshot('row', edges) > before);
  registry.attachRegion('unrelated', {requestTVFocus() {}});
  assert.equal(changes, 1);
  registry.attachRegion('rail', null);
  registry.attachRegion('rail', replacement);
  assert.equal(changes, 3);
  assert.equal(registry.host('rail'), replacement);
  release();
  registry.attachRegion('header', {requestTVFocus() {}});
  assert.equal(changes, 3);
});

test('movement within a shared rail does not notify the controls that point to its unchanged guide', () => {
  const registry = createFocusRegistry(), releases: (() => void)[] = [];
  registry.configure({id: 'rail', order: ['a', 'b']});
  registry.attachRegion('rail', {requestTVFocus() {}});
  for (const childId of ['a', 'b']) registry.attach({regionId: 'rail', childId}, {requestTVFocus() {}});
  let notified = 0;
  for (let index = 0; index < 400; index++) {
    const id = `row-${index}`;
    registry.configure({id, edges: {left: 'rail'}});
    releases.push(registry.subscribeTarget(id, ['rail'], () => notified++));
  }
  const before = registry.targetSnapshot('row-17', ['rail']);
  registry.didFocus({regionId: 'rail', childId: 'a'});
  registry.didFocus({regionId: 'rail', childId: 'b'});
  assert.equal(notified, 0);
  assert.equal(registry.targetSnapshot('row-17', ['rail']), before);
  registry.attachRegion('rail', null);
  assert.equal(notified, 400, 'removing the actual destination host invalidates every real edge');
  for (const release of releases) release();
});

test('reconfiguring parent/entry relationships retires the old dependency', () => {
  const registry = createFocusRegistry(), changed = new Set<string>();
  for (const id of ['old-parent', 'new-parent', 'row', 'other', 'guide']) registry.configure({id});
  registry.configure({id: 'row', parent: 'old-parent'});
  registry.configure({id: 'guide', entryRegion: 'row'});
  for (const id of ['old-parent', 'new-parent', 'row', 'other', 'guide']) registry.subscribeRegion(id, () => changed.add(id));
  registry.configure({id: 'row', parent: 'new-parent'});
  assert.deepEqual([...changed].sort(), ['guide', 'new-parent', 'old-parent', 'row']);
  registry.configure({id: 'guide', entryRegion: 'other'});
  changed.clear();
  registry.attach({regionId: 'row', childId: 'a'}, {requestTVFocus() {}});
  assert.deepEqual([...changed].sort(), ['new-parent', 'row']);
});

test('retained opener chains notify their own regions and releases remain idempotent', () => {
  const registry = createFocusRegistry(), changed: string[] = [];
  registry.configure({id: 'row'}); registry.configure({id: 'menu'});
  registry.attach({regionId: 'row', childId: 'a'}, {requestTVFocus() {}});
  registry.attach({regionId: 'menu', childId: 'action'}, {requestTVFocus() {}});
  registry.didFocus({regionId: 'row', childId: 'a'});
  registry.registerReturn('menu', registry.capture());
  registry.didFocus({regionId: 'menu', childId: 'action'});
  registry.subscribeRegion('row', () => changed.push('row'));
  registry.subscribeRegion('menu', () => changed.push('menu'));
  const release = registry.retain(registry.capture()!);
  assert.deepEqual(changed, ['menu', 'row']);
  assert.equal(registry.isRegionActive('row'), true);
  changed.length = 0;
  release(); release();
  assert.deepEqual(changed, ['menu', 'row']);
  assert.equal(registry.isRegionActive('row'), false);
});

test('native handle resolution caches attached hosts but retries unresolved hosts', () => {
  const first = {requestTVFocus() {}}, second = {requestTVFocus() {}};
  let ready = false, calls = 0;
  const handle = createFocusHandleResolver(host => {calls++; return host === first ? ready ? 101 : null : 202;});
  assert.equal(handle(undefined), null);
  assert.equal(calls, 0);
  assert.equal(handle(first), null);
  ready = true;
  assert.equal(handle(first), 101);
  assert.equal(handle(first), 101);
  assert.equal(calls, 2);
  assert.equal(handle(second), 202);
  assert.equal(calls, 3);
});

test('P2-71-PERF: a focus move re-renders no target of a plain row, only the regions that own focus', () => {
  const registry = createFocusRegistry(), renders = new Map<string, number>(), regionRenders: string[] = [];
  registry.configure({id: 'page'});
  registry.configure({id: 'row', parent: 'page', orientation: 'horizontal'});
  const ids = Array.from({length: 50}, (_, index) => `card-${index}`);
  for (const childId of ids) {
    registry.attach({regionId: 'row', childId}, {requestTVFocus() {}});
    registry.subscribeTarget('row', [], () => renders.set(childId, (renders.get(childId) ?? 0) + 1), childId);
  }
  for (const id of ['page', 'row']) registry.subscribeRegion(id, () => regionRenders.push(id));
  renders.clear(); // Mounting the row is structure; count only what focus moves cause.
  const before = registry.targetSnapshot('row', [], 'card-1');
  for (const childId of ids.slice(0, 10)) registry.didFocus({regionId: 'row', childId});
  assert.equal([...renders.values()].reduce((sum, count) => sum + count, 0), 0, 'ten moves along the row re-render no card');
  assert.equal(registry.targetSnapshot('row', [], 'card-1'), before);
  assert.ok(regionRenders.includes('row') && regionRenders.includes('page'), 'the owning regions still learn of the move');
  registry.attach({regionId: 'row', childId: 'late'}, {requestTVFocus() {}});
  assert.equal(renders.size, 50, 'a structural change still reaches every target');
});

test('P2-71-PERF: in a grid only the newly focused cell re-renders for its remembered column', () => {
  const registry = createFocusRegistry(), renders = new Map<string, number>();
  registry.configure({id: 'grid', grid: {columns: 6, count: 60}});
  const ids = Array.from({length: 60}, (_, index) => `t-${index}`);
  ids.forEach((childId, index) => {
    registry.attach({regionId: 'grid', childId}, {requestTVFocus() {}}, undefined, {index});
    registry.subscribeTarget('grid', [], () => renders.set(childId, (renders.get(childId) ?? 0) + 1), childId);
  });
  renders.clear(); // Mounting the grid is structure; count only what focus moves cause.
  registry.didFocus({regionId: 'grid', childId: 't-3'});
  registry.didFocus({regionId: 'grid', childId: 't-9'});
  assert.deepEqual([...renders.entries()].sort(), [['t-3', 1], ['t-9', 1]], 'two moves, two renders: the focused cells only');
  assert.equal(registry.destinations({regionId: 'grid', childId: 't-9'}).down && 'childId' in registry.destinations({regionId: 'grid', childId: 't-9'}).down!
    ? (registry.destinations({regionId: 'grid', childId: 't-9'}).down as {childId: string}).childId : undefined, 't-15', 'the focused cell still reads fresh column memory');
});
