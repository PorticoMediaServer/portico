import React, {Children, createContext, useContext, type ReactNode, type RefObject} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec, overlayForm, type OverlayBar, type OverlayFoot, type OverlayTask} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button} from '../controls/Button';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {BottomSheet} from '../overlays/BottomSheet';
import {Popover} from '../overlays/Menu';
import {Dialog} from '../overlays/surface';
import type {OverlayReason} from '../overlays/types';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

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

/** Rule 3, the compact bar of a full-height sheet, 56 high: Back, the title centred at 17/700, one text action. */
export function SheetBar({title, back, action}: SheetBarProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{height: frameSpec.header.bar, justifyContent: 'center'}}>
    <TopBar safeArea={false} back={back} center={<Text accessibilityRole="header" numberOfLines={1} style={[theme.type.barTitle, {color: theme.colors.text}]}>{title}</Text>}
      trailing={action && <Button variant="text" size="small" textStyle={{color: theme.colors.accentStrong}} disabled={action.disabled} onPress={action.onPress} testID={action.testID}>{action.label}</Button>}/>
  </View>;
}

/** R9: a foot's buttons as the table lays them out. `halves` and `full` give each button an equal share of the width
 * (one button is the full width); `end` keeps them at their own width at the end, primary last. Never hugging the left. */
export function OverlayFootRow({layout, children}: {layout: OverlayFoot; children: ReactNode}) {
  const theme = useTheme(), items = Children.toArray(children).filter(Boolean);
  if (layout === 'end') return <View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: theme.space.controlGap}}>{items}</View>;
  return <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{items.map((item, index) => <View key={index} style={{flex: 1, minWidth: 0}}>{item}</View>)}</View>;
}

/** R9: the overlay bar's three slots from the table: Cancel or nothing at the start; the title centred or at the start
 * with the item it is about beside it; Done (accent), the task's action, the round close, or nothing at the end. */
function OverlayBarRow({bar, title, subtitle, action, onClose}: {bar: OverlayBar; title: string; subtitle?: string; action?: SheetAction; onClose: () => void}) {
  const theme = useTheme(), frame = useFrameMetrics(), t = defaultI18n.t;
  const end = bar.trailing === 'done' ? <Button variant="text" size="small" textStyle={{color: theme.colors.accentStrong}} onPress={onClose}>{t('next.overlays.done')}</Button>
    : bar.trailing === 'action' && action ? <Button variant="text" size="small" textStyle={{color: theme.colors.accentStrong}} disabled={action.disabled} onPress={action.onPress}>{action.label}</Button>
    : bar.trailing === 'close' ? <Button variant="icon" size="small" icon="i-x" accessibilityLabel={t('next.overlays.close')} onPress={onClose}/> : null;
  const heading = <Text accessibilityRole="header" style={[theme.type.barTitle, {color: theme.colors.text, includeFontPadding: false, textAlign: bar.title === 'centre' ? 'center' : 'left', flexShrink: 1}]}>{title}
    {subtitle !== undefined && bar.subtitle && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{'  '}{subtitle}</Text>}</Text>;
  if (bar.title === 'centre') return <View style={{minHeight: frameSpec.header.bar, flexDirection: 'row', alignItems: 'center', paddingHorizontal: frame.gutter}}>
    <View style={{flex: 1, alignItems: 'flex-start'}}>{bar.leading === 'cancel' && <Button variant="text" size="small" onPress={onClose}>{t('next.overlays.cancel')}</Button>}</View>
    {heading}<View style={{flex: 1, alignItems: 'flex-end'}}>{end}</View>
  </View>;
  return <View style={{minHeight: frameSpec.header.bar, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: frame.gutter}}><View style={{flex: 1}}>{heading}</View>{end}</View>;
}

