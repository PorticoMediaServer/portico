// A page's starting focus is asked again after a dropped request, only while that page is the one shown (review desk B, #2908).
import assert from 'node:assert/strict';
import test from 'node:test';
import {createDestinationEntryFocus} from '../src-next/kit/focus/destination-entry.ts';
import {deferStartFocus} from '../src-next/kit/focus/registry.ts';

/** Frames run by hand: each call to step() runs the callbacks asked for on the frame before. */
function frames() {
  let queue: Array<() => void> = [];
  (globalThis as {requestAnimationFrame?: unknown}).requestAnimationFrame = (callback: () => void) => {queue.push(callback); return queue.length;};
  return {step(count = 1) {for (let i = 0; i < count; i++) {const run = queue; queue = []; for (const callback of run) callback();}}};
}
const card = {regionId: 'grid', childId: 'first'};
const entering = {visible: true, intent: 'enter', revision: 1} as const;

test('a page that is covered or left cancels its single next-frame check and asks again when shown', () => {
  const clock = frames();
  let asked = 0;
  // The request is dropped: focus stays on the rail item it was on.
  const request = createDestinationEntryFocus({focus: () => {asked++; return true;}, capture: () => ({regionId: 'rail', childId: 'movies', order: []}) as never});
  assert.equal(request(entering, card), true);
  assert.equal(asked, 1);
  // Covered before the next frame runs (useDestinationEntryFocus calls the request this way when it is not visible).
  request({...entering, visible: false}, undefined);
  clock.step(10);
  assert.equal(asked, 1, 'a covered page cancels its pending next-frame re-ask');
  // Shown again later with the same activation ticket: asks again because the prior ask was stopped before landing.
  assert.equal(request(entering, card), true);
  assert.equal(asked, 2);
});

test('a dropped entry ask checks at most once on the next frame, never looping across 30 frames', () => {
  const clock = frames();
  let asked = 0;
  const request = createDestinationEntryFocus({focus: () => {asked++; return true;}, capture: () => ({regionId: 'rail', childId: 'movies', order: []}) as never});
  assert.equal(request(entering, card), true);
  assert.equal(asked, 1);
  clock.step(1);
  assert.equal(asked, 2, 'one next-frame retry when focus did not move off the rail');
  clock.step(30);
  assert.equal(asked, 2, 'no multi-frame polling loop fighting user input');
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

test('Android TV deferred mode waits until the frame after layer uncover before requesting focus', () => {
  const clock = frames();
  let asked = 0;
  deferStartFocus(true);
  try {
    const request = createDestinationEntryFocus({
      focus: () => {asked++; return true;},
      capture: () => ({regionId: 'rail', childId: 'search', order: []}) as never,
      child: () => ({requestTVFocus() {}}),
      config: () => ({id: 'grid', enabled: true}),
    });
    assert.equal(request(entering, card), true);
    assert.equal(asked, 0, 'never fires synchronously during the uncovering commit on Android TV');
    clock.step(1);
    assert.equal(asked, 1, 'fires on the next frame after layer uncover settles');
    clock.step(1);
    assert.equal(asked, 2, 'at most one follow-up frame check if focus stayed on the rail');
    clock.step(30);
    assert.equal(asked, 2, 'never loops across 30 frames');
  } finally {
    deferStartFocus(false);
  }
});
