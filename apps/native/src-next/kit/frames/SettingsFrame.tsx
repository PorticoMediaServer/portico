import React, {createContext, useContext, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {TopBarBack} from '../layout/TopBar';
import {StartFocusScope} from '../focus';
import {useTheme} from '../theme';
import {FrameColumn, useFrameMetrics} from './metrics';
import {PageHeader} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {SheetBar, SheetFrame, useInSheet, type SheetBarProps} from './SheetFrame';
import type {OverlayReason} from '../overlays/types';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Icon} from '../Icon';
import {Button} from '../controls/Button';

/** True for a page the list frame places beside its list (tablet, TV). A page shown alone keeps Back, the gutter and a column limit. */
const BesideList = createContext(false);

export type SettingsListFrameProps = Readonly<{
  /** The sheet's bar while the list shows (Settings, Done). Phone only; wider forms have the shell's navigation. */
  bar: SheetBarProps;
  /** Phone: the sheet's open state. Wider forms draw Settings in the page and ignore it. */
  open: boolean; onOpenChange: (open: boolean, reason: OverlayReason) => void;
  /** The list: search, the Account groups, then the Server band (`SettingsNav`). */
  list: ReactNode;
  /** The search field, over the list on every form (pass `SettingsNav` no `search` of its own). */
  search?: ReactNode;
  /** Phone: the profile card (`SettingsProfileCard`) under the search field. */
  profile?: ReactNode;
  /** Phone: the four quick tiles (`ShortcutTiles`: Switch profile, Switch server, Notifications with its count, Watch Together). */
  shortcuts?: ReactNode;
  /** The selected page, a `SettingsPageFrame`. On a phone it takes the list's place inside the same sheet. */
  page?: ReactNode;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
  /** TV: the key hints under the list (R11: "Up Down Choose a page", ...). */
  hints?: ReactNode;
}>;

/** Frame 3, the index of Settings. Phone: one full-height sheet with one bar; the list, or the selected page in its
 * place. Tablet and TV: the list in its own column beside the page, each with its own scroll. */
export function SettingsListFrame({startFocus, ...props}: SettingsListFrameProps) {
  return <StartFocusScope id={startFocus}><SettingsListBody {...props}/></StartFocusScope>;
}
function SettingsListBody({bar, open, onOpenChange, search, list, profile, shortcuts, page, hints, testID}: Omit<SettingsListFrameProps, 'startFocus'>) {
  const frame = useFrameMetrics(), {form} = frame, width = frameSpec.settings.listWidth[form];
  // A TV row's focus ring is drawn outside the row: the list's scroll box leaves it that room so it is never cut.
  const theme = useTheme(), ring = form === 'tv' ? theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv : 0;
  if (form === 'phone') return <SheetFrame kind="full" open={open} onOpenChange={onOpenChange} bar={page ? undefined : bar} testID={testID}>
    {page ?? <ScrollView contentContainerStyle={{paddingBottom: frameSpec.sheet.footerClear}}>{search}{profile}{shortcuts}{list}</ScrollView>}
  </SheetFrame>;
  return <View testID={testID} style={{flex: 1, flexDirection: 'row', paddingLeft: frame.gutter, gap: frameSpec.settings.gap[form]}}>
    {/* The list's column is already placed from the gutter, so the cards in it start at the column's own edges. */}
    <View style={{width}}>
      <ScrollView style={{flex: 1, width: width + 2 * ring, marginLeft: -ring}} contentContainerStyle={{paddingTop: frame.titleTop || frame.headerGap, paddingBottom: hints ? frame.headerGap : frame.bottomClear, paddingHorizontal: ring}}><FrameColumn.Provider value>{search}{list}</FrameColumn.Provider></ScrollView>
      {form === 'tv' && hints ? <View style={{paddingBottom: frame.bottomClear}}>{hints}</View> : null}
    </View>
    <View style={{flex: 1}}><BesideList.Provider value>{page}</BesideList.Provider></View>
  </View>;
}

