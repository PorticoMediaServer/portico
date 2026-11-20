import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {useTheme} from '../theme';
import {TVRow} from '../layout/TVRow';
import {useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {SplitFrame, type FramePane} from './SplitFrame';

export type LiveFrameProps = Readonly<{
  /** Live TV, or Recordings when it is a page of its own (a phone's Recordings, phone-app 12). */
  title: string;
  /** A phone's Recordings page: Back to Live TV, with the title centred in the bar. */
  back?: TopBarBack;
  /** The source picker beside the title: "All channels ⌄". */
  switcher?: ReactNode;
  /** Desktop width: the source's name, its channel count and how far the guide reaches, at the title row's end. */
  facts?: ReactNode;
  /** The title row's own buttons: a phone's List | Guide switch and Recordings; a tablet's Grid | List; TV's Today, Now, Recordings. */
  actions?: ReactNode;
  /** Guide | Recordings on a tablet and the web; Upcoming | Recorded | Series rules | History on a phone's Recordings page. The kit `Tabs`. */
  tabs?: ReactNode;
  /** The channel groups with their counts (All 14, Favorites 3, News 2 …), or Recordings' views: one line that scrolls sideways. */
  chips?: ReactNode;
  /** Desktop width: On now, the row of what is playing, before the guide. It scrolls with the guide. */
  lead?: ReactNode;
  /** The day, the 2 h steps and Now, the sort, Grid | List; Recordings' storage. */
  toolbar?: ReactNode;
  /** The one flexible region: the guide grid, the channel list, Upcoming, Recorded, Series rules. */
  children?: ReactNode;
  /** In place of `children` and `lead`, a guide or list that scrolls itself: a virtualized list cannot sit
   * inside the frame's scroller. The frame gives it the room under the head. */
  list?: ReactNode;
  /** A programme's details, or the rule being edited: beside the page from tablet width, a sheet on a phone. */
  details?: FramePane;
  /** The keyboard line under the guide (desktop); TV's key hints (kit `KeyHints`). */
  hints?: ReactNode;
  /** The whole page can't load. */
  state?: PageStateProps;
  footer?: ReactNode;
  overlay?: ReactNode;
  testID?: string;
}>;

/** Live TV (desktop-live, phone-guide, phone-app 10 to 12, tablet-play 6 and 7, final3-tv-10): the guide, the channel
 * list and Recordings share one frame. Fixed: the title row (title, the source picker, its facts and buttons), the tabs,
 * the chips and the toolbar. Flexible: the guide or the list under them. The details pane is `SplitFrame`'s. */
export function LiveFrame({title, back, switcher, facts, actions, tabs, chips, lead, toolbar, children, list, details, hints, state, footer, overlay, testID}: LiveFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), {form} = frame;
  const edge = {paddingLeft: frame.gutter, paddingRight: frame.rightGutter};
  // On a phone the source picker heads the chips, so the title row keeps List | Guide beside the title (phone-guide g1, as the web).
  const chipsLine = (inRow: boolean) => (chips || form === 'phone' && switcher) && <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0}} contentContainerStyle={[inRow ? undefined : edge, {gap: theme.space.xs, alignItems: 'center'}]}>{form === 'phone' && !back && switcher}{chips}</ScrollView></TVRow>;
  // A phone on its side (600 wide or more, under 600 tall): the title, its buttons, the chips and the toolbar share one row
  // under the status bar and the tabs and facts give way, so the guide keeps the height (as the web's).
  const shortWide = form === 'phone' && !back && window.width >= 600;
  const titleRow = shortWide
    ? <View style={{paddingTop: insets.top + theme.space.xs, paddingLeft: Math.max(frame.gutter, insets.left), paddingRight: Math.max(frame.rightGutter, insets.right), flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      {actions}
      <View style={{flex: 1, minWidth: 0}}>{chipsLine(true)}</View>
      {toolbar}
    </View>
    : back && form === 'phone'
    ? <View style={{paddingTop: insets.top}}><TopBar safeArea={false} back={back} center={<Text accessibilityRole="header" numberOfLines={2} style={[theme.type.barTitle, {color: theme.colors.text, textAlign: 'center'}]}>{title}</Text>}/></View>
    // A phone's title row holds the title, List | Guide and Recordings on one line (final3-phone-07): its items sit the small gap apart.
    : <View style={[edge, {paddingTop: Math.max(frame.titleTop, insets.top + frame.headerGap), flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: form === 'phone' ? theme.space.xs : theme.space.sm}]}>
      <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, flexShrink: 1}]}>{title}</Text>
      {form !== 'phone' && switcher}
      <View style={{flex: 1}}/>
      {form === 'expanded' && facts}
      {actions}
    </View>;
  const header = shortWide ? <View style={{paddingBottom: theme.space.xs}}>{titleRow}</View> : <View style={{gap: frame.headingGap, paddingBottom: frame.headingGap}}>
    {titleRow}
    {tabs}
    {chipsLine(false)}
    {toolbar && form !== 'tv' && <View style={[edge, {flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', justifyContent: form === 'phone' ? 'space-between' : undefined, gap: theme.space.xs}]}>{toolbar}</View>}
  </View>;
  const page = <View testID={testID} style={{flex: 1}}>
    {header}
    {state ? <PageStateFrame {...state}/> : list ? <>
      <View style={{flex: 1}}>{list}</View>
      {hints && <View style={edge}>{hints}</View>}
    </> : <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: frame.bottomClear, gap: frame.sectionGap}}>
      {lead}
      {children}
      {hints && <View style={edge}>{hints}</View>}
    </ScrollView>}
    {footer}
    {overlay}
  </View>;
  return <SplitFrame pane={details}>{page}</SplitFrame>;
}
