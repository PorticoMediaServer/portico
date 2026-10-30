import {useOverlayPortalTarget} from './portal-target';
import {useRef, type ReactElement, type ReactNode} from 'react';
import {Tooltip as RadixTooltip} from 'radix-ui';
import {classes, useInDialog, usePresence, useSurfaceMotion} from './shared';
import styles from './overlays.module.css';

export type TooltipProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  trigger: ReactElement;
  children: ReactNode;
  description?: ReactNode;
  side?: 'top' | 'bottom' | 'left' | 'right';
  sideOffset?: number;
  className?: string;
  testID?: string;
};

/** Pointer and keyboard hint; focus stays on its trigger. */
export function Tooltip({open, onOpenChange, trigger, children, description, side = 'top', sideOffset = 10, className, testID}: TooltipProps) {
  const portalTarget = useOverlayPortalTarget();
  const triggerElement = useRef<HTMLButtonElement | null>(null), withinDialog = useInDialog(open, triggerElement);
  const present = usePresence(open, 160), motionRef = useSurfaceMotion(open, 'fade');
  return <RadixTooltip.Provider delayDuration={0}><RadixTooltip.Root open={open} onOpenChange={onOpenChange} disableHoverableContent>
    <RadixTooltip.Trigger asChild ref={triggerElement}>{trigger}</RadixTooltip.Trigger>
    {present && <RadixTooltip.Portal forceMount container={portalTarget}><div style={{pointerEvents: 'none'}}><RadixTooltip.Content forceMount ref={motionRef} side={side} sideOffset={sideOffset} collisionPadding={8} className={classes(styles.tooltip, className)} data-within-dialog={withinDialog || undefined} data-state={open ? 'open' : 'closed'} data-testid={testID}>
      {children}{description !== undefined && <small>{description}</small>}
    </RadixTooltip.Content></div></RadixTooltip.Portal>}
  </RadixTooltip.Root></RadixTooltip.Provider>;
}
