import test from 'node:test';
import assert from 'node:assert/strict';
import {createOrderAutoScroll, orderEdgeVelocity} from '../src/screens/saved/resource-order-scroll.ts';

test('Drag edge velocity follows clipped container bounds and is bounded outside the viewport', () => {
  assert.equal(orderEdgeVelocity(200, 100, 300), 0);
  assert.equal(orderEdgeVelocity(100, 100, 300), -600);
  assert.equal(orderEdgeVelocity(300, 100, 300), 600);
  assert.equal(orderEdgeVelocity(25, 100, 300), 0);
  assert.equal(orderEdgeVelocity(400, 100, 300), 0);
  assert.equal(orderEdgeVelocity(100, 100, 100), 0);
});

test('Held pointer scrolls on animation frames, retargets newly visible rows and cancels cleanly', () => {
  let id = 0, offset = 0, retargets = 0, bounds = {top: 0, bottom: 500};
  const frames = new Map<number, (time: number) => void>();
  const scroll = createOrderAutoScroll({request: callback => {frames.set(++id, callback);return id;}, cancel: id => {frames.delete(id);},
    bounds: () => bounds, scroll: delta => {offset += delta;}, retarget: (x, y) => {assert.equal(x, 20);assert.equal(y, 495);retargets++;}});
  const tick = (time: number) => {const [id, callback] = [...frames][0];frames.delete(id);callback(time);};
  scroll.update(20, 495);scroll.update(20, 495);
  assert.equal(frames.size, 1, 'pointer events never create duplicate loops');
  tick(0);tick(16);tick(32);
  assert.ok(offset > 0);assert.equal(retargets, 3);assert.equal(frames.size, 1);
  bounds = {top: 0, bottom: 1000};tick(48);assert.equal(retargets, 3, 'bounds are read afresh while held');
  scroll.stop();assert.equal(frames.size, 0);
  scroll.stop();assert.equal(frames.size, 0, 'drop/cancel/unmount cleanup is idempotent');
  scroll.update(20, 495);assert.equal(frames.size, 1);scroll.stop();
});


test('Windowing retains exactly the dragged occurrence as scrolling reaches later rows', async () => {
  const {gridRenderIndices} = await import('../src/kit/layout/geometry.ts');
  assert.deepEqual(gridRenderIndices(30, 34, 100, 2), [2, 30, 31, 32, 33, 34]);
  assert.deepEqual(gridRenderIndices(30, 34, 100, 31), [30, 31, 32, 33, 34]);
  assert.deepEqual(gridRenderIndices(30, 34, 100), [30, 31, 32, 33, 34]);
  assert.deepEqual(gridRenderIndices(30, 34, 100, -1), [30, 31, 32, 33, 34]);
});


test('Synchronous cleanup during a scroll frame cannot schedule a replacement frame', () => {
  let callback: ((time: number) => void) | undefined;
  const scroll = createOrderAutoScroll({request: next => {callback = next;return 1;}, cancel: () => {callback = undefined;},
    bounds: () => ({top: 0, bottom: 500}), scroll: () => {}, retarget: () => scroll.stop()});
  scroll.update(10, 499);const tick = callback!;callback = undefined;tick(0);
  assert.equal(callback, undefined);
});
