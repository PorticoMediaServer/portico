import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {GridSkeleton, SkeletonBlock, isSlowLoading, showSkeleton, skeletonTiming} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';

/** Keeps the actual header in place; only the arriving Saved body has placeholder geometry. */
export function SavedTVLoading({shape = 'posters', inset = false}: {shape?: 'posters' | 'squares' | 'history' | 'views'; inset?: boolean}) {
  const theme = useTheme(), [elapsed, setElapsed] = useState(0), [width, setWidth] = useState(1624);
  useEffect(() => {
    const start = Date.now();
    const delay = setTimeout(() => setElapsed(Date.now() - start), skeletonTiming.delayMs);
    const slow = setTimeout(() => setElapsed(Date.now() - start), skeletonTiming.slowMs);
    return () => {clearTimeout(delay); clearTimeout(slow);};
  }, []);
  const directory = shape === 'squares' || shape === 'views', itemWidth = shape === 'views' ? 520 : directory ? 236 : 200;
  const gap = shape === 'views' ? 32 : directory ? 34 : 32, columns = Math.max(1, Math.floor((width + gap) / (itemWidth + gap)));
  return <View testID="saved-tv-loading" onLayout={event => {if (event.nativeEvent.layout.width > 0) setWidth(event.nativeEvent.layout.width - (inset ? theme.gutter + theme.layout.gutter.tvRight : 0));}}
    style={inset ? {paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight} : undefined}>
    {shape === 'history' ? showSkeleton(elapsed) && <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{gap: 18}}>
      {[0, 1, 2, 3, 4].map(index => <View key={index} style={{height: 104, flexDirection: 'row', alignItems: 'center', gap: 26, paddingLeft: 14, paddingRight: 28}}>
        <SkeletonBlock width={156} height={88} radius={10} shine={index === 0}/>
        <View style={{flex: 1, gap: 10}}><SkeletonBlock width="64%" height={28}/><SkeletonBlock width="42%" height={22}/></View>
        <SkeletonBlock width={240} height={21}/><SkeletonBlock width={120} height={23}/>
      </View>)}
    </View> : shape === 'views' ? showSkeleton(elapsed) && <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', gap: 32}}>
      {Array.from({length: columns}, (_, index) => <SkeletonBlock key={index} width={520} height={300} radius={16} shine={index === 0}/>)}
    </View> : <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={640} width={width} totalCount={columns * 2}
      columns={columns} cardWidth={itemWidth} shape={shape === 'squares' ? 'square' : 'poster'} gap={gap} rowGap={directory ? 44 : 40} captionLines={2}/>}
    {isSlowLoading(elapsed) && <Text accessibilityLiveRegion="polite" style={[browseType(theme, 22), {color: theme.colors.textTertiary, marginTop: 24}]}>{defaultI18n.t('next.states.stillLoading')}</Text>}
  </View>;
}
