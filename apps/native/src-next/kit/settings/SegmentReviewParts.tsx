import React from 'react';
import {Text, View} from 'react-native';
import type {ArtworkSource} from '../artwork/types';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {ArtBox} from '../frames/parts';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export function SegmentReviewTitle({title, facts, artwork, first, onPress}: {
  title: string; facts: readonly string[]; artwork?: ArtworkSource | null; first: boolean; onPress: () => void;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable accessibilityRole="button" accessibilityLabel={title} accessibilityHint={facts.join(', ')} onPress={onPress}
    style={{flexDirection: 'row', alignItems: 'center', paddingHorizontal: frame.cardPadding, paddingVertical: theme.space.xs,
      gap: theme.space.controlGap, borderTopWidth: first ? 0 : theme.space.hairline, borderTopColor: theme.colors.lineSoft}}>
    <ArtBox kind="poster" icon="i-film" width={theme.targets.touch} source={artwork}/>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{facts.join(' · ')}</Text>
    </View>
    <Icon name="i-right" size={theme.type.rowLabel.fontSize} color={theme.colors.textTertiary}/>
  </Focusable>;
}

export function SegmentReviewInset({children}: {children: React.ReactNode}) {
  const frame = useFrameMetrics();
  return <View style={{paddingHorizontal: frame.cardPadding, paddingVertical: frame.cardPadding}}>{children}</View>;
}

export function SegmentReviewNotice({text}: {text: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text accessibilityLiveRegion="polite" style={[theme.type.rowHelp, {marginHorizontal: frame.cardInset, color: theme.colors.text}]}>{text}</Text>;
}
