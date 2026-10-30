import assert from 'node:assert/strict';
import test from 'node:test';
import {planHomeWindows, type HomeWindowPopulation} from '../src-next/shell/home-windows';

const row = (id: string, first: number, last: number, visible = true, retainedIds?: readonly string[]): HomeWindowPopulation => ({id, first, last, visible, retainedIds,
  physicalIds: Array.from({length: 50}, (_, index) => `${id}/${index}`)});

test('one global32 population includes every visible card, all spotlights and hidden retained openers before optional overscan', () => {
  const rows = [row('one', 4, 11), row('two', 7, 14), row('three', 20, 27), row('menu', 0, -1, false, ['menu/31']), row('player', 0, -1, false, ['player/40'])];
  const plan = planHomeWindows(rows, 5);
  assert.equal([...plan.values()].reduce((sum, ids) => sum + ids.length, 5), 32);
  for (const item of rows) {
    const required = item.visible ? item.physicalIds.slice(item.first, item.last + 1) : item.retainedIds!;
    for (const id of required) assert.ok(plan.get(item.id)?.includes(id), `${id} remains interactive`);
  }
  assert.deepEqual(plan.get('menu'), ['menu/31']);
  assert.deepEqual(plan.get('player'), ['player/40']);
});

test('a retained onscreen opener is counted once, and evicted rows cannot consume the next viewport budget', () => {
  const live = row('live', 0, 5, true, ['live/3']);
  const plan = planHomeWindows([live, row('offscreen', 0, 8, false)], 1);
  assert.equal(new Set(plan.get('live')).size, plan.get('live')!.length);
  assert.deepEqual(plan.get('offscreen'), []);
  const full = planHomeWindows([row('full', 0, 30)], 1);
  assert.equal(full.get('full')!.length, 31);
  assert.throws(() => planHomeWindows([row('full', 0, 31)], 1), /home-visible-registration-limit/, 'a real visible overflow is reported, never hidden by silently dropping an interactive card');
});


test('physical holes report exact viewport keys instead of relabelling array indices as occupied slots', () => {
  const held = {...row('held', 12, 14), physicalIds: ['held/0', 'held/9', 'held/12', 'held/14'], visibleIds: ['held/12', 'held/14'], retainedIds: ['held/9']};
  const plan = planHomeWindows([held], 5);
  assert.deepEqual(plan.get('held'), ['held/9', 'held/12', 'held/14']);
});
