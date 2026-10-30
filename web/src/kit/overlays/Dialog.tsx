import {useOverlayPortalTarget} from './portal-target';
import {useCallback, useId} from 'react';
import {Dialog as RadixDialog} from 'radix-ui';
import {defaultI18n} from '@i18n';
import {Button} from '../controls/Button';
import {BottomSheet} from './BottomSheet';
import {classes, useDismissReason, useOverlayKeys, usePhone, usePresence, useSurfaceFocus, useSurfaceMotion} from './shared';
import type {OverlayContentSlots, OverlayStyle, OverlaySurfaceProps} from './types';
import styles from './overlays.module.css';

export type DialogProps = OverlaySurfaceProps & OverlayContentSlots & {
  size?: 'sm' | 'md' | 'lg';
  /** A modal edge surface retains the same scrim, focus trap and return-focus lifecycle. */
  placement?: 'center' | 'right';
  width?: number;
  dismissOnOutsidePress?: boolean;
  dismissOnBack?: boolean;
  showCloseButton?: boolean;
  /** The close glyph's size; panels drawn with the larger close glyph pass 22. */
  closeIconSize?: number;
  /** The close button's accessible name when the surface names itself ("Close file info"); default "Close". */
  closeLabel?: string;
  role?: 'dialog' | 'alertdialog';
  /** Phone widths use the real sheet, including gestures, focus and scroll containment. */
  responsive?: boolean;
  /** Phone sheets stack their actions full width, action first (account28/29). Off by default. */
  stackFooter?: boolean;
};

export function Dialog({responsive = true, ...props}: DialogProps) {
  const phone = usePhone();
  if (responsive && phone) return <BottomSheet {...props} fitContent />;
  return <DesktopDialog {...props} />;
}

function DesktopDialog({open, onOpenChange, title, headerAction, children, description, footer, initialFocusRef, returnFocusRef, accessibilityLabel, className, testID, headerClassName, bodyClassName, footerClassName, size = 'md', placement = 'center', width, dismissOnOutsidePress = true, dismissOnBack = true, showCloseButton = true, closeIconSize = 20, closeLabel, role = 'dialog'}: DialogProps) {
  const portalTarget = useOverlayPortalTarget();
  const id = useId(), reason = useDismissReason(), focus = useSurfaceFocus(open, initialFocusRef, returnFocusRef);
  const present = usePresence(open, 160), contentRef = useSurfaceMotion(open, placement === 'right' ? 'panel' : 'dialog'), scrimRef = useSurfaceMotion(open, 'fade');
  const {contentRef: focusContentRef} = focus;
  const attachContent = useCallback((element: HTMLDivElement | null) => {contentRef(element);focusContentRef(element);}, [contentRef, focusContentRef]);
  useOverlayKeys(open, onOpenChange, false, dismissOnBack);
  const change = (value: boolean) => { if (value === open) return; onOpenChange(value, reason.current); reason.current = 'action'; };
  return <RadixDialog.Root open={open} onOpenChange={change} modal>
    {present && <RadixDialog.Portal forceMount container={portalTarget}>
      <RadixDialog.Overlay forceMount ref={scrimRef} className={styles.scrim} data-state={open ? 'open' : 'closed'} />
      {/* A closing surface keeps painting through its fade but takes no input, so nothing in it acts twice. */}
      <RadixDialog.Content forceMount ref={attachContent} inert={!open || undefined} className={classes(styles.dialog, className)} data-size={size} data-placement={placement} data-testid={testID}
        data-state={open ? 'open' : 'closed'} role={role} aria-modal={open || undefined} aria-label={accessibilityLabel} aria-describedby={description === undefined ? undefined : `${id}-description`}
        style={width === undefined ? undefined : {'--overlay-width': `${width}px`} as OverlayStyle}
        onOpenAutoFocus={focus.onOpenAutoFocus} onCloseAutoFocus={focus.onCloseAutoFocus}
        onEscapeKeyDown={event => { if (!dismissOnBack) event.preventDefault(); else reason.current = 'escape'; }}
        onPointerDownOutside={event => { if (!dismissOnOutsidePress) event.preventDefault(); else reason.current = 'outside'; }}
        onInteractOutside={event => { if (!dismissOnOutsidePress) event.preventDefault(); }}>
        <header className={headerClassName}><RadixDialog.Title className={styles.dialogTitle}>{title}</RadixDialog.Title>
        {headerAction}
        {showCloseButton && <Button variant="icon" size="small" iconOnly icon="i-x" iconSize={closeIconSize} aria-label={closeLabel ?? defaultI18n.t('next.overlays.close')} className={styles.dialogClose} onClick={() => onOpenChange(false, 'close-button')} />}</header>
        {description !== undefined && <RadixDialog.Description id={`${id}-description`} className={styles.dialogDescription}>{description}</RadixDialog.Description>}
        <div className={classes(styles.dialogBody, bodyClassName)}>{children}</div>
        {footer !== undefined && <footer className={classes(styles.dialogFooter, footerClassName)}>{footer}</footer>}
      </RadixDialog.Content>
    </RadixDialog.Portal>}
  </RadixDialog.Root>;
}
