import {useLayoutEffect, useRef, type RefObject} from 'react';
import {hasExitPaint, ownArrivalPaint, takeArrivalPaint, type ArrivalPaintLease} from './arrival-paint';
import {easings, motion, reducedMotion} from '@design';
import {useReducedMotion} from '../overlays/shared';

/** The collection's own witness for the answer it is actually showing. */
export type AcceptedAnswer = Readonly<{
  generation: number;
  /** Unknown until a page of the shown generation has arrived. */
  total?: number | undefined;
  /** A newer, non-quiet generation is still loading behind this one. */
  refreshing?: boolean;
  /** The collection's own readiness for the shown generation. */
  status?: string;
}>;

/**
 * An answer is accepted only when the collection reports the shown generation ready: its total is
 * known, no newer answer is loading, and its own status is `ready`. A generation number alone is
 * not enough — `invalidate` bumps it while the new generation's pages are still empty — and a known
 * total is not enough either, since the collection publishes one while other visible pages load.
 */
const accepted = (answer: AcceptedAnswer | undefined): answer is AcceptedAnswer & {total: number} =>
  answer !== undefined && answer.status === 'ready' && answer.total !== undefined && !answer.refreshing;

/** App or system reduction keeps the authored short linear fade, read at the moment it is drawn. */
const reducedNow = (node: HTMLElement, prefersReduced: boolean) => prefersReduced
  || window.matchMedia('(prefers-reduced-motion: reduce)').matches
  || !!node.closest('[data-reduce-motion="true"]')
  || document.documentElement.dataset.reduceMotion === 'true'
  || document.body.dataset.reduceMotion === 'true';

/** motion §3.8: the answer fades in at standard 180, and reduction keeps a 160 linear fade. */
const fade = (node: HTMLElement, fromOpacity: number, reduced: boolean) => node.animate([{opacity: fromOpacity}, {opacity: 1}],
  {duration: reduced ? reducedMotion.durationMs : motion.standard, easing: reduced ? easings.linear : easings.out, fill: 'both'});

/**
 * motion 3.8: when a filter or sort answer lands, the grid and its count cross-fade at 180; nothing
 * fades while an answer is still pending, and the first accepted answer a surface paints is already
 * there. Only the last accepted generation is remembered, and it is recorded only once the answer
 * is accepted, so a pending, failed, loading or still-empty generation never consumes the next
 * arrival.
 * `source` is the collection the answer belongs to: replacing it cancels the running fade and
 * starts over, since a new collection's first answer is immediate even if its numbers repeat.
 */
export function useAcceptedArrival(element: RefObject<HTMLElement | null>, answer: AcceptedAnswer | undefined, source?: unknown,
  select?: (root: HTMLElement) => readonly HTMLElement[]) {
  const prefersReduced = useReducedMotion(element);
  const shown = useRef<{source: unknown; node: HTMLElement; generation: number} | undefined>(undefined);
  type Job = {animation: Animation; lease?: ArrivalPaintLease; reduced: boolean};
  const jobs = useRef(new Map<HTMLElement, Job>());
  const stop = (node: HTMLElement, original: Job) => {
    if (jobs.current.get(node) !== original) return;
    jobs.current.delete(node);original.lease?.release();original.animation.cancel();
  };
  useLayoutEffect(() => {
    const root = element.current;
    const nodes = root?.isConnected ? [...new Set(select ? select(root) : [root])].filter(node => node.isConnected && root.contains(node)) : [];
    for (const [node, job] of jobs.current) if (!node.isConnected || !root?.contains(node) || !job.lease?.current()
      || accepted(answer) && !nodes.includes(node)) stop(node, job);
    const remembered = shown.current;
    if (!root || !remembered || remembered.source !== source || remembered.node !== root) {
      for (const [node, job] of jobs.current) stop(node, job);
      shown.current = root && accepted(answer) ? {source, node: root, generation: answer.generation} : undefined;
      return;
    }
    const draw = (node: HTMLElement, from: number, reduced: boolean, policyChange = false) => {
      const animation = fade(node, from, reduced || policyChange);
      const original: Job = {animation, reduced};
      jobs.current.set(node, original);
      original.lease = ownArrivalPaint(node, source, () => {
        // The taker has removed this lease before invoking its captured callback.
        if (jobs.current.get(node) !== original || element.current !== root || !node.isConnected || !root.contains(node)) return;
        const opacity = Number(getComputedStyle(node).opacity);stop(node, original);return opacity;
      });
      animation.onfinish = () => {
        if (jobs.current.get(node) !== original || !original.lease?.current() || element.current !== root || !node.isConnected || !root.contains(node)) return;
        node.style.opacity = '1';stop(node, original);
      };
    };
    // Retarget only active paint when policy changes. Settled entries never replay.
    for (const [node, job] of [...jobs.current]) {
      const reduced = reducedNow(node, prefersReduced);
      if (reduced !== job.reduced) draw(node, takeArrivalPaint(node, source) ?? Number(getComputedStyle(node).opacity), reduced, true);
    }
    if (!accepted(answer)) return;
    if (remembered.generation === answer.generation) {
      // A newly loaded page can return a key during its previous exit without
      // changing the accepted generation. Restore only that exact exit lease.
      for (const node of nodes) if (hasExitPaint(node, source)) {
        const from = takeArrivalPaint(node, source);if (from !== undefined) draw(node, from, reducedNow(node, prefersReduced));
      }
      return;
    }
    if (!nodes.length) return;
    shown.current = {source, node: root, generation: answer.generation};
    for (const node of nodes) draw(node, takeArrivalPaint(node, source) ?? 0, reducedNow(node, prefersReduced));
  }, [element, answer, source, prefersReduced, select]);
  useLayoutEffect(() => () => {for (const [node, job] of jobs.current) stop(node, job);}, []);
}

/**
 * The applied chips appear under the toolbar with the same 180 fade. Each chip keeps its own
 * identity key, so a retained chip never replays and a removed one simply leaves; this only
 * animates the element it owns, and a later reduction change does not replay it.
 */
export function useAppearanceFade(element: RefObject<HTMLElement | null>) {
  const prefersReduced = useReducedMotion(element);
  const played = useRef(false);
  useLayoutEffect(() => {
    const node = element.current;
    if (!node || played.current) return;
    played.current = true;
    const current = fade(node, 0, reducedNow(node, prefersReduced));
    current.onfinish = () => { node.style.opacity = '1'; current.cancel(); };
    return () => current.cancel();
  }, [element, prefersReduced]);
}
