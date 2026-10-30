import {useLayoutEffect, useRef, type RefObject} from 'react';
import {easings} from '@design';
import {ownArrivalPaint, takeArrivalPaint, type ArrivalPaintLease} from '../browse/arrival-paint';
import type {LayoutExit} from './retention';

type Job = {node: HTMLElement; root: HTMLDivElement; exit: object; animation: Animation; lease?: ArrivalPaintLease; reduced: boolean};
/** Exact original mounted slots only. Retirement never searches or cancels another animation. */
export function useGridExitPaint<T>(root: RefObject<HTMLDivElement | null>, exits: ReadonlyMap<string, LayoutExit<T>>, source: object, reduced: boolean) {
  const jobs = useRef(new Map<string, Job>());
  const stop = (key: string, original: Job) => {
    if (jobs.current.get(key) !== original) return;
    jobs.current.delete(key);original.lease?.release();original.animation.cancel();
  };
  useLayoutEffect(() => {
    const element = root.current;
    if (!element) {for (const [key, job] of [...jobs.current]) stop(key, job);return;}
    const nodes = new Map([...element?.children ?? []].flatMap(child => {
      const node = child as HTMLElement, key = node.dataset.layoutItem;
      return key !== undefined && node.hasAttribute('data-grid-leaving') ? [[key, node] as const] : [];
    }));
    for (const [key, job] of [...jobs.current]) {
      if (job.root !== element || nodes.get(key) !== job.node || exits.get(key)?.token !== job.exit) {
        // A returning original node lends its exact current opacity to the new arrival
        // before that owner cancels this job. Removed nodes simply retire below.
        if (job.root === element && job.node.isConnected && element.contains(job.node) && !job.node.hasAttribute('data-grid-leaving')) continue;
        stop(key, job);
      }
    }
    const now = performance.now();
    for (const [key, node] of nodes) {
      const exit = exits.get(key);if (!exit) continue;
      const previous = jobs.current.get(key);
      const still = exit.reduced || reduced || window.matchMedia('(prefers-reduced-motion: reduce)').matches
        || !!node.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true';
      if (previous?.node === node && previous.exit === exit.token && previous.reduced === still) continue;
      const from = takeArrivalPaint(node, source) ?? Number(getComputedStyle(node).opacity);
      if (previous && jobs.current.get(key) === previous) stop(key, previous);
      const delay = still ? 0 : Math.max(0, exit.admittedAt + exit.delay - now);
      const duration = Math.max(0, Math.min(still ? 160 : 180, Math.min(exit.retireAt, exit.deadline) - now - delay));
      const animation = node.animate([{opacity: from}, {opacity: 0}], {duration, delay, easing: still ? 'linear' : easings.out, fill: 'both'});
      const original: Job = {node, root: element, exit: exit.token, animation, reduced: still};
      jobs.current.set(key, original);
      original.lease = ownArrivalPaint(node, source, () => {
        if (jobs.current.get(key) !== original || !node.isConnected || root.current !== element) return;
        const opacity = Number(getComputedStyle(node).opacity);stop(key, original);return opacity;
      }, 'exit');
    }
  }, [root, exits, source, reduced]);
  useLayoutEffect(() => () => {for (const [key, job] of [...jobs.current]) stop(key, job);}, [source]);
}
