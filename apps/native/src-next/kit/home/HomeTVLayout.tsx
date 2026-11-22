import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, type AccessibilityActionEvent, type AccessibilityActionInfo} from 'react-native';
import {typeScale} from '@portico/design';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {alpha} from '../browse/presentation';
import {SidePanel} from '../overlays';
import {useTheme} from '../theme';

/** TV Customize Home (final3-tv): the panel at the right, 860 wide, with its title and line of help, the rows, and
 * Reset to default and Save at its foot. */
export function TVHomeLayoutPanel({title, description, onBack, initialFocusRef, footer, testID, children}: {
  title: string; description?: string; onBack: () => void; initialFocusRef?: React.RefObject<View | null>; footer?: ReactNode; testID: string; children: ReactNode;
}) {
  const theme = useTheme();
  return <SidePanel open onOpenChange={open => {if (!open) onBack();}} title={title} description={description} width={860} topInset={0}
    logicalCanvas={{width: 860, height: 1080}} showClose={false} titleType={typeScale.tenFoot.pageTitle}
    descriptionTextStyle={[theme.type.tvHomeLayoutIntro, {color: theme.colors.textTertiary, marginTop: 8}]} testID={testID}
    initialFocusRef={initialFocusRef} footer={footer} style={{paddingTop: 46, paddingLeft: 60, paddingRight: 96}}>{children}</SidePanel>;
}

/** The rows' own scroller; while a row is lifted, the line where it would land is drawn across it. */
export function TVHomeLayoutList({scrollRef, dropAt, children}: {scrollRef?: React.Ref<ScrollView>; /** From the list's top. */ dropAt?: number; children: ReactNode}) {
  const theme = useTheme();
  return <ScrollView ref={scrollRef} removeClippedSubviews={false} contentContainerStyle={{paddingTop: 16, paddingBottom: 20}}>
    {children}
    {dropAt !== undefined && <View pointerEvents="none" style={{position: 'absolute', left: 12, right: 12, top: dropAt - 3, height: 6, backgroundColor: theme.colors.accentStrong, borderRadius: 3}}/>}
  </ScrollView>;
}

/** One of Home's rows: its handle (rows that move), a saved view's mark, its name and what it shows, then its own
 * controls (a saved view's remove, the switch or "Always on Home"). OK on the row picks it up; lifted, it is raised and
 * dashed where it was. */
export function TVHomeLayoutRow({lifted, shift, height, minHeight, onMeasure, handle, leading, title, detail, hidden, trailing, rowRef, focusId, testID,
  disabled, preferredFocus, onPress, accessibilityLabel, accessibilityActions, onAccessibilityAction}: {
  lifted: boolean; shift: number; height?: number; minHeight: number; onMeasure: (y: number, height: number) => void;
  handle?: ReactNode; leading?: ReactNode; title: string; detail?: string; hidden?: boolean; trailing?: ReactNode;
  rowRef: (node: View | null) => void; focusId: string; testID: string; disabled: boolean; preferredFocus: boolean; onPress: () => void;
  accessibilityLabel: string; accessibilityActions: readonly AccessibilityActionInfo[]; onAccessibilityAction: (event: AccessibilityActionEvent) => void;
}) {
  const theme = useTheme();
  return <View onLayout={event => {const {y, height: measured} = event.nativeEvent.layout; onMeasure(y, measured);}}
    style={{minHeight, height: lifted ? height ?? minHeight : undefined, zIndex: lifted ? 8 : undefined, ...(lifted ? {borderWidth: 2, borderStyle: 'dashed', borderRadius: 0, borderColor: theme.colors.line} : {})}}>
    <View style={{flexDirection: 'row', alignItems: 'center', minHeight, paddingVertical: 6, gap: 18, borderRadius: 14,
      ...(lifted ? {position: 'absolute', left: 0, right: 0, backgroundColor: theme.colors.raisedHigh, transform: [{translateY: shift}, {scale: 1.03}]} : {})}}>
      {handle}
      {leading}
      <Focusable ref={rowRef} focusId={focusId} testID={testID} disabled={disabled} focusKind="row" focusRadius={16} preferredFocus={preferredFocus}
        accessibilityLabel={accessibilityLabel} accessibilityState={{selected: lifted}} onPress={onPress}
        accessibilityActions={[...accessibilityActions]} onAccessibilityAction={onAccessibilityAction}
        style={{flex: 1, minHeight, flexDirection: 'row', alignItems: 'center', gap: 18, paddingLeft: 12}}>
        <View style={{flex: 1}}>
          <Text style={[theme.type.homeLayoutTitle, {color: hidden ? theme.colors.textTertiary : theme.colors.text}]}>{title}</Text>
          {!!detail && <Text style={[theme.type.homeLayoutDetail, {color: theme.colors.textTertiary, marginTop: 1}]}>{detail}</Text>}
        </View>
      </Focusable>
      {trailing}
    </View>
  </View>;
}

