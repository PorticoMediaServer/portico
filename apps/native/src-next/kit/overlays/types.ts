import type React from 'react';
import type {PressableProps, StyleProp, TextStyle, View, ViewStyle} from 'react-native';
import type {TypeStyle} from '@portico/design';
import type {IconName} from '../Icon';

export type OverlayReason = 'close-button' | 'outside' | 'escape' | 'back' | 'action' | 'gesture';
export type FocusTarget = {focus?: () => void; requestTVFocus?: () => void};
export type FocusRef = React.RefObject<FocusTarget | null>;
export type SurfaceProps = {
  open: boolean;
  onOpenChange: (open: boolean, reason: OverlayReason) => void;
  title: React.ReactNode;
  headerContext?: React.ReactNode;
  description?: React.ReactNode;
  children: React.ReactNode;
  footer?: React.ReactNode;
  initialFocusRef?: FocusRef;
  returnFocusRef?: FocusRef;
  /** Opt out only when the caller owns original-route focus restoration. */
  restoreFocus?: boolean;
  accessibilityLabel?: string;
  testID?: string;
  style?: StyleProp<ViewStyle>;
  contentStyle?: StyleProp<ViewStyle>;
  /** Replaces the title's complete type style, including normal line-height handling. */
  titleType?: TypeStyle;
  titleTextStyle?: StyleProp<TextStyle>;
  descriptionTextStyle?: StyleProp<TextStyle>;
  /** BottomSheet backdrop alpha; defaults to .64 unless a composition opts in. */
  backdropOpacity?: number;
};
export type DialogProps = SurfaceProps & {
  /** Disable only when the body owns its scrolling container (for example Select's FlatList). */
  scrollable?: boolean;
  size?: 'sm' | 'md' | 'lg';
  width?: number;
  placement?: 'center' | 'side';
  dismissOnOutsidePress?: boolean;
  dismissOnBack?: boolean;
  dismissOnGesture?: boolean;
  showClose?: boolean;
  /** Flat face colour; the drawn gradient face is replaced when supplied. */
  faceColor?: string;
  /** Complete scrim colour including its alpha; the platform default is used when absent. */
  scrimColor?: string;
  /** Space between the description and the footer; the platform default is used when absent. */
  footerMarginTop?: number;
};
export type SidePanelProps = SurfaceProps & {
  topInset?: number;
  width?: number;
  closeOnOutsidePress?: boolean;
  dismissOnBack?: boolean;
  showClose?: boolean;
  /** TV-only authored canvas; style applies inside its uniformly scaled physical portal. */
  logicalCanvas?: {width: number; height: number};
};
export type BottomSheetProps = SurfaceProps & {
  /** A shell-root caller can retain the same content in its own window. */
  host?: 'modal' | 'inline';
  /** Inline Settings becomes an ordinary pane at wider widths without reparenting its edits. */
  presentation?: 'sheet' | 'page';
  /** Called once the closing surface has finished its exit and left the tree. */
  onExited?: () => void;
  /** A custom header can draw its own grabber within its authored height. */
  showGrabber?: boolean;
  /** Visible heights in points, sorted by the component. The controlled detent is an index. */
  detents?: readonly number[];
  width?: number;
  detent?: number;
  defaultDetent?: number;
  onDetentChange?: (index: number) => void;
  dismissOnOutsidePress?: boolean;
  dismissOnBack?: boolean;
  dismissOnGesture?: boolean;
  scrollable?: boolean;
  showClose?: boolean;
  fitContent?: boolean;
  /** Content clearance above a fixed footer; defaults to 16 points. */
  contentFooterGap?: number;
  /** Text-entry sheets move content and footer together above the native keyboard. */
  keyboardAvoiding?: boolean;
  /** Authored footer padding; bottom includes the actual safe area minimum. */
  footerPadding?: {top: number; bottom: number; keyboardBottom: number};
  /** Footer divider is present unless the authored surface omits it. */
  showFooterSeparator?: boolean;
  headerAction?: React.ReactNode;
  /** Replaces only the standard heading row; context and sheet gestures remain. */
  headerContent?: React.ReactNode;
};
export type PopoverProps = SurfaceProps & {
  anchorRef: React.RefObject<View | null>;
  width?: number;
  modal?: boolean;
  /** Native focus containment can hold an in-window TV menu independently of window modality. */
  holdFocus?: boolean;
  showTitle?: boolean;
  backdropOpacity?: number;
  placement?: 'bottom-start' | 'bottom-center' | 'bottom-end' | 'top-start' | 'top-center' | 'top-end' | 'right-end';
  offset?: number;
  /** Cross-axis adjustment in the authored surface's points; positive moves down. */
  alignOffset?: number;
  dismissOnOutsidePress?: boolean;
};
export type MenuAction = {
  id: string;
  label: React.ReactNode;
  icon?: IconName;
  description?: React.ReactNode;
  trailing?: React.ReactNode;
  selected?: boolean;
  disabled?: boolean;
  destructive?: boolean;
  tone?: 'default' | 'owner';
  /** A nested picker can replace the menu without closing it. */
  closeOnSelect?: boolean;
  onSelect: () => void;
};
export type MenuEntry = MenuAction | {id: string; separator: true} | {id: string; section: true; label: React.ReactNode; tone?: 'default' | 'owner'};
export type MenuProps = Omit<SurfaceProps, 'children' | 'headerContext'> & {
  items: readonly MenuEntry[];
  anchorRef?: React.RefObject<View | null>;
  width?: number;
  presentation?: 'anchored' | 'sheet' | 'sideSheet';
  placement?: PopoverProps['placement'];
  offset?: number;
  alignOffset?: number;
  initialFocusId?: string;
  secondaryTitle?: React.ReactNode;
  context?: React.ReactNode;
};
export type MenuRowProps = Omit<PressableProps, 'children'> & {
  focusId?: string;
  label: React.ReactNode;
  icon?: IconName;
  description?: React.ReactNode;
  selected?: boolean;
  destructive?: boolean;
  tone?: 'default' | 'owner';
  trailing?: React.ReactNode;
  variant?: 'compact' | 'grouped' | 'tv';
  onMenu?: () => void;
};
export type ToastMessage = {
  id: string;
  message: React.ReactNode;
  accessibilityLabel?: string;
  icon?: IconName;
  leading?: React.ReactNode;
  action?: {label: string; onPress: () => void};
  /** Explicit TV compositions with an authored Undo action opt in. */
  actionOnTV?: boolean;
  durationMs?: number;
};
export type ToastDismissReason = 'timeout' | 'gesture' | 'action' | 'accessibility';
export type ToastProps = ToastMessage & {
  onDismiss: (id: string, reason: ToastDismissReason) => void;
  onPauseChange?: (paused: boolean, reason?: 'touch' | 'focus' | 'gesture') => void;
  testID?: string;
};
export type ToastHostProps = {
  toast: ToastMessage | null;
  onDismiss: (id: string, reason: ToastDismissReason) => void;
  /** Height of tab bar or dock, excluding the safe-area inset. */
  bottomOffset?: number;
  /** Optional presentation-only clearance: the top of whatever actually occupies the
   * bottom of this screen, measured from the viewport bottom. Omitted by every
   * existing caller, which then keeps the original bottomOffset + safe area + 12. */
  obstructionTop?: number;
  topOffset?: number;
  testID?: string;
};
export type BannerProps = {
  message: React.ReactNode;
  accessibilityLabel?: string;
  variant?: 'quiet' | 'notice';
  tone?: 'info' | 'warning' | 'error' | 'success';
  icon?: IconName;
  action?: {label: string; onPress: () => void};
  visible?: boolean;
  placement?: 'overlay' | 'inline';
  topInset?: number;
  testID?: string;
};
