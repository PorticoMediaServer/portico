import React, {useState, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ReadingItem} from '../../../../../packages/client-core/src';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import type {NativeReadingActionOffer} from '../../screens/read/reading-actions';
import type {NativeShellRuntime} from '../../shell/runtime';
import {AmbientArt, type AmbientArtSource} from '../artwork';
import type {CardSelection} from '../cards';
import {WindowedGrid} from '../layout';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {VolumeCard} from '../reading/VolumeCard';
import {LoadingPaint, SkeletonBlock} from '../states';
import {textLine, useTheme} from '../theme';

export function ReadingSeriesFrame({testID, title, back, backdrop, collection, getKey, ready, immediate, error, hero, selection, heading,
  count, notice, continuedId, progression, comic, runtime, open, offer, pick, onMenu, menu}: {
  testID: string; title: string; back: TopBarBack; backdrop?: AmbientArtSource; collection: WindowedCollection<ReadingItem>;
  getKey: (item: ReadingItem) => string; ready: boolean; immediate: boolean; error?: ReactNode;
  hero: (onHeight: (height: number) => void) => ReactNode; selection?: ReactNode; heading: string; count?: string;
  notice?: ReactNode; continuedId?: string; progression?: number; comic: boolean;
  runtime: NativeShellRuntime; open: (item: ReadingItem) => void; offer: (item: ReadingItem) => NativeReadingActionOffer | undefined;
  pick: (offer: NativeReadingActionOffer) => CardSelection; onMenu: (offer: NativeReadingActionOffer) => void; menu?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), [paneWidth, setPaneWidth] = useState(theme.width);
  const [heroHeight, setHeroHeight] = useState(0), [collapsed, setCollapsed] = useState(false);
  const gap = theme.space.controlGap, gutter = theme.gutter, columns = theme.platform === 'phone' ? 3 : 5;
  const width = Math.max(1, Math.floor((Math.min(900, paneWidth) - gutter * 2 - gap * (columns - 1)) / columns));
  const height = width * 1.5, caption = textLine(theme.type.cardTitle) * 2 + textLine(theme.type.caption) * 2 + theme.space.sm;
  return <View testID={testID} style={{flex: 1}} onLayout={event => setPaneWidth(event.nativeEvent.layout.width)}>
    {backdrop && <AmbientArt source={backdrop} treatment="title" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 560}}/>}
    <View pointerEvents="box-none" style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: theme.layers.sticky}}>
      <TopBar overArtwork surface={collapsed} back={back} title={collapsed ? title : undefined}/>
    </View>
    {error ?? <LoadingPaint ready={ready} immediate={immediate} testID="native-read-series-paint"
      skeleton={<SkeletonBlock width="100%" height={360}/>} style={{flex: 1}}>
      <WindowedGrid collection={collection} getKey={getKey} itemWidth={width} itemHeight={height + caption}
        gap={gap} rowGap={theme.space.sm} gutter={gutter} rightGutter={gutter} paddingBottom={insets.bottom + theme.space.xl}
        style={{flex: 1, width: '100%', maxWidth: 900, alignSelf: 'center'}}
        onScrollOffset={offset => setCollapsed(heroHeight > 0 && offset > heroHeight - theme.space.xl)}
        header={<View style={{paddingTop: insets.top + theme.space.xl}}>
          <View style={{paddingHorizontal: gutter}}>{hero(setHeroHeight)}</View>
          {selection}
          <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap, marginVertical: theme.space.sm, paddingHorizontal: gutter}}>
            <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{heading}</Text>
            {count !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{count}</Text>}
          </View>
          {notice}
        </View>}
        renderPlaceholder={() => <SkeletonBlock width={width} height={height} radius={theme.baseRadii.control}/>}
        renderItem={item => {
          const offered = offer(item);
          return <VolumeCard item={item} width={width} height={height} comic={comic} theme={theme} runtime={runtime}
            progression={continuedId === item.id ? progression : undefined} onOpen={() => open(item)}
            selection={offered ? pick(offered) : undefined} onMenu={offered ? anchorRef => onMenu({...offered, anchorRef}) : undefined}/>;
        }}/>
    </LoadingPaint>}
    {menu}
  </View>;
}
