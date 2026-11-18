import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {TopBarBack} from '../layout/TopBar';
import {useTheme} from '../theme';
import {FrameColumn, useFrameMetrics} from './metrics';
import {PageHeader} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {SheetBar, SheetFrame, useInSheet, type SheetBarProps} from './SheetFrame';
import type {OverlayReason} from '../overlays/types';

export type SettingsListFrameProps = Readonly<{
  /** The sheet's bar while the list shows (Settings, Done). Phone only; wider forms have the shell's navigation. */
  bar: SheetBarProps;
  /** Phone: the sheet's open state. Wider forms draw Settings in the page and ignore it. */
  open: boolean; onOpenChange: (open: boolean, reason: OverlayReason) => void;
  /** The list: search, the Account groups, then the Server band (`SettingsNav`). */
  list: ReactNode;
  /** The selected page, a `SettingsPageFrame`. On a phone it takes the list's place inside the same sheet. */
  page?: ReactNode;
  testID?: string;
}>;

/** Frame 3, the index of Settings. Phone: one full-height sheet with one bar; the list, or the selected page in its
 * place. Tablet and TV: the list in its own column beside the page, each with its own scroll. */
export function SettingsListFrame({bar, open, onOpenChange, list, page, testID}: SettingsListFrameProps) {
  const frame = useFrameMetrics(), {form} = frame, width = frameSpec.settings.listWidth[form];
  if (form === 'phone') return <SheetFrame kind="full" open={open} onOpenChange={onOpenChange} bar={page ? undefined : bar} testID={testID}>
    {page ?? <ScrollView contentContainerStyle={{paddingBottom: frameSpec.sheet.footerClear}}>{list}</ScrollView>}
  </SheetFrame>;
  return <View testID={testID} style={{flex: 1, flexDirection: 'row', paddingLeft: frame.gutter, gap: frameSpec.settings.gap[form]}}>
    {/* The list's column is already placed from the gutter, so the cards in it start at the column's own edges. */}
    <ScrollView style={{flexGrow: 0, width}} contentContainerStyle={{paddingTop: frame.titleTop || frame.headerGap, paddingBottom: frame.bottomClear}}><FrameColumn.Provider value>{list}</FrameColumn.Provider></ScrollView>
    <View style={{flex: 1}}>{page}</View>
  </View>;
}

export type SettingsPageFrameProps = Readonly<{
  title: string;
  /** Back to the list on a phone; named after the page it returns to. */
  back?: TopBarBack;
  /** The bar's one text action in a sheet (Done, Save). */
  action?: SheetBarProps['action'];
  /** One short paragraph under the header. */
  intro?: string;
  /** Sections of rows (`SectionCard`): the one flexible region. */
  children?: ReactNode;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
  testID?: string;
}>;

/** Frame 4, one settings or owner page, or a form: its header, an intro, then sections of rows. Which header it
 * gets depends only on where it is (rule 3): the compact bar inside a sheet, Back and a large title full screen on
 * a phone, the page title in the column beside the list on a tablet and TV. */
export function SettingsPageFrame({title, back, action, intro, children, state, testID}: SettingsPageFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), inSheet = useInSheet(), beside = frame.form !== 'phone' && !inSheet;
  const header = inSheet ? <SheetBar title={title} back={back} action={action}/>
    : beside ? <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, marginTop: frame.titleTop || frame.headerGap, marginBottom: frame.headerGap}]}>{title}</Text>
    : <PageHeader title={title} back={back}/>;
  const body = <>
    {intro !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginHorizontal: beside ? 0 : frame.gutter, maxWidth: frameSpec.settings.paragraphMax}]}>{intro}</Text>}
    {children}
  </>;
  return <View testID={testID} style={{flex: 1}}>
    {header}
    {state ? <PageStateFrame {...state}/> : <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: inSheet ? frameSpec.sheet.footerClear : frame.bottomClear, paddingRight: beside ? frame.rightGutter : 0, gap: frame.sectionGap}}>
      {/* Beside the list the frame has already placed this column's edges, so its sections start at the column. */}
      {beside ? <FrameColumn.Provider value>{body}</FrameColumn.Provider> : body}
    </ScrollView>}
  </View>;
}
