import React, {type ReactNode} from 'react';
import {FlatList, ScrollView, Text, View} from 'react-native';
import type {PlayHistoryEntry} from '../../../../../packages/client-core/src/play-history';
import {useFrameMetrics} from '../frames/metrics';
import {TVRow} from '../layout/TVRow';
import {SettingsRow} from '../shell';
import {useTheme} from '../theme';

export function SettingsHistoryToolbar({search, actions, filters, children}: {
  search: ReactNode; actions: ReactNode; filters: readonly ReactNode[]; children: ReactNode;
}) {
  const frame = useFrameMetrics(), theme = useTheme(), wide = frame.form !== 'phone';
  return <View style={{marginHorizontal: frame.cardInset, gap: theme.space.sm, paddingVertical: theme.space.sm}}>
    <View style={{flexDirection: wide ? 'row' : 'column', gap: theme.space.sm, alignItems: wide ? 'flex-end' : undefined}}>
      <View style={wide ? {flex: 1} : undefined}>{search}</View>
      <View style={wide ? {flex: 1} : undefined}>{actions}</View>
    </View>
    <View style={{flexDirection: wide ? 'row' : 'column', flexWrap: wide ? 'wrap' : undefined, gap: theme.space.xs}}>
      {filters.map((control, index) => <View key={index} style={wide ? {flexGrow: 1, flexBasis: 205} : undefined}>{control}</View>)}
    </View>
    {children}
  </View>;
}

type Column = Readonly<{id: string; label: string; weight: number}>;
type HistoryRow = Readonly<{title: string; description: readonly string[]; columns: Readonly<Record<string, string | undefined>>}>;

export function SettingsHistoryList({entries, row, columns, header, empty, footer}: {
  entries: readonly PlayHistoryEntry[];
  row: (entry: PlayHistoryEntry) => HistoryRow;
  columns: readonly Column[];
  header: ReactNode; empty: ReactNode; footer: ReactNode;
}) {
  const frame = useFrameMetrics(), theme = useTheme(), wide = frame.form !== 'phone';
  const tableRow = (values?: HistoryRow['columns']) => <View testID={values ? undefined : 'server-history-column-header'}
    style={{flexDirection: 'row', backgroundColor: values ? theme.colors.raised : theme.colors.raisedHigh,
      paddingHorizontal: theme.space.sm, paddingVertical: theme.space.sm, gap: theme.space.xs,
      borderBottomColor: theme.colors.lineSoft, borderBottomWidth: theme.space.hairline}}>
    {columns.map(column => <Text key={column.id} accessibilityRole={values ? undefined : 'header'}
      android_hyphenationFrequency="none" style={[values && column.id === 'title' ? theme.type.rowLabel : theme.type.rowHelp,
        {flex: column.weight, color: values && column.id === 'title' ? theme.colors.text : theme.colors.textSecondary}]}>
      {values ? values[column.id] : column.label}
    </Text>)}
  </View>;
  const list = <FlatList testID="server-history-list" data={entries} keyExtractor={entry => entry.id} keyboardShouldPersistTaps="handled"
    contentContainerStyle={{paddingBottom: frame.bottomClear}}
    ListHeaderComponent={wide ? undefined : <>{header}</>} ListEmptyComponent={<>{empty}</>}
    ListFooterComponentStyle={{paddingTop: frame.sectionGap}} ListFooterComponent={<>{footer}</>}
    renderItem={({item, index}) => {
      const value = row(item);
      return wide ? tableRow(value.columns) :
        <SettingsRow first={index === 0} label={value.title} description={value.description} style={{marginHorizontal: frame.cardInset}}/>;
    }}/>;
  if (!wide) return list;
  return <View style={{flex: 1, minHeight: 0}}>
    {header}
    <TVRow>
      <ScrollView horizontal contentContainerStyle={{flexGrow: 1}} style={{marginHorizontal: frame.cardInset, flex: 1}}>
        <View style={{flex: 1, minWidth: 640, minHeight: 0}}>{tableRow()}{list}</View>
      </ScrollView>
    </TVRow>
  </View>;
}
