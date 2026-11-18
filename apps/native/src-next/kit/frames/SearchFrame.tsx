import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {useTheme} from '../theme';
import {TVRow} from '../layout/TVRow';
import {useFrameMetrics} from './metrics';

/** TV: the keyboard column's width beside the results (final3-tv-09: 416 of 1600, so 500 of 1920). R5 interim. */
export const searchSpec = {keyboardWidth: 500} as const; // R5 interim

export type SearchNone = Readonly<{title: string; body?: string; action?: ReactNode}>;
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
  testID?: string;
}>;

/** Search (desktop-search, phone-app 7 and 8, tablet 10, final3-tv-09). Fixed: the title (phone, before typing), the
 * field with Cancel, the scope chips; on TV the field and its keyboard in a column on the left. Flexible: what is
 * under them. The field and chips stay where they are while the results scroll. */
export function SearchFrame({title, field, cancel, keyboard, scopes, notice, none, children, testID}: SearchFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), tv = frame.form === 'tv';
  const top = Math.max(frame.titleTop, insets.top + frame.headerGap), edge = {paddingLeft: frame.gutter, paddingRight: frame.rightGutter};
  const results = <ScrollView style={{flex: 1}} contentContainerStyle={{paddingTop: tv ? frame.headingGap : frame.sectionGap / 2, paddingBottom: frame.bottomClear, gap: frame.sectionGap}}>
    {notice && <View style={edge}>{notice}</View>}
    {none && <View style={[edge, {gap: frame.headingGap}]}>
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{none.title}</Text>
      {none.body !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{none.body}</Text>}
      {none.action}
    </View>}
    {children}
  </ScrollView>;
  const chips = scopes && <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0}} contentContainerStyle={[edge, {gap: theme.space.xs, alignItems: 'center'}]}>{scopes}</ScrollView></TVRow>;
  // TV: the results column runs to the window's right edge and starts a gutter early, so its rows and sections, which
  // keep their own gutters, line up with the column's edge.
  if (tv) return <View testID={testID} style={{flex: 1, flexDirection: 'row', paddingTop: frame.titleTop, paddingLeft: frame.gutter}}>
    <View style={{width: searchSpec.keyboardWidth, gap: frame.headingGap}}>{field}{keyboard}</View>
    <View style={{flex: 1, minWidth: 0, marginLeft: frame.sectionGap - frame.gutter, gap: frame.headingGap}}>{chips}{results}</View>
  </View>;
  return <View testID={testID} style={{flex: 1}}>
    <View style={{paddingTop: top, gap: frame.headingGap}}>
      {title !== undefined && frame.form === 'phone' && <Text accessibilityRole="header" style={[theme.type.pageTitle, edge, {color: theme.colors.text}]}>{title}</Text>}
      <View style={[edge, {flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}]}><View style={{flex: 1, minWidth: 0}}>{field}</View>{cancel}</View>
      {chips}
    </View>
    {results}
  </View>;
}
