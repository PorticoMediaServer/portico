import {useOverlayPortalTarget} from './portal-target';
import {useCallback, useId, useLayoutEffect, useRef, useState, type ReactElement, type Ref, type PointerEvent as ReactPointerEvent} from 'react';
import {Dialog as RadixDialog} from 'radix-ui';
import {defaultI18n} from '@i18n';
import {springs} from '@design';
import {Button} from '../controls/Button';
import {releaseSheet, resistedHeight, sheetDetents, springFrames, sheetViewport} from './model';
import {classes, useDismissReason, useOverlayKeys, usePresence, useReducedMotion, useSurfaceFocus} from './shared';
import type {OverlayContentSlots, OverlaySheetFooterProps, OverlayStyle, OverlaySurfaceProps} from './types';
import styles from './overlays.module.css';

export type BottomSheetProps = OverlaySurfaceProps & OverlayContentSlots & OverlaySheetFooterProps & {
  detents?: readonly number[];
  detent?: number;
  onDetentChange?: (index: number) => void;
  /** Short action menus have one measured detent. */
  fitContent?: boolean;
  /** The real scrolling region, for caller-owned restored position. */
  scrollRef?: Ref<HTMLDivElement>;
  trigger?: ReactElement;
  dismissOnOutsidePress?: boolean;
  dismissOnBack?: boolean;
  showCloseButton?: boolean;
  role?: 'dialog' | 'alertdialog';
};
type Drag = {pointer: number; startY: number; startHeight: number; y: number; time: number; velocity: number};

