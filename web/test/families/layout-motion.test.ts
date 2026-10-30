import {test} from 'node:test';
import assert from 'node:assert/strict';
import {scrollLayout, cancelLayoutScroll} from '../../src/kit/layout/motion.ts';

test('holding a pointer interrupts row motion without restoring mandatory snapping until release', t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let now = 0, serial = 0;
  const frames = new Map<number, FrameRequestCallback>();
  const originalRequest = globalThis.requestAnimationFrame, originalCancel = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = callback => { frames.set(++serial, callback); return serial; };
  globalThis.cancelAnimationFrame = id => { frames.delete(id); };
  t.mock.method(performance, 'now', () => now);
  t.after(() => { globalThis.requestAnimationFrame = originalRequest; globalThis.cancelAnimationFrame = originalCancel; });
  class Scroller extends EventTarget {
    style = {scrollSnapType: 'x mandatory'};
    scrollLeft = 0; scrollTop = 0;
    ownerDocument = new EventTarget();
    scrollTo({left = 0, top = 0}: ScrollToOptions) { this.scrollLeft = left; this.scrollTop = top; }
  }
  const scroller = new Scroller(), element = scroller as unknown as HTMLElement;
  const advance = (time: number) => { now = time; const pending = [...frames.values()]; frames.clear(); pending.forEach(callback => callback(time)); };
  scrollLayout(element, 600, 0, false);
  advance(100);
  const interrupted = scroller.scrollLeft;
  assert.ok(interrupted > 0 && interrupted < 600);
  scroller.dispatchEvent(new Event('wheel'));
  scroller.dispatchEvent(new Event('pointerdown'));
  scroller.dispatchEvent(new Event('wheel'));
  t.mock.timers.tick(500);
  advance(500);
  assert.equal(scroller.scrollLeft, interrupted);
  assert.equal(scroller.style.scrollSnapType, 'none');
  scroller.ownerDocument.dispatchEvent(new Event('pointerup'));
  assert.equal(scroller.style.scrollSnapType, 'x mandatory');
  scrollLayout(element, 900, 0, false);
  advance(550);
  const retargeted = scroller.scrollLeft;
  scrollLayout(element, 100, 0, false);
  assert.equal(scroller.scrollLeft, retargeted);
  cancelLayoutScroll(element);
  assert.equal(scroller.style.scrollSnapType, 'x mandatory');
  assert.equal(frames.size, 0);
});
