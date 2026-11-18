import React, {type ReactNode} from 'react';
import {ScrollView, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PageHeader, type PageHeaderProps} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';

export type BrowseFrameProps = Readonly<{
  /** The rows or the grid: the one flexible region. Each child is a kit `Row` (it draws its own heading) or a grid;
   * the frame spaces them. A row with no items is left out whole. */
  children?: ReactNode;
  /** The whole page is loading for the first time, or can't load: drawn in place of the rows. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
  testID?: string;
}> & (
  /** A destination or a pushed list: its title is the header. */
  | Readonly<{header: PageHeaderProps; hero?: never}>
  /** Home: the hero leads and the shell's wordmark bar sits over it. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, Live TV, Saved, Search results, any page of rows of cards or a grid under one
 * header. The frame owns the header, the safe area, the space between rows and the clearance at the bottom (rows
 * take the theme's gutter themselves); the shell owns the tab bar, sidebar or rail around it, and the window owns the backdrop. */
export function BrowseFrame({header, hero, state, children, testID}: BrowseFrameProps) {
  const frame = useFrameMetrics(), insets = useSafeAreaInsets();
  // Home with nothing to feature keeps the wordmark bar's room, so the first row never sits under it.
  const top = header ? <PageHeader {...header}/> : hero ?? <View style={{height: frame.form === 'tv' ? frame.titleTop : insets.top + frameSpec.header.wordmarkBar}}/>;
  if (state && 'title' in state) return <View testID={testID} style={{flex: 1}}>{top}<PageStateFrame {...state}/></View>;
  return <ScrollView testID={testID} style={{flex: 1}} scrollEnabled={!state} contentContainerStyle={{paddingBottom: frame.bottomClear}}>
    {top}
    {/* After the hero the first row starts at the rule's own offset from the art's bottom edge. */}
    <View style={{gap: frame.sectionGap, marginTop: hero ? frameSpec.hero.home.rowOffset[frame.form] : 0}}>{state ? <RowsSkeleton/> : children}</View>
  </ScrollView>;
}

/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const theme = useTheme(), frame = useFrameMetrics();
  const card = theme.spec.card, gap = theme.spec.shelf.gap[theme.platform];
  const row = (kind: 'wide' | 'poster', shine: boolean) => {
    const width = card.width[kind][theme.platform], height = width / card.aspect[kind], count = Math.ceil(frame.width / (width + gap));
    return <View style={{paddingLeft: frame.gutter}}>
      <SkeletonBlock width={width} height={theme.type.rowHeading.fontSize} style={{marginBottom: frame.headingGap}}/>
      <View style={{flexDirection: 'row', gap, overflow: 'hidden'}}>{Array.from({length: count}, (_, index) => <SkeletonBlock key={index} width={width} height={height} radius={theme.radii.artwork} shine={shine && index === 0}/>)}</View>
    </View>;
  };
  return <>{row('wide', true)}{row('poster', false)}</>;
}
