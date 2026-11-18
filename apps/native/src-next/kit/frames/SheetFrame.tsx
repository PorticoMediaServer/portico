import React, {createContext, useContext, type ReactNode} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {BottomSheet} from '../overlays/BottomSheet';
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
      trailing={action && <Button variant="text" size="small" disabled={action.disabled} onPress={action.onPress} testID={action.testID}>{action.label}</Button>}/>
  </View>;
}

type SheetBase = Readonly<{open: boolean; onOpenChange: (open: boolean, reason: OverlayReason) => void; children: ReactNode; testID?: string}>;
export type SheetFrameProps = SheetBase & (
  /** Menus, pickers, short tasks: a bottom sheet on a phone, a side panel on wider forms and TV. The rows are the
   * flexible region: the sheet grows to its limit, then they scroll. The footer's actions stay put. */
  | Readonly<{kind: 'bottom'; title: string; footer?: ReactNode}>
  /** Settings and editors on a phone: full height from 62 below the top. `bar` is left out only when the content
   * is a page that brings the bar itself (`SettingsPageFrame`). */
  | Readonly<{kind: 'full'; bar?: SheetBarProps}>);

/** Frame 5 and rule 2. A sheet is a plain surface: raised (bottom) or ink (full height), its corners, grab and scrim,
 * and nothing else. It takes no background, glow or header of the content's own. */
export function SheetFrame(props: SheetFrameProps) {
  const theme = useTheme(), {height} = useWindowDimensions(), insets = useSafeAreaInsets(), spec = frameSpec.sheet;
  const {open, onOpenChange, children, testID} = props;
  if (props.kind === 'bottom') return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} title={props.title} footer={props.footer} showClose={false}>
    <InSheet.Provider value>{children}</InSheet.Provider>
  </BottomSheet>;
  return <BottomSheet open={open} onOpenChange={onOpenChange} testID={testID} title={props.bar?.title ?? ''} accessibilityLabel={props.bar?.title} scrollable={false}
    detents={[height - Math.max(insets.top, spec.fullTop)]} headerContent={props.bar ? <SheetBar {...props.bar}/> : null}
    style={{backgroundColor: theme.colors.ink, borderTopLeftRadius: spec.fullRadius, borderTopRightRadius: spec.fullRadius}}>
    <InSheet.Provider value>{children}</InSheet.Provider>
  </BottomSheet>;
}
