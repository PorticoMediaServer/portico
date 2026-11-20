import React from 'react';
import {Text, View} from 'react-native';
import type {ArtworkSource} from '../artwork/types';
import {ArtBox} from '../frames/parts';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export function SegmentReviewTitle({title, artwork}: {title: string; artwork?: ArtworkSource | null}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flexDirection: 'row', alignItems: 'center', padding: frame.cardPadding, gap: theme.space.controlGap}}>
    <ArtBox kind="poster" icon="i-film" width={frame.rowTwoLine} source={artwork}/>
    <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text}]}>{title}</Text>
  </View>;
}

export function SegmentReviewNotice({text}: {text: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text accessibilityLiveRegion="polite" style={[theme.type.rowHelp, {marginHorizontal: frame.cardInset, color: theme.colors.text}]}>{text}</Text>;
}
