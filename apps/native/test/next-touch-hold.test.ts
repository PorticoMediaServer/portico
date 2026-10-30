import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createTouchHold, type TouchHoldPhase} from '../src-next/kit/browse/touch-hold.ts';

function fixture() {
  let now = 0, next = 0, fires = 0;
  const tasks = new Map<number, {at: number; run: () => void}>(), phases: TouchHoldPhase[] = [];
  const hold = createTouchHold(() => {fires++;}, phase => phases.push(phase), {
    schedule(run, delay) {const id = ++next; tasks.set(id, {at: now + delay, run}); return id;},
    cancel(id) {tasks.delete(id as number);},
  });
  return {hold, tasks, phases, fires: () => fires, advance(ms: number) {
    now += ms;
    for (const [id, task] of [...tasks]) if (task.at <= now) {tasks.delete(id); task.run();}
  }};
}

test('lifting at 499ms leaves one ordinary tap and no pending timer', () => {
  const f = fixture(); f.hold.start({x: 20, y: 30}); f.advance(499); f.hold.end(); f.advance(1);
  assert.equal(f.fires(), 0); assert.equal(f.hold.consumePress(), false); assert.equal(f.tasks.size, 0);
});

test('500ms fires once and consumes only the following activation', () => {
  const f = fixture(); f.hold.start({x: 20, y: 30}); f.advance(500); f.advance(500); f.hold.end();
  assert.equal(f.fires(), 1); assert.deepEqual(f.phases, ['pending', 'fired', 'idle']);
  assert.equal(f.hold.consumePress(), true); assert.equal(f.hold.consumePress(), false);
});

test('10pt is retained; movement greater than 10pt cancels and cannot become a tap', () => {
  const edge = fixture(); edge.hold.start({x: 0, y: 0}); edge.hold.move({x: 6, y: 8}); edge.advance(500);
  assert.equal(edge.fires(), 1);
  const scroll = fixture(); scroll.hold.start({x: 0, y: 0}); scroll.hold.move({x: 6, y: 8.01}); scroll.advance(500); scroll.hold.end();
  assert.equal(scroll.fires(), 0); assert.equal(scroll.hold.consumePress(), true); assert.equal(scroll.tasks.size, 0);
});

test('cancelled or retired callbacks cannot fire a newer gesture early', () => {
  const f = fixture(); f.hold.start({x: 0, y: 0}); const stale = [...f.tasks.values()][0]!.run;
  f.hold.cancel(); f.advance(200); f.hold.start({x: 100, y: 100}); stale();
  assert.equal(f.fires(), 0); f.advance(499); assert.equal(f.fires(), 0); f.advance(1); assert.equal(f.fires(), 1);
  f.hold.start({x: 0, y: 0}); f.hold.dispose(); f.advance(500);
  assert.equal(f.fires(), 1); assert.equal(f.tasks.size, 0);
});
