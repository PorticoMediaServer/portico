import {useEffect, useLayoutEffect, useRef, useState, type RefObject} from 'react';
import {easings, motion, reducedMotion} from '@design';
import {useReducedMotion} from '../overlays/shared';

export type NoticeSlot<T> = Readonly<{key: string; value: T; outgoing: boolean; retained: boolean}>;
export type NoticeState<T> = Readonly<{current?: NoticeSlot<T>; outgoing?: NoticeSlot<T>}>;
export type NoticeTiming = Readonly<{entryMs: number; exitMs: number}>;

/** The authored notice timings: entry 180, exit 120, 160 opacity-only under
 * reduction, and an immediate reduced exit where the surface asks for it. */
export function noticeTiming(reduced: boolean, exit: 'fade' | 'immediate'): NoticeTiming {
  return Object.freeze({entryMs: reduced ? reducedMotion.durationMs : motion.standard,
    exitMs: exit === 'immediate' && reduced ? 0 : reduced ? reducedMotion.durationMs : 120});
}

/** A retained paint continues from exactly what is visible, including a fully
 * painted 1; freshness is carried by the slot, not inferred from opacity. */
export function noticeReverse(fromPainted: number): number {
  return Number.isFinite(fromPainted) ? Math.min(1, Math.max(0, fromPainted)) : 1;
}

/** A fresh entry starts at zero; a retained or retargeted paint resumes from
 * what is actually visible, including a fully painted 1. */
export function noticeEntryFrom(retained: boolean, painted: number): number {
  return retained ? noticeReverse(painted) : 0;
}

/** The initial state is the actual committed identity: a notice already live at
 * mount is current and has no outgoing copy of itself. */
export function noticeInitial<T>(value: T | undefined, keyOf: (value: T) => string): NoticeState<T> {
  return value === undefined ? {} : {current: Object.freeze({key: keyOf(value), value, outgoing: false, retained: false})};
}

/**
 * Slot reconciliation only, and identity-stable: a no-op returns the SAME state
 * object so a consumer render cannot feed a loop. A withdrawal keeps the same
 * keyed node and committed facts for its exit; a replacement retires the older
 * outgoing and moves the current to outgoing.
 */
export function noticeState<T>(state: NoticeState<T>, value: T | undefined, keyOf: (value: T) => string): NoticeState<T> {
  if (value === undefined) {
    if (!state.current) return state;
    return {outgoing: Object.freeze({key: state.current.key, value: state.current.value, outgoing: true, retained: false})};
  }
  const key = keyOf(value);
  if (state.current?.key === key) {
    if (state.current.value === value) return state;
    return {...state, current: Object.freeze({key, value, outgoing: false, retained: state.current.retained})};
  }
  if (!state.current && state.outgoing?.key === key) return {current: Object.freeze({key, value, outgoing: false, retained: true})};
  const outgoing = state.current
    ? Object.freeze({key: state.current.key, value: state.current.value, outgoing: true, retained: false})
    : state.outgoing;
  return {current: Object.freeze({key, value, outgoing: false, retained: false}), ...(outgoing ? {outgoing} : {})};
}

/** A finished exit retires only its captured slot. A successor that reuses the
 * same key (Listening's constant slot) is left untouched. */
export function noticeClearOutgoing<T>(state: NoticeState<T>, slot: NoticeSlot<T>): NoticeState<T> {
  return state.outgoing === slot ? {...state, outgoing: undefined} : state;
}

export type NoticePresence<T> = NoticeState<T> & Readonly<{
  currentRef: RefObject<HTMLDivElement | null>;
  outgoingRef: RefObject<HTMLDivElement | null>;
}>;

type Job = Readonly<{slot: NoticeSlot<unknown>; animation: Animation; duration: number; easing: string}>;

/** A settled entry survives same-key content changes; a retained revival is the
 * distinct painted case. */
const paintToken = (slot: Readonly<{key: string; retained: boolean}>) => `${slot.retained ? 'r' : 'c'}:${slot.key}`;

