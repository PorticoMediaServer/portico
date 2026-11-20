import React, {createContext, useContext, type ReactNode, type Ref} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {TopBarBack} from '../layout/TopBar';
import {StartFocusScope} from '../focus';
import {FormScrollView} from '../../platform/keyboard';
import {useTheme} from '../theme';
import {FrameColumn, useFrameMetrics} from './metrics';
import {PageHeader} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {SheetBar, useInSheet, type SheetBarProps} from './SheetFrame';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Icon} from '../Icon';
import {Button} from '../controls/Button';

/** True for a page the list frame places beside its list (tablet, TV). A page shown alone keeps Back, the gutter and a column limit. */
const BesideList = createContext(false);
/** True for a page the list frame shows in the list's place (a phone): it takes the compact bar, as in a sheet. */
const InListPlace = createContext(false);

export type SettingsListFrameProps = Readonly<{
  /** The sheet's bar while the list shows (Settings, Done) on a phone; wider forms draw its title over the search, as the
   * page's title beside it (final3-tv-15, desktop-settings f1, tablet f12). */
  bar: SheetBarProps;
  backdrop?: ReactNode;
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
  /** TV search puts the keyboard in the wider left column. */
  searching?: boolean;
  /** The phone sheet has begun its close animation. */
  closing?: boolean;
}>;

/** Frame 3, the index of Settings. Phone: one bar over the list, or the selected page in its place. Tablet and TV: the
 * list in its own column beside the page, each with its own scroll. The surface (the phone's sheet, a tablet's page) is
 * the caller's: Settings' own `SettingsSheet`. One tree on every form: a form change (a foldable opening, a tablet
 * crossing 600) restyles the two columns and never remounts the open page, so its edits and scroll survive. */
export function SettingsListFrame({startFocus, ...props}: SettingsListFrameProps) {
  return <StartFocusScope id={startFocus}><SettingsListBody {...props}/></StartFocusScope>;
}
function SettingsListBody({bar, backdrop, search, list, profile, shortcuts, page, hints, testID, searching, closing}: Omit<SettingsListFrameProps, 'startFocus'>) {
  const frame = useFrameMetrics(), {form} = frame, phone = form === 'phone', width = searching && form === 'tv' ? frameSpec.settings.tvSearchWidth : frameSpec.settings.listWidth[form];
  // A TV row's focus ring is drawn outside the row: the list's scroll box leaves it that room so it is never cut.
  const theme = useTheme(), column = useContext(FrameColumn), ring = form === 'tv' ? theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv : 0;
  // A phone shows one of the two columns; the other stays mounted, hidden.
  const shown = {flex: 1}, hidden = {display: 'none'} as const;
  return <View testID={testID} pointerEvents={closing ? 'none' : undefined} style={{flex: 1, flexDirection: phone ? 'column' : 'row', paddingLeft: phone ? 0 : frame.gutter, gap: phone ? 0 : frameSpec.settings.gap[form]}}>
    {backdrop}
    {/* The list's column is already placed from the gutter, so the cards in it start at the column's own edges. */}
    <View style={phone ? page ? hidden : shown : {width}}>
      {phone && <SheetBar {...bar}/>}
      <ScrollView keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive" style={phone ? undefined : {flex: 1, width: width + 2 * ring, marginLeft: -ring}}
        contentContainerStyle={phone ? {paddingBottom: frameSpec.sheet.footerClear} : {paddingTop: frame.titleTop || frame.headerGap, paddingBottom: hints ? frame.headerGap : frame.bottomClear, paddingHorizontal: ring}}>
        <FrameColumn.Provider value={!phone || column}>
          {!phone && !searching && <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, marginBottom: frame.headerGap}]}>{bar.title}</Text>}
          {phone ? <View style={{marginHorizontal: frame.cardInset, marginBottom: theme.space.xs}}>{search}</View> : search}{phone && profile}{phone && shortcuts}{list}
        </FrameColumn.Provider>
      </ScrollView>
      {form === 'tv' && hints ? <View style={{paddingBottom: frame.bottomClear}}>{hints}</View> : null}
    </View>
    <View style={phone ? page ? shown : hidden : {flex: 1}}><BesideList.Provider value={!phone}><InListPlace.Provider value={phone}>{page}</InListPlace.Provider></BesideList.Provider></View>
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
  /** A server panel that owns its own scrolling. */
  panel?: ReactNode;
  /** An embedded panel or the TV search results already has a heading. */
  hideHeader?: boolean;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
  /** The page's scroll, for a screen that brings a found row into view (Settings search), and its height once laid out. */
  scrollRef?: Ref<ScrollView>;
  onScrollLayout?: (height: number) => void;
  onScrollOffset?: (y: number) => void;
}>;

