import {useEffect, useState} from 'react';
import {motion} from '@design';

/** System and in-app preferences both pause rotation, including preference changes in another shell. */
export function useLayoutMotion(reduce = false) {
  const [environment, setEnvironment] = useState({reduced: false, visible: true});
  useEffect(() => {
    const media = matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setEnvironment({reduced: media.matches || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true', visible: document.visibilityState !== 'hidden'});
    update();
    media.addEventListener('change', update);
    document.addEventListener('visibilitychange', update);
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {attributes: true, attributeFilter: ['data-reduce-motion']});
    observer.observe(document.body, {attributes: true, attributeFilter: ['data-reduce-motion']});
    return () => { media.removeEventListener('change', update); document.removeEventListener('visibilitychange', update); observer.disconnect(); };
  }, []);
  return {reduced: reduce || environment.reduced, visible: environment.visible};
}

/** CSS ease-in-out(.4,0,.2,1), inverted by bisection for scroll offsets. */
export function rowEase(progress: number): number {
  let lo = 0, hi = 1;
  for (let i = 0; i < 16; i++) {
    const t = (lo + hi) / 2, x = 3 * (1 - t) ** 2 * t * .4 + 3 * (1 - t) * t ** 2 * .2 + t ** 3;
    if (x < progress) lo = t; else hi = t;
  }
  const t = (lo + hi) / 2;
  return 3 * (1 - t) * t ** 2 + t ** 3;
}

/** One animation per scroller. New input retargets from the current offset. */
const scrolling = new WeakMap<HTMLElement, () => void>();
export function scrollLayout(element: HTMLElement, left: number, top: number, reduced: boolean, repeat = false): () => void {
  scrolling.get(element)?.();
  if (reduced) { element.scrollTo({left, top, behavior: 'instant'}); return () => {}; }
  const fromX = element.scrollLeft, fromY = element.scrollTop, started = performance.now();
  const snap = element.style.scrollSnapType; element.style.scrollSnapType = 'none';
  let frame = 0, cancelled = false, wheelTimer: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => {
    cancelled = true; cancelAnimationFrame(frame); element.style.scrollSnapType = snap;
    if (wheelTimer !== undefined) clearTimeout(wheelTimer);
    element.removeEventListener('pointerdown', interrupt); element.removeEventListener('wheel', interrupt);
    element.ownerDocument.removeEventListener('pointerup', cancel); element.ownerDocument.removeEventListener('pointercancel', cancel);
    if (scrolling.get(element) === cancel) scrolling.delete(element);
  };
  const interrupt = (event: Event) => {
    cancelled = true; cancelAnimationFrame(frame);
    // Restoring mandatory snapping on pointer-down would continue moving the row
    // underneath the held pointer. Let the user's gesture finish first.
    if (event.type === 'pointerdown') {
      if (wheelTimer !== undefined) clearTimeout(wheelTimer);
      element.removeEventListener('wheel', interrupt);
      element.ownerDocument.addEventListener('pointerup', cancel, {once: true});
      element.ownerDocument.addEventListener('pointercancel', cancel, {once: true});
    } else {
      if (wheelTimer !== undefined) clearTimeout(wheelTimer);
      wheelTimer = setTimeout(cancel, motion.quick);
    }
  };
  scrolling.set(element, cancel);
  element.addEventListener('pointerdown', interrupt, {once: true, passive: true}); element.addEventListener('wheel', interrupt, {passive: true});
  const tick = (now: number) => {
    if (cancelled) return;
    const p = Math.min(1, (now - started) / (repeat ? motion.quick : motion.slow)), eased = repeat ? p : rowEase(p);
    element.scrollTo({left: fromX + (left - fromX) * eased, top: fromY + (top - fromY) * eased, behavior: 'instant'});
    if (p < 1) frame = requestAnimationFrame(tick); else cancel();
  };
  frame = requestAnimationFrame(tick);
  return cancel;
}

export function cancelLayoutScroll(element: HTMLElement) { scrolling.get(element)?.(); }
