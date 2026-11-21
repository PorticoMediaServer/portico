import React from 'react';
import {FlatList, Text} from 'react-native';
import {Focusable} from '../controls';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export type LicenceRow = Readonly<{id: string; title: string; body?: string; componentId?: string; heading?: boolean}>;

export function LicenceRows({rows, onSelect}: {rows: readonly LicenceRow[]; onSelect: (id: string) => void}) {
  const theme = useTheme(), frame = useFrameMetrics(), first = rows.find(row => !row.heading)?.id;
  const ring = theme.isTV ? theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv : 0;
  return <FlatList<LicenceRow> data={rows} keyExtractor={row => row.id} initialNumToRender={10} maxToRenderPerBatch={10} windowSize={5}
    contentContainerStyle={{paddingHorizontal: frame.gutter + ring, paddingBottom: frame.bottomClear}}
    renderItem={({item}) => {
      if (item.heading) return <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.textSecondary, marginVertical: theme.space.md}]}>{item.title}</Text>;
      const id = item.componentId;
      return <Focusable testID={`license-${encodeURIComponent(item.id)}`} accessibilityRole={id ? 'button' : 'text'}
        accessibilityLabel={[item.title, item.body].filter(Boolean).join(', ')}
        preferredFocus={theme.isTV && item.id === first} onPress={id ? () => onSelect(id) : undefined}
        focusKind="row" focusRadius={theme.baseRadii.control}
        style={{padding: theme.space.md, borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.raised, marginBottom: theme.space.xs}}>
        {item.title !== '' && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{item.title}</Text>}
        {item.body && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginTop: item.title ? theme.space.xs : 0}]}>{item.body}</Text>}
      </Focusable>;
    }}/>;
}
