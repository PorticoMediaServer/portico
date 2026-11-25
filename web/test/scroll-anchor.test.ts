import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';

let effectRunner: (() => void | (() => void)) | undefined;
const hookKey = '__porticoScrollAnchorHooks';
(globalThis as Record<string, unknown>)[hookKey] = {
  useLayoutEffect: (fn: () => void | (() => void)) => { effectRunner = fn; },
};
const hookModule = 'data:text/javascript,' + encodeURIComponent(`export const useLayoutEffect = fn => globalThis.${hookKey}.useLayoutEffect(fn);`);
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('/kit/theme/scroll-anchor.ts')) {
    return {format: 'module-typescript', shortCircuit: true,
      source: readFileSync(new URL(url), 'utf8').replace("from 'react'", `from ${JSON.stringify(hookModule)}`)};
  }
  return next(url, context);
}});
const {useWindowScrollAnchor} = await import('../src/kit/theme/scroll-anchor.ts');
test.after(() => { loader.deregister(); delete (globalThis as Record<string, unknown>)[hookKey]; });

test('scroll events coalesce into a single rAF measurement and clean up on resize/unmount', () => {
  const prevWindow = globalThis.window, prevDocument = globalThis.document;
  const prevRaf = globalThis.requestAnimationFrame, prevCancelRaf = globalThis.cancelAnimationFrame;
  try {
    const listeners = new Map<string, Set<() => void>>();
    const rafQueue = new Map<number, FrameRequestCallback>();
    let nextRafId = 1, queryCalls = 0, rectCalls = 0, scrollDelta = 0, cardTop = 120;
    const card = {
      isConnected: true,
      getBoundingClientRect: () => { rectCalls++; return {top: cardTop, bottom: cardTop + 200, left: 10, right: 210}; },
    };
    const emit = (type: string) => { for (const fn of [...(listeners.get(type) ?? [])]) fn(); };
    const flushRaf = () => {
      const batch = [...rafQueue.entries()];
      rafQueue.clear();
      for (const [, cb] of batch) cb(performance.now());
    };
    (globalThis as Record<string, unknown>).document = {
      querySelectorAll: () => { queryCalls++; return [card]; },
    };
    (globalThis as Record<string, unknown>).window = {
      innerHeight: 900,
      innerWidth: 1200,
      scrollY: 300,
      scrollBy: ({top}: {top: number}) => { scrollDelta += top; },
      addEventListener: (type: string, fn: () => void) => {
        if (!listeners.has(type)) listeners.set(type, new Set());
        listeners.get(type)!.add(fn);
      },
      removeEventListener: (type: string, fn: () => void) => { listeners.get(type)?.delete(fn); },
    };
    (globalThis as Record<string, unknown>).requestAnimationFrame = (cb: FrameRequestCallback) => {
      const id = nextRafId++;
      rafQueue.set(id, cb);
      return id;
    };
    (globalThis as Record<string, unknown>).cancelAnimationFrame = (id: number) => { rafQueue.delete(id); };

    useWindowScrollAnchor();
    assert.ok(effectRunner);
    const cleanup = effectRunner();
    assert.equal(queryCalls, 1, 'initial mount captures the anchor once');
    assert.equal(rectCalls, 1, 'single getBoundingClientRect call per matched card');

    // Burst of 20 scroll events in one frame must do zero synchronous DOM reads and schedule 1 rAF.
    for (let i = 0; i < 20; i++) emit('scroll');
    assert.equal(queryCalls, 1, 'no synchronous querySelectorAll during scroll burst');
    assert.equal(rectCalls, 1, 'no synchronous getBoundingClientRect during scroll burst');
    assert.equal(rafQueue.size, 1, 'coalesced to a single animation frame');

    cardTop = 140;
    flushRaf();
    assert.equal(queryCalls, 2, 'measured once when rAF fires');
    assert.equal(rectCalls, 2);

    // Resize cancels pending scroll rAF and compensates anchor drift once the resize frame settles.
    emit('scroll');
    assert.equal(rafQueue.size, 1);
    cardTop = 185;
    emit('resize');
    assert.equal(rafQueue.size, 1, 'resize replaces pending scroll rAF with resize rAF');
    flushRaf();
    assert.equal(scrollDelta, 45, 'restored scroll drift relative to remembered anchor');

    // Unmount cancels queued rAFs and removes window listeners.
    emit('scroll');
    assert.equal(rafQueue.size, 1);
    cleanup?.();
    assert.equal(rafQueue.size, 0, 'pending rAF cancelled on unmount');
    assert.equal(listeners.get('scroll')?.size ?? 0, 0);
    assert.equal(listeners.get('resize')?.size ?? 0, 0);
  } finally {
    (globalThis as Record<string, unknown>).window = prevWindow;
    (globalThis as Record<string, unknown>).document = prevDocument;
    (globalThis as Record<string, unknown>).requestAnimationFrame = prevRaf;
    (globalThis as Record<string, unknown>).cancelAnimationFrame = prevCancelRaf;
  }
});
