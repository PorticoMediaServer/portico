import React, {useEffect, useState} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {LoadingBoundary, SkeletonBlock} from '../../kit/states';
import {useTheme} from '../../kit/theme';

/** One static page outline, with the same delayed paint as the rest of the app. */
export function SavedLoading({kind = 'grid', columns = 3}: {kind?: 'grid' | 'directory' | 'resource' | 'shelf'; columns?: number}) {
  const theme = useTheme(), window = useWindowDimensions(), [slow, setSlow] = useState(false);
  useEffect(() => {const timer = setTimeout(() => setSlow(true), 8000); return () => clearTimeout(timer);}, []);
  const width = Math.max(1, (window.width - theme.gutter * 2 - (columns - 1) * 10) / columns);
  const outline = <View style={{paddingHorizontal: theme.gutter, paddingTop: 16, gap: 16}}>
    {kind === 'resource' && <View style={{alignItems: 'center', gap: 14, paddingTop: 32}}>
      <SkeletonBlock width={152} height={152} radius={14}/><SkeletonBlock width="65%" height={32}/><SkeletonBlock width="40%" height={14}/>
      <SkeletonBlock width="100%" height={48} radius={24}/>
    </View>}
    {kind !== 'shelf' && <View style={{flexDirection: 'row', gap: 10}}><SkeletonBlock width={110} height={34} radius={10}/><SkeletonBlock width={94} height={34} radius={10}/></View>}
    {kind === 'directory' || kind === 'resource' ? Array.from({length: 5}, (_, index) => <View key={index} style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
      <SkeletonBlock width={64} height={64} radius={10}/><View style={{flex: 1, gap: 8}}><SkeletonBlock width="65%" height={16}/><SkeletonBlock width="40%" height={12}/></View>
    </View>) : <View style={{flexDirection: 'row', flexWrap: kind === 'shelf' ? 'nowrap' : 'wrap', gap: 10, overflow: 'hidden'}}>
      {Array.from({length: columns * (kind === 'shelf' ? 1 : 2)}, (_, index) => <View key={index} style={{width, gap: 9}}>
        <SkeletonBlock width={width} height={width * 1.5} radius={8}/><SkeletonBlock width="70%" height={12}/>
      </View>)}
    </View>}
  </View>;
  return <View><LoadingBoundary ready={false} skeleton={outline}/>
    {slow && <Text accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textTertiary, padding: theme.gutter}]}>{defaultI18n.t('next.states.stillLoading')}</Text>}
  </View>;
}

export function SavedTail({width, square = false, row = false}: {width?: number; square?: boolean; row?: boolean}) {
  return <View testID="saved-continuation" accessible={false} style={{width, padding: row ? 12 : 0, gap: 9}}>
    <SkeletonBlock width={width ?? '100%'} height={row ? 64 : (width ?? 110) * (square ? 1 : 1.5)} radius={8}/>
    {!row && <SkeletonBlock width="65%" height={12}/>}</View>;
}