export function BottomSheet({open, onOpenChange, title, headerAction, children, description, footer, initialFocusRef, returnFocusRef, accessibilityLabel, className, testID, headerClassName, bodyClassName, footerClassName, detents, detent, onDetentChange, scrollRef, fitContent = false, trigger, dismissOnOutsidePress = true, dismissOnBack = true, showCloseButton = true, stackFooter = false, role = 'dialog'}: BottomSheetProps) {
  const portalTarget = useOverlayPortalTarget();
  const id = useId(), [viewport, setViewport] = useState(() => typeof window === 'undefined' ? {height: 844, bottom: 0} : sheetViewport(window.innerHeight, window.visualViewport));
  const [active, setActive] = useState(0), [naturalHeight, setNaturalHeight] = useState(0), [element, setElement] = useState<HTMLDivElement | null>(null);
  const [footerHeight, setFooterHeight] = useState(0);
  const body = useRef<HTMLDivElement | null>(null), header = useRef<HTMLDivElement | null>(null), footerRef = useRef<HTMLElement | null>(null), scrim = useRef<HTMLDivElement | null>(null);
  const drag = useRef<Drag | null>(null), height = useRef(0), animations = useRef<Animation[]>([]), closingVelocity = useRef(0), initialized = useRef<HTMLElement | null>(null);
  const triggerElement = useRef<HTMLButtonElement | null>(null);
  const reason = useDismissReason(), reduced = useReducedMotion(), present = usePresence(open, 520), focus = useSurfaceFocus(open, initialFocusRef, returnFocusRef ?? triggerElement);
  const {contentRef: focusContentRef} = focus;
  const attachContent = useCallback((element: HTMLDivElement | null) => {setElement(element);focusContentRef(element);}, [focusContentRef]);
  const limits = sheetDetents(fitContent && naturalHeight ? [naturalHeight] : detents ?? [Math.round(viewport.height / 2), viewport.height - 100], viewport.height - 20);
  const maximum = limits.at(-1)?.height ?? Math.max(1, viewport.height - 100), selected = limits.find(value => value.index === (detent ?? active)) ?? limits[0];
  const hasFooter = footer !== undefined;
  const callbacks = useRef({onOpenChange, onDetentChange}); callbacks.current = {onOpenChange, onDetentChange};
  useOverlayKeys(open, onOpenChange, false, dismissOnBack);
  useLayoutEffect(() => {
    const update = () => setViewport(sheetViewport(window.innerHeight, window.visualViewport));
    window.addEventListener('resize', update); window.visualViewport?.addEventListener('resize', update); window.visualViewport?.addEventListener('scroll', update);
    return () => { window.removeEventListener('resize', update); window.visualViewport?.removeEventListener('resize', update); window.visualViewport?.removeEventListener('scroll', update); };
  }, []);
  useLayoutEffect(() => {
    void hasFooter; // Preserve the authored footer-presence invalidation.
    if (!fitContent || !element) return;
    const measure = () => setNaturalHeight((header.current?.offsetHeight ?? 0) + (body.current?.scrollHeight ?? 0) + (footerRef.current?.offsetHeight ?? 0) + Number.parseFloat(getComputedStyle(element).paddingBottom));
    const observer = new ResizeObserver(measure);
    if (body.current) observer.observe(body.current); if (header.current) observer.observe(header.current);
    // A footer that appears later, or one that grows as its actions wrap, is the
    // last part of the contents' own height.
    if (footerRef.current) observer.observe(footerRef.current);
    measure(); return () => observer.disconnect();
  }, [element, fitContent, footerHeight, hasFooter]);

  // account28/29: an opted-in sheet stacks its actions, so its body clears the
  // footer by the footer's own measured height rather than a fixed guess.
  useLayoutEffect(() => {
    const node = footerRef.current;
    if (!stackFooter || !element || !node) return;
    const measure = () => setFooterHeight(node.offsetHeight);
    const observer = new ResizeObserver(measure);
    observer.observe(node); measure();
    return () => observer.disconnect();
  }, [element, stackFooter]);

  const paintedHeight = useCallback(() => {
    if (!element) return height.current;
    const transform = getComputedStyle(element).transform;
    return maximum - (transform === 'none' ? 0 : new DOMMatrixReadOnly(transform).m42);
  }, [element, maximum]);
  const cancelMotion = useCallback(() => {
    const current = paintedHeight();
    animations.current.forEach(animation => animation.cancel()); animations.current = [];
    height.current = current;
    return current;
  }, [paintedHeight]);
  const writeHeight = useCallback((value: number) => {
    height.current = value;
    const offset = maximum - value;
    if (element) element.style.transform = `translateY(${offset}px)`;
    if (footerRef.current) footerRef.current.style.transform = `translateY(${-offset}px)`;
    if (scrim.current) scrim.current.style.opacity = String(Math.max(0, Math.min(1, value / maximum)));
  }, [element, maximum]);
  const settle = useCallback((target: number, velocityY = 0, closing = false) => {
    if (!element) return;
    const current = cancelMotion(); writeHeight(reduced && closing ? current : target);
    element.style.opacity = reduced && closing ? '0' : '1';
    const duration = reduced ? 160 : closing && !velocityY ? 320 : 500;
    const values = reduced ? [current, target] : velocityY || !closing ? springFrames(current, target, -velocityY, springs.settle, duration) : [current, target];
    const easing = reduced || values.length > 2 ? 'linear' : 'cubic-bezier(.4,0,1,1)';
    const list: Animation[] = [];
    list.push(element.animate(reduced ? [{opacity: closing ? 1 : 0}, {opacity: closing ? 0 : 1}] : values.map(value => ({transform: `translateY(${maximum - value}px)`})), {duration, easing}));
    if (footerRef.current && !reduced) list.push(footerRef.current.animate(values.map(value => ({transform: `translateY(${value - maximum}px)`})), {duration, easing}));
    if (scrim.current) list.push(scrim.current.animate(values.map(value => ({opacity: Math.max(0, Math.min(1, value / maximum))})), {duration, easing}));
    animations.current = list;
  }, [element, maximum, reduced, cancelMotion, writeHeight]);
  useLayoutEffect(() => {
    if (!element) return;
    if (initialized.current !== element) { initialized.current = element; writeHeight(0); }
    if (!drag.current) settle(open ? selected?.height ?? maximum : 0, closingVelocity.current, !open);
    closingVelocity.current = 0;
  }, [element, open, selected?.height, maximum, settle, writeHeight]);
  useLayoutEffect(() => () => animations.current.forEach(animation => animation.cancel()), []);

  const choose = (index: number, velocity = 0) => {
    const target = limits.find(value => value.index === index); if (!target) return;
    setActive(index); callbacks.current.onDetentChange?.(index); settle(target.height, velocity);
  };
  const start = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!open || !dismissOnBack || event.button !== 0 || (event.target as HTMLElement).closest('button,a,input,select,textarea')) return;
    const current = cancelMotion(); writeHeight(current);
    drag.current = {pointer: event.pointerId, startY: event.clientY, startHeight: current, y: event.clientY, time: event.timeStamp, velocity: 0};
    event.currentTarget.setPointerCapture(event.pointerId); event.preventDefault();
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    const current = drag.current; if (!current || current.pointer !== event.pointerId) return;
    const deltaTime = event.timeStamp - current.time;
    if (deltaTime > 0) current.velocity = (event.clientY - current.y) / deltaTime * 1000;
    current.y = event.clientY; current.time = event.timeStamp;
    writeHeight(resistedHeight(current.startHeight - (event.clientY - current.startY), maximum));
  };
  const end = (event: ReactPointerEvent<HTMLDivElement>, cancelled = false) => {
    const current = drag.current; if (!current || current.pointer !== event.pointerId) return;
    drag.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    const velocity = cancelled || event.timeStamp - current.time > 100 ? 0 : current.velocity;
    const target = cancelled ? selected : releaseSheet(height.current, velocity, limits);
    if (!target) { closingVelocity.current = velocity; callbacks.current.onOpenChange(false, 'gesture'); }
    else choose(target.index, velocity);
  };
  const change = (value: boolean) => { if (value === open) return; onOpenChange(value, reason.current); reason.current = 'action'; };
  return <RadixDialog.Root open={open} onOpenChange={change} modal>{trigger && <RadixDialog.Trigger asChild ref={triggerElement}>{trigger}</RadixDialog.Trigger>}{present && <RadixDialog.Portal forceMount container={portalTarget}>
    <RadixDialog.Overlay forceMount ref={scrim} className={styles.sheetScrim} data-state={open ? 'open' : 'closed'} />
    <RadixDialog.Content forceMount ref={attachContent} inert={!open || undefined} className={classes(styles.sheet, className)} role={role} aria-modal={open || undefined} data-testid={testID} data-state={open ? 'open' : 'closed'}
      data-full-height={selected?.height === maximum || undefined} data-has-footer={footer !== undefined || undefined} aria-label={accessibilityLabel} aria-describedby={description === undefined ? undefined : `${id}-description`}
      data-footer-stack={stackFooter || undefined}
      style={{bottom: viewport.bottom, '--overlay-sheet-height': `${maximum}px`, ...(stackFooter && footerHeight ? {'--overlay-sheet-footer-height': `${footerHeight}px`} : {})} as OverlayStyle}
      onOpenAutoFocus={focus.onOpenAutoFocus} onCloseAutoFocus={focus.onCloseAutoFocus}
      onEscapeKeyDown={event => { if (!dismissOnBack) event.preventDefault(); else reason.current = 'escape'; }} onPointerDownOutside={event => { if (!dismissOnOutsidePress) event.preventDefault(); else reason.current = 'outside'; }}>
      <div ref={header} className={classes(styles.sheetHeader, headerClassName)} onPointerDown={start} onPointerMove={move} onPointerUp={event => end(event)} onPointerCancel={event => end(event, true)} onLostPointerCapture={event => { if (drag.current) end(event, true); }}>
        <div className={styles.grabber} role="slider" tabIndex={0} aria-label={defaultI18n.t('next.overlays.resizeSheet')} aria-valuemin={limits[0]?.height ?? 0} aria-valuemax={maximum} aria-valuenow={selected?.height ?? maximum} aria-orientation="vertical"
          onKeyDown={event => { const position = Math.max(0, limits.findIndex(value => value.index === selected?.index)); const next = event.key === 'ArrowUp' ? position + 1 : event.key === 'ArrowDown' ? position - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? limits.length - 1 : undefined; if (next !== undefined) { event.preventDefault(); choose(limits[Math.max(0, Math.min(limits.length - 1, next))]!.index); } }}><span /></div>
        <div className={styles.sheetHeading}><RadixDialog.Title className={styles.sheetTitle}>{title}</RadixDialog.Title>{headerAction}{showCloseButton && <Button variant="text" size="small" className={styles.sheetDone} onClick={() => onOpenChange(false, 'close-button')}>{defaultI18n.t('next.overlays.done')}</Button>}</div>
        {description !== undefined && <RadixDialog.Description id={`${id}-description`} className={styles.sheetDescription}>{description}</RadixDialog.Description>}
      </div>
      <div ref={scrollRef} className={classes(styles.sheetBody, bodyClassName)}><div ref={body}>{children}</div></div>
      {footer !== undefined && <footer ref={footerRef} className={classes(styles.sheetFooter, footerClassName)}>{footer}</footer>}
    </RadixDialog.Content>
  </RadixDialog.Portal>}</RadixDialog.Root>;
}
