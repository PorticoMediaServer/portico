/**
 * PERF-07: the 30 s now-tick re-renders only the now-line and the blocks whose "live now" state
 * changed. Mirrors web `guide-blocks.test.ts`.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {layoutRow, type GuideProgram} from '../../../packages/client-core/src/guide/index.ts';

const MIN = 60_000;
const T = Date.UTC(2026, 9, 26, 18, 0, 0);

const prog = (row: number, i: number, start: number, end: number): GuideProgram => ({
  id: `r${row}p${i}`,
  channelId: `r${row}`,
  title: `Programme ${row}.${i}`,
  start,
  end,
});

const rows: GuideProgram[][] = [0, 1, 2, 3, 4].map(row => {
  if (row === 0) return [0, 1, 2, 3, 4, 5].map(i => prog(row, i, T + i * 30 * MIN, T + (i + 1) * 30 * MIN));
  return [0, 1, 2, 3, 4, 5].map(i => prog(row, i, T + i * 60 * MIN, T + (i + 1) * 60 * MIN));
});

const lay = (now: number) => rows.map(list => layoutRow(list, {viewStart: T, viewEnd: T + 3 * 60 * MIN, pxPerMs: 360 / (30 * MIN), gapPx: 4, now}));

test('a now-tick ending one programme changes at most 2 blocks in a 5x6 window', () => {
  const before = lay(T + 89 * MIN + 59_000);
  const after = lay(T + 90 * MIN + 24_000);
  const stateOf = (cells: ReturnType<typeof layoutRow>) => new Map(cells.filter(c => c.kind === 'program').map(c => [c.program!.id, c.state]));
  const changed: string[] = [];
  let programCells = 0;
  for (let row = 0; row < 5; row++) {
    const a = stateOf(before[row]!);
    const b = stateOf(after[row]!);
    programCells += b.size;
    for (const [id, state] of b) {
      if (a.get(id) !== state) changed.push(`${row}:${id} ${a.get(id)} -> ${state}`);
    }
  }
  assert.equal(programCells, 5 * 6 - 4 * 3);
  assert.deepEqual(changed.sort(), ['0:r0p2 now -> past', '0:r0p3 future -> now']);
  assert.ok(changed.length <= 2);
});
