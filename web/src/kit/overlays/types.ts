import type {CSSProperties, ReactElement, ReactNode, RefObject} from 'react';

export type OverlayDismissReason = 'close-button' | 'outside' | 'escape' | 'back' | 'action' | 'gesture';
export type OverlayChange = (open: boolean, reason: OverlayDismissReason) => void;
export type OverlayContentSlots = {headerClassName?: string; bodyClassName?: string; footerClassName?: string};
/** Phone sheets may stack their actions full width; the desktop dialog is unchanged. */
export type OverlaySheetFooterProps = {stackFooter?: boolean};
export type OverlaySurfaceProps = {
  open: boolean;
  onOpenChange: OverlayChange;
  title: ReactNode;
  headerAction?: ReactNode;
  children: ReactNode;
  description?: ReactNode;
  footer?: ReactNode;
  initialFocusRef?: RefObject<HTMLElement | null>;
  returnFocusRef?: RefObject<HTMLElement | null>;
  accessibilityLabel?: string;
  className?: string;
  testID?: string;
};
export type AnchoredOverlayProps = Omit<OverlaySurfaceProps, 'footer'> & {
  trigger?: ReactElement;
  anchorRef?: RefObject<HTMLElement | null>;
  modal?: boolean;
  placement?: 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end';
  sideOffset?: number;
  alignOffset?: number;
  width?: number;
};
export type OverlayStyle = CSSProperties & Record<`--overlay-${string}`, string | number>;
