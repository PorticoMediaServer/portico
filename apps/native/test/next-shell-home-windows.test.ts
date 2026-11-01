import assert from 'node:assert/strict';
import test from 'node:test';
import {homeRegistrationLimit, planHomeWindows, planHomeWindowsReport, type HomeWindowPopulation} from '../src-next/shell/home-windows';
import {homeSelectionWindowLimit} from '../../../packages/client-core/src/home-selection/limits.ts';

const row = (id: string, first: number, last: number, visible = true, retainedIds?: readonly string[]): HomeWindowPopulation => ({id, first, last, visible, retainedIds,
  physicalIds: Array.from({length: 50}, (_, index) => `${id}/${index}`)});

test('one global32 population includes every visible card, all spotlights and hidden retained openers before optional overscan', () => {
  const rows = [row('one', 4, 11), row('two', 7, 14), row('three', 20, 27), row('menu', 0, -1, false, ['menu/31']), row('player', 0, -1, false, ['player/40'])];
  const plan = planHomeWindows(rows, 5, 32);
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
  const full = planHomeWindows([row('full', 0, 30)], 1, 32);
  assert.equal(full.get('full')!.length, 31);
  // A real visible overflow is reported for diagnostics, never thrown at the viewer as an error.
  const over = planHomeWindowsReport([row('full', 0, 31)], 1, 32);
  assert.equal(over.overflow, 1);
  assert.equal(over.plan.get('full')!.length, 31, 'every card that fits stays interactive, in row order');
});


test('physical holes report exact viewport keys instead of relabelling array indices as occupied slots', () => {
  const held = {...row('held', 12, 14), physicalIds: ['held/0', 'held/9', 'held/12', 'held/14'], visibleIds: ['held/12', 'held/14'], retainedIds: ['held/9']};
  const plan = planHomeWindows([held], 5, 32);
  assert.deepEqual(plan.get('held'), ['held/9', 'held/12', 'held/14']);
});

test('the default budget is the Home selection limit and fits a full television Home', () => {
  // Walk, 1 Nov: three poster rows on a 1920 TV overflowed 32 and showed an error toast.
  // Four rows of nine visible cards, five spotlights and two retained openers fit with room for overscan.
  assert.equal(homeSelectionWindowLimit, 64);
  const rows = ['a', 'b', 'c', 'd'].map((id, index) => row(id, 0, 8, true, index === 0 ? ['a/20', 'a/21'] : undefined));
  const report = planHomeWindowsReport(rows, 5);
  assert.equal(report.overflow, 0);
  const used = [...report.plan.values()].reduce((sum, ids) => sum + ids.length, 5);
  assert.ok(used <= homeSelectionWindowLimit && used > 4 * 9 + 5 + 2, `overscan fills the room left (${used})`);
});

test('running out of registrations is never shown to the viewer as an error toast', async () => {
  const coded = (code: string) => Object.assign(new Error(code), {code});
  assert.ok(homeRegistrationLimit(coded('home-visible-registration-limit')));
  assert.ok(homeRegistrationLimit(coded('registration-limit')));
  assert.ok(homeRegistrationLimit(new Error('registration-limit')));
  assert.ok(!homeRegistrationLimit(coded('unavailable')));
  const {readFile} = await import('node:fs/promises');
  const assembly = await readFile(new URL('../src-next/shell/assembly.tsx', import.meta.url), 'utf8');
  const report = assembly.slice(assembly.indexOf('const reportError = '), assembly.indexOf('feedback.reportError(error, \'home\')'));
  assert.match(report, /homeActionDiagnostics\.error\(origin, error\)[\s\S]*if \(homeRegistrationLimit\(error\)\) return;/, 'recorded, then returns before the toast');
});
