import React, {type ReactNode, type Ref} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {useTheme} from '../theme';
import {TVRow} from '../layout/TVRow';
import {ChipRail} from '../browse/Toolbar';
import {useFrameMetrics} from './metrics';
import {NativePageInsetProvider, usePageInset} from '../layout/page-inset';
import {frameSpec} from '@portico/design';

export type SearchNone = Readonly<{title: string; body?: string; action?: ReactNode; testID?: string}>;
export type SearchFrameProps = Readonly<{
  /** The page title, drawn on a phone before anything is typed (phone-app 7). Wider forms have the shell's Search. */
  title?: string;
  /** The search field: the kit `TextField` (`variant="search"`), focused when the page opens. */
  field: ReactNode;
  /** Phone, while there is text: Cancel beside the field (phone-app 8). */
  cancel?: ReactNode;
  /** TV: the on-screen keyboard under the field, in its own column (final3-tv-09). */
  keyboard?: ReactNode;
  /** The scope chips with their counts (All, Movies 7, Shows 5 …): one line that scrolls sideways. */
  scopes?: ReactNode;
  /** One line over the results, when part of a search ran late (desktop-search s-top). */
  notice?: ReactNode;
  /** No matches: a large line and what to try, at the top of the results (desktop-search s-none). */
  none?: SearchNone;
  /** The one flexible region: before typing, Recent searches and browsing; while typing, the Top result and the
   * results by kind; after `none`, Browse instead. Kit `Section`s and `Row`s; the frame spaces them. */
  children?: ReactNode;
  /** In place of `children`, a list that scrolls itself: one kind's grid that pages as it nears the end (a FlatList cannot
   * sit inside the frame's scroller). The frame gives it the room under the head. */
  list?: ReactNode;
  /** The results' scroller, so a new search starts at the top. */
  scrollRef?: Ref<ScrollView>;
  overlay?: ReactNode;
  visible?: boolean;
  testID?: string;
}>;

/** Search (desktop-search, phone-app 7 and 8, tablet 10, final3-tv-09). Fixed: the title (phone, before typing), the
 * field with Cancel, the scope chips; on TV the field and its keyboard in a column on the left. Flexible: what is
 * under them. The field and chips stay where they are while the results scroll. */
export function SearchFrame({title, field, cancel, keyboard, scopes, notice, none, children, list, scrollRef, overlay, visible = true, testID}: SearchFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), inset = usePageInset(), tv = frame.form === 'tv';
  const top = Math.max(frame.titleTop, insets.top + frame.headerGap), edge = {paddingLeft: frame.gutter, paddingRight: frame.rightGutter};
  // Dragging the results puts the keyboard away; a tap on a result still lands while it is up.
  const results = list ?? <ScrollView ref={scrollRef} style={{flex: 1}} keyboardDismissMode="on-drag" keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingTop: tv ? frame.headingGap : frame.sectionGap / 2, paddingBottom: frame.bottomClear, gap: frame.sectionGap}}>
    {notice && <View style={edge}>{notice}</View>}
    {none && <View testID={none.testID} style={[edge, {gap: frame.headingGap}]}>
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{none.title}</Text>
      {none.body !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{none.body}</Text>}
      {none.action}
    </View>}
    {children}
  </ScrollView>;
  const chips = scopes && (tv
    ? <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0}} contentContainerStyle={[edge, {gap: theme.space.xs, alignItems: 'center'}]}>{scopes}</ScrollView></TVRow>
    : <View style={[edge, {flexDirection: 'row'}]}><ChipRail>{scopes}</ChipRail></View>);
  // TV: the results column runs to the window's right edge and starts a gutter early, so its rows and sections, which
  // keep their own gutters, line up with the column's edge.
  const {keyboardWidth, tvResultsGap, tvFocusRoom} = frameSpec.search;
  if (tv) return <View testID={testID} pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1, flexDirection: 'row', paddingTop: frame.titleTop, paddingLeft: frame.gutter}}>
    <View style={{width: keyboardWidth, gap: frame.headingGap}}>{field}{keyboard}</View>
    {/* The column is the page inset by the keyboard's column: parts that size from the page width (a row of tiles, the Top result) size from it. */}
    <NativePageInsetProvider value={inset + frame.gutter + keyboardWidth + tvResultsGap}>
      <View style={{flex: 1, minWidth: 0, marginLeft: list ? tvResultsGap - tvFocusRoom : tvResultsGap - frame.gutter, marginTop: list ? -tvFocusRoom : 0, gap: frame.headingGap}}>{chips}{results}</View>
    </NativePageInsetProvider>
    {overlay}
  </View>;
  return <View testID={testID} style={{flex: 1}}>
    <View style={{paddingTop: top, gap: frame.headingGap}}>
      {title !== undefined && frame.form === 'phone' && <Text accessibilityRole="header" style={[theme.type.pageTitle, edge, {color: theme.colors.text}]}>{title}</Text>}
      <View style={[edge, {flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}]}><View style={{flex: 1, minWidth: 0}}>{field}</View>{cancel}</View>
      {chips}
    </View>
    {results}
    {overlay}
  </View>;
}
