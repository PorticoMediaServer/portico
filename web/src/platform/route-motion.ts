import {useLayoutEffect, type RefObject} from 'react';
import {easings, motion, reducedMotion} from '@design';

export function routeFadeTiming(reduce: boolean): {duration: number; easing: string} {
  return {duration: reduce ? reducedMotion.durationMs : motion.standard, easing: reduce ? reducedMotion.easing : easings.out};
}

export type RouteFadeCommits = Readonly<{subscribe: (listener: (entryKey: string) => void) => () => void}>;

/** The router registers its real scroll-restoration handler before consumer subscriptions.
 * This owner starts only after that handler for the original accepted history entry. */
export function bindRouteFade(target: RefObject<HTMLElement | null>, routeKey: string | undefined, commits: RouteFadeCommits): () => void {
  const element = target.current;
  if (!routeKey || !element?.animate || typeof element.ownerDocument.startViewTransition === 'function') return () => {};
  const document = element.ownerDocument, view = document.defaultView;
  if (!view) return () => {};
  let live = true, started = false, animation: Animation | null = null;
  const media = view.matchMedia('(prefers-reduced-motion: reduce)');
  const reduced = () => media.matches || !!element.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true';
  let previousReduced = reduced();
  const current = () => live && target.current === element && element.isConnected;
  const draw = (policyChange = false) => {
    if (!current()) return;
    const reduce = reduced();previousReduced = reduce;
    const opacity = policyChange ? Number(view.getComputedStyle(element).opacity) : 0;
    animation?.cancel();
    const frames = policyChange || reduce
      ? [{opacity, transform: 'none'}, {opacity: 1, transform: 'none'}]
      : [{opacity: 0, transform: 'translateY(4px)'}, {opacity: 1, transform: 'translateY(0)'}];
    const job = element.animate(frames, {...routeFadeTiming(policyChange || reduce), fill: 'both'});animation = job;
    job.onfinish = () => {if (!current() || animation !== job) return;animation = null;job.cancel();};
  };
  const off = commits.subscribe(entryKey => {
    if (!current() || started || entryKey !== routeKey) return;
    started = true;draw();
  });
  const preference = () => {if (animation && previousReduced !== reduced()) draw(true);};
  const observer = new MutationObserver(preference);
  observer.observe(document.documentElement, {subtree: true, attributes: true, attributeFilter: ['data-reduce-motion']});
  media.addEventListener('change', preference);
  return () => {live = false;off();observer.disconnect();media.removeEventListener('change', preference);animation?.cancel();animation = null;};
}

/** View Transitions own supported browsers. The older-browser fallback is the new page's
 * fade and 4 px rise (motion.html 3.1: standard 180, ease-out, as Page.module.css); Reduce
 * motion keeps the fade only. */
export function useRouteFade(target: RefObject<HTMLElement | null>, routeKey: string | undefined, commits: RouteFadeCommits): void {
  useLayoutEffect(() => bindRouteFade(target, routeKey, commits), [target, routeKey, commits]);
}
