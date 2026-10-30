import {useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject} from 'react';
import {easings, motion, reducedMotion} from '@design';
import type {OverlayChange, OverlayDismissReason} from './types';
import {captureListFocusReturn, type ListFocusReturn} from '../layout/removal-focus-owner';

export const classes = (...values: (string | undefined | false)[]) => values.filter(Boolean).join(' ');
export const focusableSelector = 'button:not([disabled]),[href],input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';
export function focusable(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter(element => !element.closest('[inert],[hidden],[aria-hidden="true"]') && element.getClientRects().length > 0);
}
export function focusElement(element: HTMLElement | null | undefined) { if (element?.isConnected) element.focus({preventScroll: true}); }

/** Portalled child surfaces must paint above the dialog that contains their trigger. */
export function useInDialog(open: boolean, anchor: RefObject<HTMLElement | null>) {
  const [inside, setInside] = useState(false);
  useLayoutEffect(() => { setInside(!!anchor.current?.closest('[role="dialog"],[role="alertdialog"]')); }, [open, anchor]);
  return inside;
}

export function usePhone() {
  const [phone, setPhone] = useState(() => typeof window !== 'undefined' && window.matchMedia('(max-width: 599px)').matches);
  useEffect(() => {
    const media = window.matchMedia('(max-width: 599px)'), update = () => setPhone(media.matches);
    media.addEventListener('change', update); update();
    return () => media.removeEventListener('change', update);
  }, []);
  return phone;
}

export function useReducedMotion(elementRef?: RefObject<HTMLElement | null>) {
  const [reduced, setReduced] = useState(false);
  useLayoutEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(media.matches || !!(elementRef?.current ?? document.documentElement).closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true');
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {attributes: true, subtree: true, attributeFilter: ['data-reduce-motion']});
    media.addEventListener('change', update); update();
    return () => { observer.disconnect(); media.removeEventListener('change', update); };
  }, [elementRef]);
  return reduced;
}

const stack: symbol[] = [];
/** Back/Menu keys dismiss only the highest overlay. Escape stays with Radix except for SidePanel. */
export function useOverlayKeys(open: boolean, onOpenChange: OverlayChange, escape = false, dismissOnBack = true) {
  const callback = useRef(onOpenChange); callback.current = onOpenChange;
  const identity = useRef(Symbol());
  useLayoutEffect(() => {
    if (!open) return;
    const id = identity.current; stack.push(id);
    const keydown = (event: KeyboardEvent) => {
      if (stack.at(-1) !== id || event.defaultPrevented) return;
      const isEscape = escape && event.key === 'Escape';
      const isBack = ['BrowserBack', 'GoBack', 'Back', 'Menu', 'ContextMenu'].includes(event.key);
      if (!isEscape && !isBack) return;
      event.preventDefault(); event.stopPropagation();
      if (isEscape || dismissOnBack) callback.current(false, isEscape ? 'escape' : 'back');
    };
    document.addEventListener('keydown', keydown);
    return () => { const index = stack.indexOf(id); if (index >= 0) stack.splice(index, 1); document.removeEventListener('keydown', keydown); };
  }, [open, escape, dismissOnBack]);
  return () => stack.at(-1) === identity.current;
}

export function useSurfaceFocus(open: boolean, initialFocusRef?: RefObject<HTMLElement | null>, returnFocusRef?: RefObject<HTMLElement | null>) {
  const opener = useRef<HTMLElement | null>(null);
  const returnTarget = useRef(returnFocusRef);
  useLayoutEffect(() => {returnTarget.current = returnFocusRef;}, [returnFocusRef]);
  const suppressRestore = useRef(false);
  type Opening = {target: HTMLElement | null; list?: ListFocusReturn; content: HTMLElement | null};
  const [opening, setOpening] = useState<Opening | null>(null);
  const current = useRef<Opening | null>(null);
  const content = useRef<HTMLElement | null>(null);
  const contentRef = useCallback((element: HTMLElement | null) => {
    const record = current.current;
    if (!element && record?.content === content.current) record?.list?.retireContent();
    content.current = element;
    // A Portal can mount after its owner's layout setup. Publish its first actual node only;
    // a later replacement node cannot inherit the previous opening's return receipt.
    if (element && record && (!record.content || record.content === element)) {record.content = element;record.list?.attach(element);}
  }, []);
  useLayoutEffect(() => {
    if (!open) return;
    current.current?.list?.dispose();
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const target = returnTarget.current?.current ?? opener.current;
    const record: Opening = {target, list: captureListFocusReturn(target), content: content.current};
    current.current = record;suppressRestore.current = false;setOpening(record);
    if (record.content) record.list?.attach(record.content);
    return () => {record.list?.detach();};
    // Refs are inputs to this original opening, not permission to adopt another return target.
  }, [open]);
  return {
    suppressRestore, contentRef,
    onOpenAutoFocus(event: Event) {
      const record = current.current;
      if (!record || record.content !== event.target) {event.preventDefault();return;}
      const initial = initialFocusRef?.current;
      if (initial && record.content?.contains(initial)) { event.preventDefault(); focusElement(initial); }
    },
    onCloseAutoFocus(event: Event) {
      event.preventDefault();
      if (!opening || current.current !== opening || opening.content !== event.target) return;
      if (!suppressRestore.current) {
        if (opening.list) opening.list.restore();
        else focusElement(opening.target);
      }
      opening.list?.dispose();
    },
  };
}

