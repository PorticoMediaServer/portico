// A page's starting focus is asked again after a dropped request, only while that page is the one shown (review desk B, #2908).
import assert from 'node:assert/strict';
import test from 'node:test';
import {createDestinationEntryFocus} from '../src-next/kit/focus/destination-entry.ts';

/** Frames run by hand: each call to step() runs the callbacks asked for on the frame before. */
function frames() {
  let queue: Array<() => void> = [];
  (globalThis as {requestAnimationFrame?: unknown}).requestAnimationFrame = (callback: () => void) => {queue.push(callback); return queue.length;};
  return {step(count = 1) {for (let i = 0; i < count; i++) {const run = queue; queue = []; for (const callback of run) callback();}}};
}
const card = {regionId: 'grid', childId: 'first'};
const entering = {visible: true, intent: 'enter', revision: 1} as const;

test('a page that is covered or left stops asking for its starting focus', () => {
  const clock = frames();
  let asked = 0;
  // The request is dropped: focus stays on the rail item it was on.
  const request = createDestinationEntryFocus({focus: () => {asked++; return true;}, capture: () => ({regionId: 'rail', childId: 'movies', order: []}) as never});
  assert.equal(request(entering, card), true);
  clock.step(3);
  const before = asked;
  // The page is covered (useDestinationEntryFocus calls the request this way when it is not visible).
  request({...entering, visible: false}, undefined);
  clock.step(10);
  assert.equal(asked, before, 'a covered page went on asking for focus');
});

test('with nothing focused when it asked, a move to something the registry does not track is not taken back', () => {
  const clock = frames();
  let asked = 0;
  // Nothing is focused before, and the person then focuses a text field, which the registry does not know:
  // capture() answers undefined both times.
  const request = createDestinationEntryFocus({focus: () => {asked++; return true;}, capture: () => undefined});
  request(entering, card);
  clock.step(30);
  assert.ok(asked <= 2, `asked ${asked} times with no way to see that focus had moved`);
});
