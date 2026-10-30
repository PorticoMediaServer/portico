import {captureGridAnchor, gridAnchorOffset} from '../src-next/kit/layout/model.ts';
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {alphabetAnchors, alphabetBand, anchoredOffset, createArtworkReadiness, createPreviewDebounce, createRotationClock, flowSectionWidth, gridWindow, type PreviewScheduler} from '../src-next/kit/layout/model';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed';
import {itemSelection} from '../src-next/kit/layout/types';

test('a 50,000-item grid derives a bounded window at the beginning, middle and end', () => {
  for (const offset of [0, 100, 500_000, 4_000_000]) {
    const window = gridWindow(50_000, 3, 220, offset, 630, 2);
    const mounted = (window.endRow - window.startRow + 1) * 3;
    assert.ok(mounted <= 24);
    assert.ok(window.first >= 0 && window.last < 50_000);
    assert.equal(window.height, Math.ceil(50_000 / 3) * 220);
  }
});

test('the last incomplete row never invents item IDs', () => {
  const window = gridWindow(50_000, 3, 220, 3_666_000, 630);
  assert.equal(window.last, 49_999);
  assert.equal(window.endRow, 16_666);
});

test('empty collection has no visible or mounted items', () => {
  assert.deepEqual(gridWindow(0, 3, 220, 0, 630), {first: 0, last: -1, startRow: 0, endRow: -1, height: 0});
});

test('visible range changes at both partially visible row boundaries', () => {
  assert.deepEqual([gridWindow(100, 3, 200, 0, 600).first, gridWindow(100, 3, 200, 0, 600).last], [0, 8]);
  assert.deepEqual([gridWindow(100, 3, 200, 1, 600).first, gridWindow(100, 3, 200, 1, 600).last], [0, 11]);
  assert.deepEqual([gridWindow(100, 3, 200, 201, 600).first, gridWindow(100, 3, 200, 201, 600).last], [3, 14]);
});

test('sparse collection requests only viewport pages after a far letter jump', async () => {
  const requests: Array<[number, number]> = [];
  const collection = createWindowedCollection({pageSize: 30, maxResidentPages: 4, maxConcurrent: 1, prefetchPages: 0,
    keyOf: (item: {id: string}) => item.id,
    fetchPage: async (start, count) => {requests.push([start, count]); return {total: 50_000, items: Array.from({length: Math.min(count, 50_000 - start)}, (_, i) => ({id: `item-${start + i}`})), positionIndex: {A: 0, M: 25_000}};}});
  collection.ensureRange(0, 8);
  await new Promise(resolve => setImmediate(resolve));
  collection.ensureRange(25_000, 25_011);
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(requests.length <= 3);
  assert.ok(requests.every(([, count]) => count === 30));
  assert.equal(collection.keyAt(25_000), 'item-25000');
  assert.equal(collection.itemAt(10_000), undefined);
  assert.equal(collection.indexOfLetter('M'), 25_000);
  assert.ok(collection.resident().items <= 120);
  collection.dispose();
});

test('selection identity stays with the ID through sort and window changes', () => {
  const selected = new Set(['stable-id']);
  const toggled: string[] = [], entered: string[] = [];
  const selection = {active: true, isSelected: (id: string) => selected.has(id), onToggle: (id: string) => toggled.push(id), onEnter: (id: string) => entered.push(id)};
  const before = [{id: 'stable-id'}, {id: 'other'}], after = [...before].reverse();
  assert.equal(itemSelection(selection, before[0].id)?.selected, true);
  assert.equal(itemSelection(selection, after[1].id)?.selected, true);
  assert.equal(itemSelection(selection, after[0].id)?.selected, false);
  itemSelection(selection, after[1].id)?.onToggle(); itemSelection(selection, after[0].id)?.onEnter();
  assert.deepEqual(toggled, ['stable-id']); assert.deepEqual(entered, ['other']);
  assert.equal(itemSelection(undefined, 'stable-id'), undefined);
});

test('safe-edge anchoring holds interior focus and moves only outside the safe area', () => {
  assert.equal(anchoredOffset(200, 400, 1920, 0, 5000, 200, 96), 0);
  assert.equal(anchoredOffset(1484, 400, 1920, 0, 5000, 200, 96), 60);
  assert.equal(anchoredOffset(200, 400, 1920, 428, 5000, 200, 96), 0);
  assert.equal(anchoredOffset(4900, 400, 1920, 3000, 5000, 200, 96), 3080);
});

test('alphabet includes empty Latin letters and preserves actual server non-Latin order', () => {
  const anchors = alphabetAnchors({'#': 0, A: 3, M: 30, 'あ': 80, 'Ж': 90}, 100);
  assert.equal(anchors.length, 29);
  assert.deepEqual(anchors.slice(-2).map(anchor => anchor.key), ['あ', 'Ж']);
  assert.equal(anchors.find(anchor => anchor.key === 'B')?.index, undefined);
  assert.equal(anchors.find(anchor => anchor.key === 'M')?.index, 30);
  assert.equal(anchors.find(anchor => anchor.key === 'M')?.end, 79);
});

test('visible alphabet band intersects buckets rather than only bucket starting indices', () => {
  const anchors = alphabetAnchors({'#': 0, A: 3, M: 30, 'あ': 80}, 100);
  assert.deepEqual(alphabetBand(anchors, 34, 50), {first: 13, last: 13});
  assert.deepEqual(alphabetBand(anchors, 2, 5), {first: 0, last: 1});
  assert.equal(alphabetBand(anchors, 100, 101), undefined);
});

