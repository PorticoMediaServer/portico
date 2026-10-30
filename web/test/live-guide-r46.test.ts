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
const {GUIDE_CELL_INSET, guideCellFacts, guideCellProgress, guideVisibleRows} = await import('../src/screens/live/GuideScreen.tsx');
const {layoutRow} = await import('../../packages/client-core/src/guide/layout.ts');
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
