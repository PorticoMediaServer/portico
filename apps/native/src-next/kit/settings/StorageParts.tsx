import React from 'react';
import {Text, View} from 'react-native';
import {Progress} from '../cards/primitives';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export function StorageLocation({path}: {path: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text selectable style={[theme.type.rowHelp, {color: theme.colors.textSecondary, paddingHorizontal: frame.cardPadding,
    paddingBottom: theme.space.sm}]}>{path}</Text>;
}

export function StorageSpaceRow({name, size, help, share}: {name: string; size: string; help?: string; share?: number}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{padding: frame.cardPadding, gap: theme.space.xs, borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.lineSoft}}>
    <View style={{flexDirection: 'row', justifyContent: 'space-between', gap: theme.space.controlGap}}>
      <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text}]}>{name}</Text>
      <Text style={[theme.type.rowHelp, {flexShrink: 1, color: theme.colors.textSecondary}]}>{size}</Text>
    </View>
    {share !== undefined && <Progress value={share}/>}
    {help && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{help}</Text>}
  </View>;
}
