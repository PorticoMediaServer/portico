import {useOverlayPortalTarget} from './portal-target';
import {useCallback, useId, useLayoutEffect, useRef} from 'react';
import {createPortal} from 'react-dom';
import {defaultI18n} from '@i18n';
import {Button} from '../controls/Button';
import {classes, focusElement, focusable, useOverlayKeys, usePresence, useSurfaceMotion} from './shared';
import type {OverlayStyle, OverlaySurfaceProps} from './types';
import styles from './overlays.module.css';

export type SidePanelProps = OverlaySurfaceProps & {topInset?: number; width?: number; closeOnOutsidePress?: boolean};

/** A separate non-modal region: no inert siblings, scroll lock, scrim, or focus loop. */
export function SidePanel({open, onOpenChange, title, children, description, footer, initialFocusRef, returnFocusRef, accessibilityLabel, className, testID, topInset = 76, width = 520, closeOnOutsidePress = false}: SidePanelProps) {
  const portalTarget = useOverlayPortalTarget();
  const id = useId(), panel = useRef<HTMLElement | null>(null), opener = useRef<HTMLElement | null>(null), outside = useRef(false);
  const motionRef = useSurfaceMotion(open, 'panel'), present = usePresence(open, 160);
  const isTop = useOverlayKeys(open, onOpenChange, true);
  const configuration = useRef({onOpenChange, initialFocusRef, returnFocusRef, closeOnOutsidePress, isTop}); configuration.current = {onOpenChange, initialFocusRef, returnFocusRef, closeOnOutsidePress, isTop};
  const ref = useCallback((element: HTMLElement | null) => { panel.current = element; motionRef(element); }, [motionRef]);
  useLayoutEffect(() => {
    if (!open) return;
    const surface = panel.current;
    outside.current = false; opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const returnTarget = configuration.current.returnFocusRef?.current ?? opener.current;
    focusElement(configuration.current.initialFocusRef?.current ?? (panel.current ? focusable(panel.current)[0] ?? panel.current : null));
    const pointer = (event: PointerEvent) => {
      if (!configuration.current.isTop() || !configuration.current.closeOnOutsidePress || panel.current?.contains(event.target as Node)) return;
      outside.current = true; configuration.current.onOpenChange(false, 'outside');
    };
    document.addEventListener('pointerdown', pointer);
    return () => {
      document.removeEventListener('pointerdown', pointer);
      if (!outside.current && (document.activeElement === document.body || surface?.contains(document.activeElement))) focusElement(returnTarget);
    };
  }, [open]);
  if (!present || !portalTarget) return null;
  return createPortal(<aside ref={ref} className={classes(styles.sidePanel, className)} role="region" tabIndex={-1}
    aria-label={accessibilityLabel} aria-labelledby={accessibilityLabel ? undefined : `${id}-title`} aria-describedby={description === undefined ? undefined : `${id}-description`}
    data-state={open ? 'open' : 'closed'} data-testid={testID} style={{'--overlay-top': `${topInset}px`, '--overlay-width': `${width}px`} as OverlayStyle}>
    <header className={styles.panelHeader}><div><h2 id={`${id}-title`} className={styles.panelTitle}>{title}</h2>{description !== undefined && <div id={`${id}-description`} className={styles.panelDescription}>{description}</div>}</div>
      <Button variant="icon" size="small" iconOnly icon="i-x" iconSize={22} aria-label={defaultI18n.t('next.overlays.close')} className={styles.panelClose} onClick={() => onOpenChange(false, 'close-button')} /></header>
    <div className={styles.panelBody}>{children}</div>
    {footer !== undefined && <footer className={styles.panelFooter}>{footer}</footer>}
  </aside>, portalTarget);
}