const anchoredOpenEvent = 'portico:anchored-overlay-open';
export function useExclusiveAnchor(open: boolean, change: OverlayChange) {
  const callback = useRef(change); callback.current = change;
  const [superseded, setSuperseded] = useState(false);
  useLayoutEffect(() => {
    if (!open) return;
    setSuperseded(false);
    const id = Symbol();
    document.dispatchEvent(new CustomEvent(anchoredOpenEvent, {detail: id}));
    const receive = (event: Event) => { if ((event as CustomEvent<symbol>).detail !== id) { setSuperseded(true); callback.current(false, 'action'); } };
    document.addEventListener(anchoredOpenEvent, receive);
    return () => document.removeEventListener(anchoredOpenEvent, receive);
  }, [open]);
  return superseded;
}

export function useDismissReason() {
  const reason = useRef<OverlayDismissReason>('action');
  return reason;
}

export function usePresence(open: boolean, milliseconds: number) {
  const [held, setHeld] = useState(open);
  useEffect(() => {
    if (open) { setHeld(true); return; }
    const timer = window.setTimeout(() => setHeld(false), milliseconds);
    return () => window.clearTimeout(timer);
  }, [open, milliseconds]);
  return open || held;
}

/** The last non-null value, so a surface can keep its content on screen while it plays its exit. */
export function useRetained<T>(value: T | null | undefined): T | undefined {
  const last = useRef<T | undefined>(undefined);
  if (value !== null && value !== undefined) last.current = value;
  return last.current;
}

/** Cancel and retarget from the painted value, including a reversal during entry. */
export function useSurfaceMotion(open: boolean, kind: 'dialog' | 'menu' | 'panel' | 'fade' | 'toast' | 'banner', exitTransform?: RefObject<string | null>) {
  const [element, setElement] = useState<HTMLElement | null>(null);
  const animation = useRef<Animation | null>(null);
  const motionElement = useRef<HTMLElement | null>(null); motionElement.current = element;
  const prefersReduced = useReducedMotion(motionElement);
  const first = useRef(true);
  const previousElement = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (!element) return;
    if (previousElement.current !== element) { first.current = true; previousElement.current = element; }
    const reduced = prefersReduced || window.matchMedia('(prefers-reduced-motion: reduce)').matches || !!element.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true';
    const style = getComputedStyle(element), entering = open;
    const initialTransform = kind === 'dialog' ? 'scale(.98)' : kind === 'menu' ? 'translateY(-4px) scale(.98)' : kind === 'panel' ? 'translateX(24px)' : kind === 'toast' ? 'translateY(24px)' : 'none';
    // A menu enters from the side the content currently reports. Radix writes data-side when the
    // positioner resolves, so the first frame may still carry the requested side; the observation
    // below retargets the entrance if the resolved pose arrives different.
    const menuEntry = () => element.dataset.side === 'top' ? 'translateY(4px) scale(.98)' : 'translateY(-4px) scale(.98)';
    const entryTransform = kind === 'menu' ? menuEntry() : initialTransform;
    const from = {opacity: first.current && entering ? 0 : Number(style.opacity), transform: reduced ? 'none' : first.current && entering ? entryTransform : style.transform};
    animation.current?.cancel(); first.current = false;
    // Menus and side panels leave from their settled pose on opacity alone. A caller that
    // publishes its own exit target (toasts, banners) still wins; the rest keep their painted one.
    const targetTransform = reduced || entering ? 'none' : exitTransform?.current ?? from.transform;
    const current = element.animate([from, {opacity: entering ? 1 : 0, transform: targetTransform}], {duration: reduced ? reducedMotion.durationMs : entering ? motion.standard : motion.quick, easing: reduced ? reducedMotion.easing : entering ? easings.out : easings.in, fill: 'both'});
    animation.current = current;
    // The entry pose is normalised to the two poses a menu can actually occupy: a missing
    // data-side is the default bottom pose, so the first resolution to bottom is not a change.
    const pose = () => element.dataset.side === 'top' ? 'top' : 'bottom';
    const entryPose = pose();
    let sideObserver: MutationObserver | undefined;
    current.onfinish = () => {
      if (animation.current !== current) return;
      sideObserver?.disconnect();
      element.style.opacity = entering ? '1' : '0'; element.style.transform = targetTransform;
      current.cancel(); animation.current = null;
    };
    // Only a menu whose resolved pose arrives after its first frame is retargeted, and only when
    // the pose really changed; nothing here infers a side from the viewport, and focus and pointer
    // behaviour are untouched. The observation ends with the entry, the retarget or the cleanup.
    if (kind === 'menu' && entering) {
      sideObserver = new MutationObserver(() => {
        if (animation.current !== current || pose() === entryPose) return;
        sideObserver?.disconnect();
        // Read what is actually painted before cancelling, so the late flip continues from the
        // current frame instead of restarting from transparent.
        const painted = getComputedStyle(element);
        const fromPose = {opacity: Number(painted.opacity), transform: reduced ? 'none' : painted.transform};
        current.cancel();
        const replay = element.animate([fromPose, {opacity: 1, transform: 'none'}],
          {duration: reduced ? reducedMotion.durationMs : motion.standard, easing: reduced ? reducedMotion.easing : easings.out, fill: 'both'});
        animation.current = replay;
        replay.onfinish = () => {
          if (animation.current !== replay) return;
          element.style.opacity = '1'; element.style.transform = 'none'; replay.cancel(); animation.current = null;
        };
      });
      sideObserver.observe(element, {attributes: true, attributeFilter: ['data-side']});
    }
    return () => {sideObserver?.disconnect(); /* The next run reads the painted value before cancelling. */};
  }, [element, open, kind, exitTransform, prefersReduced]);
  useEffect(() => () => animation.current?.cancel(), []);
  return setElement;
}