/** Frame 4, one settings or owner page, or a form: its header, an intro, then sections of rows. Which header it
 * gets depends only on where it is (rule 3): the compact bar inside a sheet, Back and a large title full screen on
 * a phone, the page title in the column beside the list on a tablet and TV. */
export function SettingsPageFrame({startFocus, ...props}: SettingsPageFrameProps) {
  return <StartFocusScope id={startFocus}><SettingsPageBody {...props}/></StartFocusScope>;
}
function SettingsPageBody({title, back, action, status, actions, server, intro, tabs, children, panel, hideHeader, state, testID, scrollRef, onScrollLayout, onScrollOffset}: Omit<SettingsPageFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), parentColumn = useContext(FrameColumn), inListPlace = useContext(InListPlace), inSheet = useInSheet() || inListPlace, beside = useContext(BesideList) && frame.form !== 'phone' && !inSheet, t = defaultI18n.t;
  const saved = status && <View accessibilityLiveRegion="polite" style={{flexDirection: 'row', alignItems: 'center', gap: 4}}>{status === 'saved' && <Icon name="i-check" size={16} color={theme.colors.textSecondary}/>}
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t(status === 'saved' ? 'next.settings.saved' : 'next.settings.saving')}</Text></View>;
  // Outside the phone's sheet the page's own action (Mark all read, Invite someone) joins the header's end.
  const own = action && !inSheet && !actions ? <Button variant="link" disabled={action.disabled} onPress={action.onPress}>{action.label}</Button> : undefined;
  const trailing = (saved || actions || own) ? <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>{saved}{actions}{own}</View> : undefined;
  // Rule 3: the compact bar inside the phone's Settings sheet; the page title beside the list; a page shown alone keeps
  // Back and the large title at the gutter (tablets SC-8, r7 S6 on TV), like any pushed page.
  const header = inSheet ? <SheetBar title={title} back={back} action={action}/>
    : beside ? <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.sm, marginTop: server ? frame.headerGap : frame.titleTop || frame.headerGap, marginBottom: frame.headerGap}}>
      {/* "✓ Saved" follows the title, the page's own buttons keep the far end (desktop-settings f1, final3-tv-15). */}
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {flexShrink: 1, color: theme.colors.text}]}>{title}</Text>
      {saved && <View style={{marginBottom: theme.space.xs}}>{saved}</View>}<View style={{flex: 1}}/>
      {(actions || own) && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>{actions}{own}</View>}</View>
    : <PageHeader title={title} back={back} trailing={trailing}/>;
  const strip = server && <View style={{marginHorizontal: beside ? 0 : frame.cardInset, marginTop: beside ? frame.titleTop || frame.headerGap : 0}}>{server}</View>;
  const body = <>
    {intro !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginHorizontal: beside ? 0 : frame.gutter, maxWidth: frameSpec.settings.paragraphMax}]}>{intro}</Text>}
    {tabs}
    {children}
  </>;
  // A page shown alone on a tablet or TV is held to the page's column, as it is beside the list.
  const ring = frame.form === 'tv' ? theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv : 0;
  const column = !beside && frame.form !== 'phone' && !inSheet ? {maxWidth: frameSpec.settings.pageMax + 2 * frame.gutter} : undefined;
  return <View testID={testID} style={[{flex: 1}, column]}>
    {beside && strip}
    {!hideHeader && header}
    {!beside && strip}
    {panel ?? (state ? <PageStateFrame {...state}/> : <FormScrollView ref={scrollRef} onLayout={onScrollLayout && (event => onScrollLayout(event.nativeEvent.layout.height))} keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive"
      scrollEventThrottle={onScrollOffset ? 100 : undefined} onScroll={onScrollOffset && (event => onScrollOffset(event.nativeEvent.contentOffset.y))}
      fadingEdgeLength={ring ? frame.bottomClear : undefined} style={{flex: 1, marginLeft: -ring}}
      contentContainerStyle={{paddingBottom: inSheet ? frameSpec.sheet.footerClear : frame.bottomClear, paddingLeft: ring, paddingRight: beside ? frame.rightGutter : 0, gap: frame.sectionGap}}>
      {/* Beside the list the frame has already placed this column's edges, so its sections start at the column. One
          provider on every form, so a form change restyles the body and never remounts it. */}
      <FrameColumn.Provider value={beside || parentColumn}>{body}</FrameColumn.Provider>
    </FormScrollView>)}
  </View>;
}
