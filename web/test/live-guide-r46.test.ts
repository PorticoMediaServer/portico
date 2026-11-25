import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';

// The Guide screen's pure cell/window rules. CSS modules and JSX are loaded the way the
// running app resolves them; nothing is rendered, so this is behaviour proof, not visuals.
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module' as const, source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module' as const, source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {GUIDE_CELL_INSET, channelKey, guideCellFacts, guideCellProgress, guideTickLabel, guideVisibleRows, handleGuideGridKeyDown} = await import('../src/screens/live/GuideScreen.tsx');
const {layoutRow} = await import('../../packages/client-core/src/guide/layout.ts');
const {rulerTicks} = await import('../../packages/client-core/src/guide/time.ts');
test.after(() => loader.deregister());

const part = (id: string, text: string) => ({id, text});

test('A guide cell shows status flags, then the episode; a programme without episode facts shows its airing time', () => {
  const episodic = guideCellFacts([part('time-range', '3:30 to 4:30 PM'), part('episode-label', 'S3 E1'), part('programme-subtitle', 'Homecoming'),
    part('live', 'Live'), part('new', 'New'), part('premiere', 'Premiere'), part('recording', 'Recording')]);
  assert.deepEqual(episodic.flags.map(item => item.id), ['live', 'new', 'premiere']);
  assert.deepEqual(episodic.detail.map(item => item.id), ['episode-label', 'programme-subtitle']);

  const plain = guideCellFacts([part('time-range', '3:30 to 4:30 PM'), part('live', 'Live')]);
  assert.deepEqual(plain.flags.map(item => item.id), ['live']);
  assert.deepEqual(plain.detail.map(item => item.id), ['time-range']);
});

test('Guide progress is drawn only for the airing that is on now, bounded to its span', () => {
  const now = Date.parse('2026-10-21T19:05:00Z');
  const onAir = {state: 'now' as const, start: now - 35 * 60_000, end: now + 25 * 60_000};
  assert.equal(guideCellProgress(onAir, now), 35 / 60);
  assert.equal(guideCellProgress({...onAir, state: 'future'}, now), undefined);
  assert.equal(guideCellProgress({...onAir, state: 'past'}, now), undefined);
  assert.equal(guideCellProgress({state: 'now', start: now, end: now}, now), undefined);
  assert.equal(guideCellProgress({state: 'now', start: now - 60 * 60_000, end: now - 1}, now), 1);
  assert.equal(guideCellProgress({state: 'now', start: now + 1, end: now + 60_000}, now), 0);
});

test('A guide body that scrolls with the page loads its visible rows plus one row either side', () => {
  assert.deepEqual(guideVisibleRows(0, 1909, 76, 14), {first: 0, last: 13});
  // The body starts 1200px above the viewport top: row 15 is the first visible row.
  assert.deepEqual(guideVisibleRows(-1200, 1909, 76, 40), {first: 14, last: 39});
  assert.deepEqual(guideVisibleRows(-1200, 1909, 76, 14), {first: 13, last: 13});
  assert.deepEqual(guideVisibleRows(12, 1909, 76, 0), {first: 0, last: 0});
});

test('The drawn cell inset keeps every programme on its true right edge', () => {
  const now = Date.parse('2026-10-21T19:05:00Z');
  const start = now - 35 * 60_000, end = start + 2 * 3_600_000, scale = 240 / 1_800_000;
  const [first] = layoutRow([{id: 'a', channelId: 'c', title: 'A', start, end: start + 3_600_000}], {viewStart: start, viewEnd: end, pxPerMs: scale, now, gapPx: 2});
  assert.equal(GUIDE_CELL_INSET, 2);
  assert.equal(first!.x + GUIDE_CELL_INSET, 2);
  assert.equal(first!.x + GUIDE_CELL_INSET + (first!.width - GUIDE_CELL_INSET), 3_600_000 * scale - 2);
});

test('channelKey scopes rows by kind, sourceId and id so duplicate channel ids across sources never collide', () => {
  const fromA = {id: '101', sourceId: 'src-a', kind: 'live' as const, number: '101', name: 'News A', group: '', favorite: false, tuneAvailable: true, recordAvailable: true, guide: 'full' as const};
  const fromB = {...fromA, sourceId: 'src-b', name: 'News B'};
  const fromLibrary = {...fromA, sourceId: 'src-a', kind: 'library' as const};
  assert.notEqual(channelKey(fromA), channelKey(fromB));
  assert.notEqual(channelKey(fromA), channelKey(fromLibrary));
});

test('midnight day ticks display the new day in the guide timezone instead of repeating a plain time', () => {
  const start = Date.parse('2026-10-21T23:00:00Z');
  const end = Date.parse('2026-10-22T01:30:00Z');
  const ticks = rulerTicks(start, end, 240 / 1_800_000, 'UTC');
  const midnight = ticks.find(tick => tick.kind === 'day');
  const hour = ticks.find(tick => tick.kind === 'hour');
  assert.ok(midnight, 'expected a midnight day tick');
  assert.ok(hour, 'expected an hour tick');
  assert.match(guideTickLabel(midnight, 'UTC'), /Thu.*Oct.*22/);
  assert.match(guideTickLabel(hour, 'UTC'), /11:00|1:00/);
});

