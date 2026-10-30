import test from 'node:test';
import assert from 'node:assert/strict';
import {createInputCadence, type CadenceClock} from '../src/screens/browse/input-cadence.ts';

function manualClock() {
 let next = 0; const runs = new Map<number, () => void>(), active = new Set<number>();
 const clock: CadenceClock = {set: run => {const id = ++next; runs.set(id, run); active.add(id); return id;}, clear: handle => {active.delete(handle as number);}};
 return {clock, fire: (id: number) => {if (!active.has(id)) return; active.delete(id); runs.get(id)?.();}, late: (id: number) => runs.get(id)?.(), size: () => active.size};
}
test('rapid typing dispatches only the newest value once',()=>{
 const m = manualClock(), cadence = createInputCadence(250, m.clock), seen: string[] = [];
 cadence.schedule('title', 'a', v => seen.push(v));
 cadence.schedule('title', 'ab', v => seen.push(v));
 cadence.schedule('title', 'abc', v => seen.push(v));
 m.fire(1); m.fire(2); m.fire(3);
 assert.deepEqual(seen, ['abc']);
});
test('a replaced opening or field cancels the retired value',()=>{
 const m = manualClock(), cadence = createInputCadence(250, m.clock), seen: string[] = [];
 cadence.schedule('title', 'old', v => seen.push(v));
 cadence.reset();
 cadence.schedule('artist', 'new', v => seen.push(v));
 m.fire(1); m.fire(2);
 assert.deepEqual(seen, ['new']);
});
test('closing is inert for a late timer and stays inert on reopen',()=>{
 const m = manualClock(), cadence = createInputCadence(250, m.clock), seen: string[] = [];
 cadence.schedule('title', 'late', v => seen.push(v));
 cadence.close();
 m.fire(1);
 cadence.schedule('title', 'after', v => seen.push(v));
 m.fire(2);
 assert.deepEqual(seen, []);
 assert.equal(m.size(), 0);
});

test('a late callback from a replaced value cannot hide the replacement timer',()=>{
 const m = manualClock(), cadence = createInputCadence(250, m.clock), seen: string[] = [];
 cadence.schedule('title', 'old', v => seen.push(v));
 cadence.schedule('title', 'new', v => seen.push(v));
 m.late(1);
 assert.equal(m.size(), 1, 'the replacement timer is still armed');
 assert.equal(cadence.pendingKey(), 'title');
 m.fire(2);
 assert.deepEqual(seen, ['new']);
});

test('typing another field retires the first pending value and draft key',()=>{
 const m = manualClock(), cadence = createInputCadence(250, m.clock), seen: string[] = [];
 cadence.schedule('title', 'title text', v => seen.push(v));
 assert.equal(cadence.pendingKey(), 'title');
 cadence.schedule('artist', 'artist text', v => seen.push(v));
 assert.equal(cadence.pendingKey(), 'artist');
 m.fire(1); m.fire(2);
 assert.deepEqual(seen, ['artist text']);
});