/** The grip before a row that moves: OK picks the row up (focus then moves onto the row itself). */
export function TVHomeLayoutHandle({focusId, testID, label, lifted, disabled, onPress}: {focusId: string; testID: string; label: string; lifted: boolean; disabled: boolean; onPress: () => void}) {
  const theme = useTheme();
  return <Focusable focusId={focusId} testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled}
    focusKind="button" focusRadius={10} onPress={onPress} style={{width: 44, height: 50, alignItems: 'center', justifyContent: 'center'}}>
    <Icon name="i-grip" size={26} color={lifted ? theme.colors.text : theme.colors.textTertiary}/>
  </Focusable>;
}

/** A saved view's mark before its name. */
export function TVHomeViewMark() {
  const theme = useTheme();
  return <View style={{width: 36, height: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .1)}}>
    <Icon name="i-filter" size={22} color={theme.colors.textSecondary}/>
  </View>;
}

/** A saved view's remove (×) at the row's end. */
export function TVHomeLayoutRemove({focusId, testID, label, disabled, onPress}: {focusId: string; testID: string; label: string; disabled: boolean; onPress: () => void}) {
  const theme = useTheme();
  return <Focusable focusId={focusId} testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled}
    focusKind="button" focusRadius={21} onPress={onPress} style={{width: 42, height: 42, borderRadius: 22, alignItems: 'center', justifyContent: 'center'}}>
    <Icon name="i-close" size={22} color={theme.colors.textTertiary}/>
  </Focusable>;
}

/** "Always on Home" with its lock, in place of a switch on a row that cannot be hidden. */
export function TVHomeAlwaysOn({label}: {label: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 9, paddingRight: 18}}>
    <Icon name="i-lock" size={18} color={theme.colors.textTertiary}/>
    <Text style={[theme.type.homeLayoutAlwaysOn, {color: theme.colors.textTertiary}]}>{label}</Text>
  </View>;
}

/** While a row is lifted: its keys (▲▼ Move, OK Drop, Back Put back), under the page beside the panel. */
export function TVHomeMoveHints({keys}: {keys: readonly (readonly [key: string, label: string])[]}) {
  const theme = useTheme();
  return <View pointerEvents="none" style={{position: 'absolute', left: 200, right: 960, bottom: 56, flexDirection: 'row', justifyContent: 'center', gap: 28}}>
    {keys.map(([key, label]) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.text, backgroundColor: theme.colors.raised, paddingHorizontal: 10, borderRadius: 6}]}>{key}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{label}</Text>
    </View>)}
  </View>;
}

/** The layout changed on another device: say so, with Reload and Keep editing. */
export function TVHomeLayoutConflict({title, body, actions}: {title: string; body: string; actions: ReactNode}) {
  const theme = useTheme();
  return <View style={{paddingVertical: 20, flexDirection: 'row', gap: 16}}>
    <Icon name="i-warn" size={30} color={theme.colors.textSecondary}/>
    <View style={{flex: 1, gap: 12}}>
      <Text style={[theme.type.title, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{body}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 18}}>{actions}</View>
    </View>
  </View>;
}

/** A notice over the rows (a view that is no longer available), with its action. */
export function TVHomeLayoutNotice({message, action}: {message?: string; action?: ReactNode}) {
  const theme = useTheme();
  return <View style={{paddingVertical: 12, gap: 8}}>
    {message && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{message}</Text>}
    {action}
  </View>;
}

/** The panel's foot: one action at each end. */
export function TVHomeLayoutFoot({start, end}: {start?: ReactNode; end?: ReactNode}) {
  return <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 18}}>{start ?? <View/>}{end}</View>;
}

/** Reset in the reset confirmation: a glass button in the record colour. */
export function TVHomeResetButton({label, testID, disabled, onPress}: {label: string; testID: string; disabled: boolean; onPress: () => void}) {
  const theme = useTheme();
  return <Button focusId="reset-confirm" testID={testID} variant="glass" style={{backgroundColor: `${theme.colors.record}24`}} textStyle={{color: theme.colors.record}}
    disabled={disabled} onPress={onPress}>{label}</Button>;
}