test('guide keyboard navigation handles ArrowUp/ArrowDown, R/W/N shortcuts, composite row lookup, and ignores modified combos', () => {
  type FakeButton = {
    dataset: Record<string, string>;
    style: {left: string; width: string};
    focused: number;
    parentElement?: FakeLane;
    row?: FakeRow;
    focus(): void;
    closest(selector: string): unknown;
    getBoundingClientRect(): {left: number; right: number; width: number};
  };
  type FakeLane = {querySelectorAll(selector: string): FakeButton[]};
  type FakeRow = {
    dataset: Record<string, string>;
    parentElement?: FakeContainer;
    cells: FakeButton[];
    querySelectorAll(selector: string): FakeButton[];
  };
  type FakeContainer = {rows: FakeRow[]; querySelectorAll(selector: string): FakeRow[]};

  const makeRow = (rowKey: string, specs: readonly {id: string; start: number; left: number; width: number}[]): FakeRow => {
    const lane: FakeLane = {querySelectorAll: () => row.cells};
    const row: FakeRow = {dataset: {guideRow: rowKey}, cells: [], querySelectorAll: () => row.cells};
    row.cells = specs.map(spec => {
      const btn: FakeButton = {
        dataset: {kind: 'program', programId: spec.id, programStart: String(spec.start)},
        style: {left: `${spec.left}px`, width: `${spec.width}px`},
        focused: 0,
        parentElement: lane,
        row,
        focus() {this.focused++;},
        closest(selector: string) {
          if (selector === 'button[data-kind]') return btn;
          if (selector === '[data-guide-row]') return row;
          return null;
        },
        getBoundingClientRect() {return {left: spec.left, right: spec.left + spec.width, width: spec.width};},
      };
      return btn;
    });
    return row;
  };

  const chA = {id: 'dup', sourceId: 'src-a', kind: 'live' as const, number: '1', name: 'Source A', group: '', favorite: false, tuneAvailable: true, recordAvailable: true, guide: 'full' as const};
  const chB = {...chA, sourceId: 'src-b', name: 'Source B'};
  const progA1 = {id: 'p-a1', channelId: 'dup', title: 'Show A1', start: 1000, end: 2000};
  const progA2 = {id: 'p-a2', channelId: 'dup', title: 'Show A2', start: 2000, end: 3000};
  const progB1 = {id: 'p-b1', channelId: 'dup', title: 'Show B1', start: 1000, end: 2000};
  const progB2 = {id: 'p-b2', channelId: 'dup', title: 'Show B2', start: 2000, end: 3000};

  const rowA = makeRow(channelKey(chA), [{id: 'p-a1', start: 1000, left: 0, width: 100}, {id: 'p-a2', start: 2000, left: 100, width: 100}]);
  const emptyRow = makeRow('["live","gap","empty"]', []);
  const rowB = makeRow(channelKey(chB), [{id: 'p-b1', start: 1000, left: 0, width: 80}, {id: 'p-b2', start: 2000, left: 120, width: 100}]);
  const container: FakeContainer = {rows: [rowA, emptyRow, rowB], querySelectorAll: () => container.rows};
  for (const row of container.rows) row.parentElement = container;

  const rows = [
    {index: 0, channel: chA, programs: [progA1, progA2], complete: true},
    {index: 1, programs: [], complete: true},
    {index: 2, channel: chB, programs: [progB1, progB2], complete: true},
  ];
  const played: string[] = [], recorded: [string, string | undefined][] = [];
  let nowCount = 0;
  const handlers = {
    rows,
    onPlay: (channel: {name: string}) => {played.push(channel.name);},
    onRecord: (channel: {name: string}, programme?: {id: string}) => {recorded.push([channel.name, programme?.id]);},
    onSelect: () => {},
    onNow: () => {nowCount++;},
  };

  const fire = (target: FakeButton, key: string, mods: Partial<{ctrlKey: boolean; metaKey: boolean; altKey: boolean}> = {}) => {
    let prevented = false;
    handleGuideGridKeyDown({key, ctrlKey: !!mods.ctrlKey, metaKey: !!mods.metaKey, altKey: !!mods.altKey, target: target as unknown as EventTarget, preventDefault: () => {prevented = true;}}, handlers);
    return prevented;
  };

  // Modifier combos must never be intercepted.
  assert.equal(fire(rowA.cells[0]!, 'w', {ctrlKey: true}), false);
  assert.equal(fire(rowA.cells[0]!, 'r', {metaKey: true}), false);
  assert.equal(fire(rowA.cells[0]!, 'n', {ctrlKey: true}), false);
  assert.equal(fire(rowA.cells[0]!, 'ArrowDown', {altKey: true}), false);
  assert.deepEqual(played, []);
  assert.deepEqual(recorded, []);
  assert.equal(nowCount, 0);

  // ArrowDown skips the gap-only row and focuses the horizontally closest cell in rowB.
  assert.equal(fire(rowA.cells[1]!, 'ArrowDown'), true);
  assert.equal(rowB.cells[1]!.focused, 1);
  // ArrowUp returns to rowA's closest cell.
  assert.equal(fire(rowB.cells[1]!, 'ArrowUp'), true);
  assert.equal(rowA.cells[1]!.focused, 1);

  // R and W resolve the exact source-scoped row even with duplicate channel.id ('dup').
  assert.equal(fire(rowB.cells[1]!, 'r'), true);
  assert.deepEqual(recorded, [['Source B', 'p-b2']]);
  assert.equal(fire(rowB.cells[0]!, 'W'), true);
  assert.deepEqual(played, ['Source B']);
  assert.equal(fire(rowA.cells[0]!, 'n'), true);
  assert.equal(nowCount, 1);
});