test('invalid or out-of-library rail offsets never become active jump targets', () => {
  const anchors = alphabetAnchors({A: -1, B: NaN, C: 5.5, D: 100, 'Ж': 30}, 100);
  assert.equal(anchors.find(anchor => anchor.key === 'A')?.index, undefined);
  assert.equal(anchors.find(anchor => anchor.key === 'D')?.index, undefined);
  assert.equal(anchors.find(anchor => anchor.key === 'Ж')?.index, 30);
});

test('rotation pauses its remaining dwell and resets after a deliberate candidate change', () => {
  const clock = createRotationClock(8000);
  assert.equal(clock.resume(1000), 8000);
  clock.pause(3500);
  assert.equal(clock.remaining(99_000), 5500);
  assert.equal(clock.resume(100_000), 5500);
  assert.equal(clock.remaining(101_000), 4500);
  clock.reset();
  assert.equal(clock.resume(200_000), 8000);
});

test('preview waits 250ms and publishes only the last focused entry', () => {
  let now = 0, next = 0;
  const tasks = new Map<number, {at: number; run: () => void}>(), seen: string[] = [];
  const scheduler: PreviewScheduler = {schedule: (run, ms) => {const id = ++next; tasks.set(id, {at: now + ms, run}); return id;}, cancel: id => {tasks.delete(id as number);}};
  const advance = (ms: number) => {now += ms; for (const [id, task] of [...tasks]) if (task.at <= now) {tasks.delete(id); task.run();}};
  const preview = createPreviewDebounce<string>(entry => seen.push(entry), 250, scheduler);
  preview.request('first'); advance(249); assert.deepEqual(seen, []);
  preview.request('second'); advance(249); assert.deepEqual(seen, []);
  advance(1); assert.deepEqual(seen, ['second']);
  preview.request('third', true); advance(1000); assert.deepEqual(seen, ['second']);
  preview.request('third'); advance(250); assert.deepEqual(seen, ['second', 'third']);
  preview.request('fourth'); preview.dispose(); advance(1000); assert.deepEqual(seen, ['second', 'third']);
});

test('flow width follows section count and cap without filling missing cards', () => {
  assert.equal(flowSectionWidth(2, 4, 172, 16), 360);
  assert.equal(flowSectionWidth(20, 4, 172, 16), 736);
  assert.equal(flowSectionWidth(0, 4, 172, 16), 0);
});

test('only the current mounted foreground can reveal its preview', () => {
  const shown: string[] = [], errors: string[] = [];
  const gate = createArtworkReadiness<string>(entry => shown.push(entry), entry => errors.push(entry));
  gate.stage('first'); assert.deepEqual(shown, []);
  gate.stage('second'); assert.equal(gate.ready('first'), false); assert.deepEqual(shown, []);
  assert.equal(gate.ready('second'), true); assert.deepEqual(shown, ['second']);
  gate.stage('third'); assert.equal(gate.error('third', new Error('decode')), true);
  assert.deepEqual(shown, ['second']); assert.deepEqual(errors, ['third']);
  assert.equal(gate.ready('third'), false);
});

test('unmount and retarget discard stale decode/error callbacks without retry', () => {
  const shown: string[] = [], errors: string[] = [];
  const gate = createArtworkReadiness<string>(entry => shown.push(entry), entry => errors.push(entry));
  gate.stage('first'); gate.stage('second');
  assert.equal(gate.error('first', new Error('stale')), false);
  gate.clear(); assert.equal(gate.ready('second'), false);
  assert.deepEqual(shown, []); assert.deepEqual(errors, []);
});


test('a fresh mounted generation with the same entry ID rejects its prior decode', () => {
  const first = {id: 'same', generation: 1}, second = {id: 'same', generation: 2};
  const shown: typeof first[] = [];
  const gate = createArtworkReadiness<typeof first>(entry => shown.push(entry), () => assert.fail('unexpected image failure'));
  gate.stage(first); gate.stage(second);
  assert.equal(gate.ready(first), false);
  assert.equal(gate.ready(second), true);
  assert.deepEqual(shown, [second]);
});


test('overscan input cannot turn a sparse grid into a full-collection render', () => {
  for (const overscan of [-3, 50_000, NaN, Infinity]) {
    const window = gridWindow(50_000, 3, 220, 500_000, 630, overscan);
    assert.ok(window.startRow <= window.endRow);
    assert.ok((window.endRow - window.startRow + 1) * 3 <= 63);
  }
});

// Source coverage for foldable frame9: identity index survives column changes,
// and the fractional offset is measured relative to the sticky edge.
test('grid reflow preserves a far-visible slot and its fractional offset', () => {
  const captured = captureGridAnchor(4063.25, 4, 400, 100, 60, 5_000_000)!;
  assert.deepEqual(captured, {index: 40, offset: -23.25});
  assert.equal(gridAnchorOffset(captured, 6, 340, 120, 80, 300_000_000, 900), 2103.25);
  assert.equal(gridAnchorOffset(captured, 2, 400, 100, 60, 1_000, 900), 100, 'clamp at the real end');
  assert.equal(captureGridAnchor(0, 4, 400, 500, 60, 5_000_000), undefined, 'header has no grid identity');
  assert.equal(captureGridAnchor(0, 4, 400, 0, 0, 0), undefined, 'empty collection has no identity');
});
