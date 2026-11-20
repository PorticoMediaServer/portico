import React, {createContext, useContext, type ReactNode, type RefObject} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec, overlayForm, popoverWidth, type OverlayBar, type OverlayFoot, type OverlayTask} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {BackCloseHints} from '../controls/KeyHints';
import {Icon} from '../Icon';
import type {TopBarBack} from '../layout/TopBar';
import {BottomSheet} from '../overlays/BottomSheet';
import {Popover} from '../overlays/Menu';
import {Dialog, OverlayFootRow} from '../overlays/surface';
import type {OverlayReason} from '../overlays/types';
import {StartFocusScope} from '../focus';
import {useTheme} from '../theme';
import {FrameColumn, useFrameMetrics} from './metrics';

/** True for whatever is drawn inside a sheet: it has no header of its own, the sheet's bar shows its title (rule 3). */
const InSheet = createContext(false);
export const useInSheet = (): boolean => useContext(InSheet);

export type SheetBarProps = Readonly<{
  title: string;
  /** Named after the page it returns to. */
  back?: TopBarBack;
  /** The bar's one text action (Done, Save). */
  action?: Readonly<{label: string; onPress: () => void; disabled?: boolean; testID?: string}>;
}>;

/** Rule 3, the compact bar of a full-height sheet. Long headings can grow above the rows. */
export function SheetBar({title, back, action}: SheetBarProps) {
  const theme = useTheme();
  return <View style={{minHeight: frameSpec.header.bar, flexDirection: 'row', alignItems: 'center', paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs}}>
    <View style={{flex: 1, alignItems: 'flex-start'}}>{back && <Focusable testID={back.testID} disabled={back.disabled}
      accessibilityRole="button" accessibilityLabel={back.label} onPress={back.onPress}
      style={{minHeight: theme.targets.touch, flexDirection: 'row', alignItems: 'center'}}>
      <Icon name="i-left" size={theme.type.rowHeading.fontSize} color={theme.colors.textSecondary}/>
      <Text style={[theme.type.rowLabel, {flexShrink: 1, color: theme.colors.textSecondary}]}>{back.label}</Text>
    </Focusable>}</View>
    <Text accessibilityRole="header" style={[theme.type.barTitle, {maxWidth: '55%', textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
    <View style={{flex: 1, alignItems: 'flex-end'}}>{action && <Button variant="text" size="small" align="end"
      textStyle={{color: theme.colors.accentStrong}} disabled={action.disabled} onPress={action.onPress} testID={action.testID}>{action.label}</Button>}</View>
  </View>;
}

/** R9: the overlay bar's three slots from the table: Cancel or nothing at the start; the title centred or at the start
 * with the item it is about beside it; Done (accent), the task's action, the round close, or nothing at the end. */
function OverlayBarRow({bar, title, subtitle, action, onClose}: {bar: OverlayBar; title: string; subtitle?: string; action?: SheetAction; onClose: () => void}) {
  const theme = useTheme(), frame = useFrameMetrics(), t = defaultI18n.t;
  const end = bar.trailing === 'done' ? <Button variant="text" size="small" textStyle={{color: theme.colors.accentStrong}} onPress={onClose}>{t('next.overlays.done')}</Button>
    : bar.trailing === 'action' && action ? <Button variant="text" size="small" textStyle={{color: theme.colors.accentStrong}} disabled={action.disabled} onPress={action.onPress}>{action.label}</Button>
    : bar.trailing === 'close' ? <Button variant="icon" size="small" icon="i-x" accessibilityLabel={t('next.overlays.close')} onPress={onClose}/> : null;
  const titleText = <Text accessibilityRole="header" style={[theme.type.barTitle, {color: theme.colors.text, includeFontPadding: false, textAlign: bar.title === 'centre' ? 'center' : 'left', flexShrink: 1}]}>{title}</Text>;
  // The item stays beside a title that fits with it on one line, else it wraps under the title; the title never wraps for it.
  const heading = subtitle !== undefined && bar.subtitle ? <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', columnGap: theme.space.xs, flexShrink: 1}}>
    {titleText}<Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{subtitle}</Text></View> : titleText;
  if (bar.title === 'centre') return <View style={{minHeight: frameSpec.header.bar, flexDirection: 'row', alignItems: 'center', paddingHorizontal: frame.gutter}}>
    <View style={{flex: 1, alignItems: 'flex-start'}}>{bar.leading === 'cancel' && <Button variant="text" size="small" onPress={onClose}>{t('next.overlays.cancel')}</Button>}</View>
    {heading}<View style={{flex: 1, alignItems: 'flex-end'}}>{end}</View>
  </View>;
  return <View style={{minHeight: frameSpec.header.bar, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: frame.gutter}}><View style={{flex: 1}}>{heading}</View>{end}</View>;
}

export type SheetAction = Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
type SheetBase = Readonly<{open: boolean; onOpenChange: (open: boolean, reason: OverlayReason) => void; children: ReactNode; testID?: string;
  /** TV: the row the sheet starts on (R1), by its `focusId` or `testID`: the first row, or Cancel in a confirmation. */
  startFocus?: string}>;
export type SheetFrameProps = SheetBase & (
  /** R9: menus, pickers, confirmations, short tasks, details and filters. What kind of overlay the task is on this form
   * (sheet, dialog, side panel, popover), what its bar holds and how the foot lays out come from packages/design
   * `overlayForms`; a screen names the task, never the kind. The rows are the flexible region. */
  | Readonly<{kind: 'task'; task: Exclude<OverlayTask, 'settings' | 'notifications'>; title: string; subtitle?: string;
      /** The task's own action: in the bar where the form puts it there, else the foot's primary. */
      action?: SheetAction;
      /** Foot buttons (Cancel and Delete; Back and Add source), laid out by the form's foot. */
      foot?: ReactNode;
      /** A popover's trigger: it opens beside or below it and the trigger stays in view. */
      anchorRef?: RefObject<View | null>;
      /** TV: the key hints at the sheet's foot where the form's foot is `hints` (R11: "OK Choose", "Back Close"); Back Close when left out. */
      hints?: ReactNode}>
  /** Settings and editors on a phone: full height from 62 below the top. `bar` is left out only when the content
   * is a page that brings the bar itself (`SettingsPageFrame`). */
  | Readonly<{kind: 'full'; bar?: SheetBarProps}>);

/** Frame 5 and rule 2. A sheet is a plain surface: raised (bottom) or ink (full height), its corners, grab and scrim,
 * and nothing else. It takes no background, glow or header of the content's own. */
export function SheetFrame(props: SheetFrameProps) {
  const theme = useTheme(), viewport = useWindowDimensions(), {height} = viewport, insets = useSafeAreaInsets(), spec = frameSpec.sheet;
  const {open, onOpenChange, testID} = props, frame = useFrameMetrics();
  if (props.kind === 'task') {
    const form = overlayForm(props.task, frame.form), close = () => onOpenChange(false, 'close-button'), t = defaultI18n.t;
    const bar = form.bar;
    // TV focus regions need a stable id on every focusable; the sheet names its own buttons.
    const primary = props.action && <Button testID={`${testID ?? 'sheet'}-action`} variant="primary" disabled={props.action.disabled} onPress={props.action.onPress}>{props.action.label}</Button>;
    // A read-only TV sheet (File info) has one full-width Done at its foot and starts on it (detail-tv-5d).
    const doneId = `${testID ?? 'sheet'}-done`, done = frame.form === 'tv' && form.foot === 'full' && !props.foot && !props.action;
    const buttons = props.foot ?? (props.action && bar.trailing !== 'action' ? <>{bar.leading !== 'cancel' && <Button testID={`${testID ?? 'sheet'}-cancel`} variant="glass" onPress={close}>{t('next.overlays.cancel')}</Button>}{primary}</>
      : done ? <Button testID={doneId} variant="primary" onPress={close}>{t('next.overlays.done')}</Button> : null);
    const start = props.startFocus ?? (done ? doneId : undefined), children = <StartFocusScope id={start}>{props.children}</StartFocusScope>;
    // The foot is drawn outside the body, so it carries the same start (a Done that the sheet starts on).
    const footer = buttons ? <StartFocusScope id={start}><OverlayFootRow layout={form.foot}>{buttons}</OverlayFootRow></StartFocusScope> : undefined;
    // A side panel, a dialog and a popover place their own column (their padding is the edge), so the page gutter and card
    // inset are zero inside them, as beside the Settings list; a bottom sheet spans the window and keeps them.
    const column = (content: ReactNode) => form.kind !== 'sheet' ? <FrameColumn.Provider value>{content}</FrameColumn.Provider> : content;
    const body = column(<InSheet.Provider value>{children}</InSheet.Provider>);
    const header = column(<OverlayBarRow bar={bar} title={props.title} subtitle={props.subtitle} action={props.action} onClose={close}/>);
    // R9, TV: Back closes and the foot says so. The kit sheet's TV form is the side sheet; key hints stand where buttons
    // would, and a task's own action (Add source) is the last stop of the body, the sheet's width.
    const tvAction = props.foot ?? primary;
    if (form.foot === 'hints') return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} width={form.width} title={props.title} description={bar.subtitle ? props.subtitle : undefined} footer={props.hints ?? <BackCloseHints/>} showClose={false}>
      {body}{tvAction && <View style={{marginTop: theme.space.md}}><OverlayFootRow layout="full">{tvAction}</OverlayFootRow></View>}</BottomSheet>;
    if (form.kind === 'popover' && props.anchorRef) return <Popover open={open} onOpenChange={onOpenChange} anchorRef={props.anchorRef} title={props.title} modal={form.modal} backdropOpacity={form.modal ? .5 : 0}
      placement={frame.form === 'phone' ? 'bottom-start' : 'right-end'} width={popoverWidth.min.native}
      maxWidth={frame.form === 'phone' ? viewport.width - 2 * frame.gutter : popoverWidth.max} testID={testID}>{bar.title === 'start' ? header : null}{body}</Popover>;
    if (form.kind === 'dialog') return <Dialog open={open} onOpenChange={onOpenChange} title={props.title} description={bar.subtitle ? props.subtitle : undefined} footer={footer} testID={testID}>{body}</Dialog>;
    // TV draws its side sheet's own heading (the title, the item it is about under it), as the hints form does.
    return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} width={form.width} title={props.title} description={bar.subtitle ? props.subtitle : undefined} headerContent={frame.form === 'tv' ? undefined : header} footer={footer} showClose={false} fitContent={form.kind === 'sheet'}>{body}</BottomSheet>;
  }
  const children = <StartFocusScope id={props.startFocus}>{props.children}</StartFocusScope>;
  return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} title={props.bar?.title ?? ''} accessibilityLabel={props.bar?.title} scrollable={false}
    detents={[height - Math.max(insets.top, spec.fullTop)]} headerContent={props.bar ? <SheetBar {...props.bar}/> : null}
    style={{backgroundColor: theme.colors.ink, borderTopLeftRadius: spec.fullRadius, borderTopRightRadius: spec.fullRadius}}>
    <InSheet.Provider value>{children}</InSheet.Provider>
  </BottomSheet>;
}