export type SheetAction = Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
type SheetBase = Readonly<{open: boolean; onOpenChange: (open: boolean, reason: OverlayReason) => void; children: ReactNode; testID?: string}>;
export type SheetFrameProps = SheetBase & (
  /** R9: menus, pickers, confirmations, short tasks, details and filters. What kind of overlay the task is on this form
   * (sheet, dialog, side panel, popover), what its bar holds and how the foot lays out come from packages/design
   * `overlayForms`; a screen names the task, never the kind. The rows are the flexible region. */
  | Readonly<{kind: 'task'; task: Exclude<OverlayTask, 'settings' | 'account' | 'notifications'>; title: string; subtitle?: string;
      /** The task's own action: in the bar where the form puts it there, else the foot's primary. */
      action?: SheetAction;
      /** Foot buttons (Cancel and Delete; Back and Add source), laid out by the form's foot. */
      foot?: ReactNode;
      /** A popover's trigger: it opens beside or below it and the trigger stays in view. */
      anchorRef?: RefObject<View | null>}>
  /** Settings and editors on a phone: full height from 62 below the top. `bar` is left out only when the content
   * is a page that brings the bar itself (`SettingsPageFrame`). */
  | Readonly<{kind: 'full'; bar?: SheetBarProps}>);

/** Frame 5 and rule 2. A sheet is a plain surface: raised (bottom) or ink (full height), its corners, grab and scrim,
 * and nothing else. It takes no background, glow or header of the content's own. */
export function SheetFrame(props: SheetFrameProps) {
  const theme = useTheme(), {height} = useWindowDimensions(), insets = useSafeAreaInsets(), spec = frameSpec.sheet;
  const {open, onOpenChange, children, testID} = props, frame = useFrameMetrics();
  if (props.kind === 'task') {
    // TV's column of the table is the TV builder's: until it is filled a TV task keeps the kit sheet's own TV form.
    const form = overlayForm(props.task, frame.form), close = () => onOpenChange(false, 'close-button'), t = defaultI18n.t;
    const bar = form?.bar ?? {leading: 'none', title: 'start', trailing: 'none', subtitle: false} as const;
    const primary = props.action && <Button variant="primary" disabled={props.action.disabled} onPress={props.action.onPress}>{props.action.label}</Button>;
    const buttons = props.foot ?? (props.action && bar.trailing !== 'action' ? <>{bar.leading !== 'cancel' && <Button variant="glass" onPress={close}>{t('next.overlays.cancel')}</Button>}{primary}</> : null);
    const footer = buttons ? <OverlayFootRow layout={form?.foot ?? 'halves'}>{buttons}</OverlayFootRow> : undefined;
    const body = <InSheet.Provider value>{children}</InSheet.Provider>;
    const header = <OverlayBarRow bar={bar} title={props.title} subtitle={props.subtitle} action={props.action} onClose={close}/>;
    if (form?.kind === 'popover' && props.anchorRef) return <Popover open={open} onOpenChange={onOpenChange} anchorRef={props.anchorRef} title={props.title} modal={form.modal} backdropOpacity={form.modal ? .5 : 0}
      placement={frame.form === 'phone' ? 'bottom-start' : 'right-end'} width={320} testID={testID}>{bar.title === 'start' ? header : null}{body}</Popover>;
    if (form?.kind === 'dialog') return <Dialog open={open} onOpenChange={onOpenChange} title={props.title} description={bar.subtitle ? props.subtitle : undefined} footer={footer} testID={testID}>{body}</Dialog>;
    return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} title={props.title} headerContent={header} footer={footer} showClose={false} fitContent={form?.kind === 'sheet'}>{body}</BottomSheet>;
  }
  return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} title={props.bar?.title ?? ''} accessibilityLabel={props.bar?.title} scrollable={false}
    detents={[height - Math.max(insets.top, spec.fullTop)]} headerContent={props.bar ? <SheetBar {...props.bar}/> : null}
    style={{backgroundColor: theme.colors.ink, borderTopLeftRadius: spec.fullRadius, borderTopRightRadius: spec.fullRadius}}>
    <InSheet.Provider value>{children}</InSheet.Provider>
  </BottomSheet>;
}