export type SettingsPageFrameProps = Readonly<{
  title: string;
  /** Back to the list on a phone; named after the page it returns to. */
  back?: TopBarBack;
  /** The bar's one text action in a sheet (Done, Save). */
  action?: SheetBarProps['action'];
  /** Beside the title: Saving or "✓ Saved" (desktop-settings f1, tablet f12). */
  status?: 'saving' | 'saved';
  /** The page's own buttons beside the title ("Invite someone", "Add account"); in a phone's sheet the bar's `action` instead. */
  actions?: ReactNode;
  /** An owner page's server header (kit `ServerHeader`): the card under the bar on a phone (phone-app 19), the strip over the title wider. */
  server?: ReactNode;
  /** One short paragraph under the header. */
  intro?: string;
  /** The page's own tabs (kit `Tabs`: Unread / All / Archived; Accounts / Invitations / Devices), under the intro. */
  tabs?: ReactNode;
  /** Sections of rows (`SectionCard`): the one flexible region. */
  children?: ReactNode;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
}>;

/** Frame 4, one settings or owner page, or a form: its header, an intro, then sections of rows. Which header it
 * gets depends only on where it is (rule 3): the compact bar inside a sheet, Back and a large title full screen on
 * a phone, the page title in the column beside the list on a tablet and TV. */
export function SettingsPageFrame({startFocus, ...props}: SettingsPageFrameProps) {
  return <StartFocusScope id={startFocus}><SettingsPageBody {...props}/></StartFocusScope>;
}
function SettingsPageBody({title, back, action, status, actions, server, intro, tabs, children, state, testID}: Omit<SettingsPageFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), inSheet = useInSheet(), beside = useContext(BesideList) && frame.form !== 'phone' && !inSheet, t = defaultI18n.t;
  const saved = status && <View accessibilityLiveRegion="polite" style={{flexDirection: 'row', alignItems: 'center', gap: 4}}>{status === 'saved' && <Icon name="i-check" size={16} color={theme.colors.textSecondary}/>}
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t(status === 'saved' ? 'next.settings.saved' : 'next.settings.saving')}</Text></View>;
  // Outside the phone's sheet the page's own action (Mark all read, Invite someone) joins the header's end.
  const own = action && !inSheet && !actions ? <Button variant="link" disabled={action.disabled} onPress={action.onPress}>{action.label}</Button> : undefined;
  const trailing = (saved || actions || own) ? <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>{saved}{actions}{own}</View> : undefined;
  // Rule 3: the compact bar inside the phone's Settings sheet; the page title beside the list; a page shown alone keeps
  // Back and the large title at the gutter (tablets SC-8, r7 S6 on TV), like any pushed page.
  const header = inSheet ? <SheetBar title={title} back={back} action={action}/>
    : beside ? <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.sm, marginTop: server ? frame.headerGap : frame.titleTop || frame.headerGap, marginBottom: frame.headerGap}}>
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {flex: 1, color: theme.colors.text}]}>{title}</Text>{trailing}</View>
    : <PageHeader title={title} back={back} trailing={trailing}/>;
  const strip = server && <View style={{marginHorizontal: beside ? 0 : frame.cardInset, marginTop: beside ? frame.titleTop || frame.headerGap : 0}}>{server}</View>;
  const body = <>
    {intro !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginHorizontal: beside ? 0 : frame.gutter, maxWidth: frameSpec.settings.paragraphMax}]}>{intro}</Text>}
    {tabs}
    {children}
  </>;
  // A page shown alone on a tablet or TV is held to the page's column, as it is beside the list.
  const column = !beside && frame.form !== 'phone' && !inSheet ? {maxWidth: frameSpec.settings.pageMax + 2 * frame.gutter} : undefined;
  return <View testID={testID} style={[{flex: 1}, column]}>
    {beside && strip}
    {header}
    {!beside && strip}
    {state ? <PageStateFrame {...state}/> : <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: inSheet ? frameSpec.sheet.footerClear : frame.bottomClear, paddingRight: beside ? frame.rightGutter : 0, gap: frame.sectionGap}}>
      {/* Beside the list the frame has already placed this column's edges, so its sections start at the column. */}
      {beside ? <FrameColumn.Provider value>{body}</FrameColumn.Provider> : body}
    </ScrollView>}
  </View>;
}
