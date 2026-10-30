import {useLayoutEffect, useRef, useState, type PointerEvent, type FocusEvent, type KeyboardEvent as ReactKeyboardEvent} from 'react';
import {useRetainedLayoutPaint} from '../layout/retained-paint';
import type {CardInteractionAdmission} from './interaction-admission';
import type {CardSelection} from './types';

type Entry = {receipt?: CardInteractionAdmission; legacyPaint?: (enabled: boolean) => void; active: boolean; live: boolean; preview: boolean; detach?: () => void};
const legacyIdentity = Object.freeze({});

/** Paint intent only; original selection authority remains with its producer. */
export function useSelectionRangePreview(selection?: CardSelection, disabled = false) {
  const retained = useRetainedLayoutPaint(), inactive = disabled || retained;
  const source = selection?.interaction, receipt = source && selection?.admission?.source === source ? selection.admission : undefined;
  const identity = source ? receipt : legacyIdentity, eligible = !inactive && (!source || !!receipt?.live);
  const legacyAvailable = !source && !!selection?.onPreview;
  const [hover, setHover] = useState(false), [entry, setEntry] = useState<Entry | null>(null);
  const committed = useRef<Entry | null>(null);
  const legacyPreview = useRef<{paint: (enabled: boolean) => void; shown: boolean} | null>(null);
  // A legacy descriptor may publish a new callback every render; the committed
  // entry keeps the port it was admitted with and the update pass below refreshes it.
  const admittedPreview = useRef(selection?.onPreview);
  useLayoutEffect(() => {admittedPreview.current = selection?.onPreview;});
  useLayoutEffect(() => {
    if (!eligible) {const previous = legacyPreview.current; legacyPreview.current = null; if (previous?.shown) previous.paint(false); setHover(false); setEntry(null); return;}
    if (!source && !selection?.active) {const previous = legacyPreview.current; legacyPreview.current = null; if (previous?.shown) previous.paint(false); setHover(false);}
    const original: Entry = {receipt, legacyPaint: source ? undefined : admittedPreview.current, active: !!selection?.active, live: true, preview: false};
    committed.current = original; setEntry(original);
    const retire = (resetHover: boolean) => {if (!original.live) return; original.live = false; if (committed.current === original) {committed.current = null; if (resetHover) setHover(false);}};
    const off = receipt?.onRetire(() => retire(true));
    return () => {
      off?.();
      const clearOriginal = original.live && original.preview && original.receipt?.live;
      original.detach?.();
      retire(!!receipt);
      // Only the original receipt can clear its own preview; ordinary descriptor
      // publication does not change this effect's identity or run this cleanup.
      if (clearOriginal) original.receipt?.preview(false);
    };
  }, [identity, eligible, inactive, selection?.active, receipt, source]);
  // A legacy descriptor can publish a new callback every render. Update only the
  // committed port, without replacing the admission or its hovered listeners.
  useLayoutEffect(() => {
    const original = committed.current;
    if (!source && original?.live && !original.receipt) original.legacyPaint = selection?.onPreview;
  });
  const current = eligible && entry?.live && committed.current === entry && entry.receipt === receipt ? entry : null;
  const owns = (candidate: Entry | null) => !!candidate && candidate.live && committed.current === candidate
    && (!candidate.receipt || candidate.receipt.current()) && candidate.live && committed.current === candidate;
  const paint = (candidate: Entry | null, value: boolean) => {
    if (!owns(candidate) || !candidate) return;
    candidate.preview = value;
    if (candidate.receipt) candidate.receipt.preview(value); else if (candidate.legacyPaint) {legacyPreview.current = {paint: candidate.legacyPaint, shown: value}; candidate.legacyPaint(value);}
  };
  useLayoutEffect(() => {
    const original = current;
    if (!original || !hover || !original.active || (!original.receipt && !original.legacyPaint)) return;
    const key = (event: KeyboardEvent) => {if (event.key === 'Shift') paint(original, event.type === 'keydown');};
    const interrupt = () => {if (!owns(original)) return; setHover(false); paint(original, false);};
    window.addEventListener('keydown', key); window.addEventListener('keyup', key); window.addEventListener('blur', interrupt);
    window.addEventListener('scroll', interrupt, true); document.addEventListener('visibilitychange', interrupt);
    const detach = () => {window.removeEventListener('keydown', key); window.removeEventListener('keyup', key); window.removeEventListener('blur', interrupt); window.removeEventListener('scroll', interrupt, true); document.removeEventListener('visibilitychange', interrupt); if (original.detach === detach) original.detach = undefined;};
    original.detach = detach;
    const off = original.receipt?.onRetire(detach);
    return () => {off?.(); detach();};
  }, [current, hover, legacyAvailable]);
  const leave = () => {if (!owns(current)) return; setHover(false); paint(current, false);};
  return {
    onPointerEnter: (event: PointerEvent<HTMLElement>) => {if (!owns(current) || !current) return; if (current.receipt && !current.receipt.node.contains(event.currentTarget)) return; setHover(event.pointerType === 'mouse'); paint(current, current.active && event.pointerType === 'mouse' && event.shiftKey);},
    onPointerMove: (event: PointerEvent<HTMLElement>) => {if (!owns(current) || !current) return; if (current.receipt && !current.receipt.node.contains(event.currentTarget)) return; if (event.pointerType === 'mouse') setHover(true); paint(current, current.active && event.pointerType === 'mouse' && event.shiftKey);},
    onPointerLeave: leave, onPointerCancel: leave,
    onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => {if (event.key === 'Shift' && !event.defaultPrevented) paint(current, !!current?.active);},
    onKeyUp: (event: ReactKeyboardEvent<HTMLElement>) => {if (event.key === 'Shift') paint(current, false);},
    onBlur: (event: FocusEvent<HTMLElement>) => {if (!event.currentTarget.contains(event.relatedTarget)) paint(current, false);},
  };
}