const reducedNow = (node: HTMLElement | null, prefers: boolean) => {
  if (prefers) return true;
  if (typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return true;
  if (typeof document === 'undefined') return false;
  return !!node?.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true';
};
const painted = (node: HTMLElement) => noticeReverse(Number.parseFloat(getComputedStyle(node).opacity));

/**
 * Visual presence for a passive notice only. The caller keeps eligibility, the
 * original clock, copy and anchor; this hook never starts, extends or ends one.
 * It retains at most the current slot and one silent outgoing slot. Every
 * affected node is sampled before any running job is cancelled, an active job
 * retargets from that sample when the authored timing changes (reduction), a
 * settled paint stays settled, and completion clears only its exact captured
 * slot and job.
 */
export function useNoticePresence<T>(value: T | undefined, keyOf: (value: T) => string, exit: 'fade' | 'immediate' = 'fade'): NoticePresence<T> {
  const [state, setState] = useState<NoticeState<T>>(() => noticeInitial(value, keyOf));
  const currentRef = useRef<HTMLDivElement | null>(null), outgoingRef = useRef<HTMLDivElement | null>(null);
  const entryJob = useRef<Job | null>(null), exitJob = useRef<Job | null>(null), settledEntry = useRef<string | null>(null);
  const keyOfRef = useRef(keyOf);
  keyOfRef.current = keyOf;
  const prefersReduced = useReducedMotion(currentRef);
  useLayoutEffect(() => {setState(previous => noticeState(previous, value, keyOfRef.current));}, [value]);
  useLayoutEffect(() => {
    const current = currentRef.current, outgoing = outgoingRef.current;
    // Sample every affected paint first: cancelling a running job must not reset
    // the opacity the next job, or a retained current, has to continue from.
    const currentPaint = current ? painted(current) : 1, outgoingPaint = outgoing ? painted(outgoing) : 1;
    const reduce = reducedNow(current ?? outgoing, prefersReduced);
    const timing = noticeTiming(reduce, exit), easing = reduce ? easings.linear : easings.out;
    const stop = (job: {current: Job | null}) => {job.current?.animation.cancel();job.current = null;};

    if (outgoing && state.outgoing) {
      const slot = state.outgoing, job = exitJob.current;
      if (!job || job.slot !== slot || job.duration !== timing.exitMs || job.easing !== easing) {
        stop(exitJob);
        if (timing.exitMs === 0) setState(previous => noticeClearOutgoing(previous, slot));
        else {
          const animation = outgoing.animate([{opacity: outgoingPaint}, {opacity: 0}], {duration: timing.exitMs, easing, fill: 'both'});
          exitJob.current = {slot, animation, duration: timing.exitMs, easing};
          animation.onfinish = () => {
            const finished = exitJob.current;
            if (finished && finished.animation === animation && finished.slot === slot) {
              exitJob.current = null;
              setState(previous => noticeClearOutgoing(previous, slot));
            }
          };
        }
      }
    } else stop(exitJob);

    if (current && state.current) {
      const slot = state.current, job = entryJob.current, token = paintToken(slot);
      const same = job !== null && paintToken(job.slot) === token;
      const retarget = job !== null && same && (job.duration !== timing.entryMs || job.easing !== easing);
      const start = retarget || (!same && settledEntry.current !== token);
      if (start) {
        stop(entryJob);
        const from = noticeEntryFrom(retarget || slot.retained, currentPaint);
        const animation = current.animate([{opacity: from}, {opacity: 1}], {duration: timing.entryMs, easing, fill: 'both'});
        entryJob.current = {slot, animation, duration: timing.entryMs, easing};
        animation.onfinish = () => {
          const finished = entryJob.current;
          if (finished && finished.animation === animation && finished.slot === slot) {
            entryJob.current = null;settledEntry.current = token;current.style.opacity = '1';animation.cancel();
          }
        };
      } else if (!same) stop(entryJob);
    } else {stop(entryJob);settledEntry.current = null;}
  }, [state, exit, prefersReduced]);
  useEffect(() => () => {entryJob.current?.animation.cancel();exitJob.current?.animation.cancel();}, []);
  return {...state, currentRef, outgoingRef};
}
