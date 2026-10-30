import React, {useEffect, useState, type ReactNode} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {LoadingBoundary, SkeletonBlock, skeletonTiming} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';

/** A page-shaped quiet load, followed by the shared eight-second notice. */
export function ReadingLoading({ready, skeleton, children, style}: {ready: boolean; skeleton: ReactNode; children?: ReactNode; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme(), [slow, setSlow] = useState(false);
  useEffect(() => {
    setSlow(false);
    if (ready) return;
    const timer = setTimeout(() => setSlow(true), skeletonTiming.slowMs);
    return () => clearTimeout(timer);
  }, [ready]);
  return <View style={style}>
    <LoadingBoundary ready={ready} immediate={ready} skeleton={skeleton} style={{flex: 1}}>{children}</LoadingBoundary>
    {!ready && slow ? <Text accessibilityLiveRegion="polite" style={[browseType(theme, 13, 500), {color: theme.colors.textTertiary, padding: 20}]}>{defaultI18n.t('next.states.stillLoading')}</Text> : null}
  </View>;
}

export function ReadingGridSkeleton({width, columns = 3, gap = 16, gutter = 20, rows = 2}: {width: number; columns?: number; gap?: number; gutter?: number; rows?: number}) {
  return <View style={{paddingHorizontal: gutter, paddingTop: 20, gap: 30}}>
    {Array.from({length: rows}, (_, row) => <View key={row} style={{flexDirection: 'row', gap}}>
      {Array.from({length: columns}, (_, column) => <View key={column} style={{width, gap: 7}}>
        <SkeletonBlock width={width} height={width * 1.5} radius={10}/><SkeletonBlock width="86%" height={14}/><SkeletonBlock width="58%" height={11}/>
      </View>)}
    </View>)}
  </View>;
}
