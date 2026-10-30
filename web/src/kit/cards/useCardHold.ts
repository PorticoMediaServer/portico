import {useCallback, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent} from 'react';
import {useRetainedLayoutPaint} from '../layout/retained-paint';
import type {CardInteractionAdmission} from './interaction-admission';
import type {CardSelection} from './types';

export type CardHold = Readonly<{x: number; y: number; progress: number}>;
type Entry = {receipt?: CardInteractionAdmission; row: unknown; disabled: boolean; live: boolean};
type Gesture = Readonly<{entry: Entry; selection: CardSelection; anchor: HTMLButtonElement}>;

/** A hold belongs to the original committed receipt and physical node. */
export function useCardHold({selection, disabled = false, row}: {selection?: CardSelection; disabled?: boolean; row: unknown}) {
  const retained = useRetainedLayoutPaint(), inactive = disabled || retained;
  const source = selection?.interaction, receipt = source && selection?.admission?.source === source ? selection.admission : undefined;
  const identity = source ? receipt : selection, physical = source ? receipt?.node : row;
  const eligible = !inactive && (!source || !!receipt?.live);
  const [hold, setHold] = useState<CardHold>(), [entry, setEntry] = useState<Entry | null>(null);
  const committed = useRef<Entry | null>(null), gesture = useRef<Gesture | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined), animation = useRef<number | undefined>(undefined);
  const origin = useRef<{x: number; y: number} | undefined>(undefined);
  const suppressed = useRef<{receipt?: CardInteractionAdmission; row: unknown} | null>(null);
  const stop = useCallback(() => {
    if (timer.current !== undefined) clearTimeout(timer.current);
    if (animation.current !== undefined) cancelAnimationFrame(animation.current);
    timer.current = undefined; animation.current = undefined; origin.current = undefined; gesture.current = null; setHold(undefined);
  }, []);
  useLayoutEffect(() => {
    if (!eligible) {suppressed.current = null; setEntry(null); return;}
    if (suppressed.current && (suppressed.current.receipt !== receipt || suppressed.current.row !== physical)) suppressed.current = null;
    const original: Entry = {receipt, row: physical, disabled: inactive, live: true};
    committed.current = original; setEntry(original);
    const interrupt = () => {if (!original.live || committed.current !== original || (original.receipt && !original.receipt.current())) return; if (!original.live || committed.current !== original) return; if (gesture.current?.entry === original) stop(); if (suppressed.current?.receipt === original.receipt && suppressed.current?.row === original.row) suppressed.current = null;};
    const removeListeners = () => {window.removeEventListener('blur', interrupt); window.removeEventListener('scroll', interrupt, true); document.removeEventListener('visibilitychange', interrupt);};
    const withdraw = (clearSuppression: boolean) => {
      removeListeners();
      if (!original.live) return;
      original.live = false;
      if (gesture.current?.entry === original) stop();
      if (clearSuppression && suppressed.current?.receipt === original.receipt && suppressed.current?.row === original.row) suppressed.current = null;
      if (committed.current === original) committed.current = null;
    };
    window.addEventListener('blur', interrupt); window.addEventListener('scroll', interrupt, true); document.addEventListener('visibilitychange', interrupt);
    const off = original.receipt?.onRetire(() => withdraw(true));
    return () => {off?.(); withdraw(false);};
  }, [identity, physical, eligible, inactive, selection?.active, receipt, stop]);
  const active = eligible && entry?.live && committed.current === entry && entry.receipt === receipt && entry.row === physical ? entry : null;
  const owns = (candidate: Entry | null) => !!candidate && candidate.live && committed.current === candidate
    && (!candidate.receipt || candidate.receipt.current()) && candidate.live && committed.current === candidate;
  const consumeClick = useCallback(() => {
    if (!owns(active) || !active || suppressed.current?.receipt !== active.receipt || suppressed.current?.row !== active.row) return false;
    suppressed.current = null; return true;
  }, [active]);
  const onPointerDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const original = active;
    if (!owns(original) || !original || original.disabled || !event.isPrimary || event.button !== 0) return;
    if (original.receipt && !original.receipt.node.contains(event.currentTarget)) return;
    suppressed.current = null; stop();
    if (event.pointerType === 'mouse' || !selection || selection.active) return;
    const capture: Gesture = {entry: original, selection, anchor: event.currentTarget}; gesture.current = capture;
    const bounds = capture.anchor.parentElement!.getBoundingClientRect(), x = event.clientX - bounds.left, y = event.clientY - bounds.top, started = performance.now();
    origin.current = {x: event.clientX, y: event.clientY};
    const tick = () => {if (!owns(original) || gesture.current !== capture) return; setHold({x, y, progress: Math.min(1, (performance.now() - started) / 500)}); animation.current = requestAnimationFrame(tick);};
    tick();
    timer.current = setTimeout(() => {
      if (!owns(original) || gesture.current !== capture) return;
      stop(); suppressed.current = {receipt: original.receipt, row: original.row};
      capture.selection.onToggle({range: false, source: 'long-press', anchor: capture.anchor});
    }, 500);
  };
  const onPointerMove = (event: ReactPointerEvent<HTMLButtonElement>) => {if (!owns(active)) return; const start = origin.current; if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) > 10) stop();};
  const cancel = () => {if (owns(active)) stop();};
  const handlers = active ? {onPointerDown, onPointerMove, onPointerUp: cancel, onPointerLeave: cancel, onLostPointerCapture: cancel,
    onPointerCancel: () => {if (!owns(active) || !active) return; stop(); if (suppressed.current?.receipt === active.receipt && suppressed.current?.row === active.row) suppressed.current = null;},
    onContextMenu: (event: ReactMouseEvent<HTMLButtonElement>) => {if (owns(active) && active && suppressed.current?.receipt === active.receipt && suppressed.current?.row === active.row) event.preventDefault();},
  } : {};
  return {hold, handlers, cancel, consumeClick};
}
