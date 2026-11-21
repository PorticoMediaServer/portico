import React, {type ReactNode} from 'react';
import {FlatList, ScrollView, Text, View} from 'react-native';
import type {PlayHistoryEntry} from '../../../../../packages/client-core/src/play-history';
import {useFrameMetrics} from '../frames/metrics';
import {TVRow} from '../layout/TVRow';
import {SettingsRow} from '../shell';
import {useTheme} from '../theme';
import {Button, Chip, Select, type SelectProps} from '../controls';
import {BottomSheet} from '../overlays';
import {ChipRail} from '../browse/Toolbar';

export function SettingsHistoryFilter({rowLabel, ...props}: SelectProps) {
  const frame = useFrameMetrics(), theme = useTheme();
  return <Select {...props} rowLabel={frame.form === 'phone' ? undefined : rowLabel}
    style={frame.form === 'phone' ? {minWidth: 0, height: theme.targets.touch, borderRadius: theme.baseRadii.pill,
      paddingHorizontal: theme.space.sm, gap: theme.space.xs} : undefined}/>;
}

export function SettingsHistoryToolbar({search, actions, filters, sort, sortLabel, doneLabel, children}: {
  search: ReactNode; actions: ReactNode; filters: readonly ReactNode[]; sort: readonly ReactNode[];
  sortLabel: string; doneLabel: string; children: ReactNode;
}) {
  const frame = useFrameMetrics(), theme = useTheme(), wide = frame.form !== 'phone';
  const [sortOpen, setSortOpen] = React.useState(false);
  return <View style={{marginHorizontal: frame.cardInset, gap: theme.space.sm, paddingVertical: theme.space.sm}}>
    {wide ? <>
      <View style={{flexDirection: 'row', gap: theme.space.sm, alignItems: 'flex-end'}}>
        <View style={{flex: 1}}>{search}</View>
        <View style={{flex: 1}}>{actions}</View>
      </View>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
        {[...filters, ...sort].map((control, index) => <View key={index} style={{flexGrow: 1, flexBasis: 205}}>{control}</View>)}
      </View>
    </> : <>
      {search}
      <ChipRail>{filters}<Chip testID="server-history-sort-menu" onPress={() => setSortOpen(true)}>{sortLabel}</Chip></ChipRail>
      <BottomSheet open={sortOpen} title={sortLabel} onOpenChange={setSortOpen} fitContent
        footer={<Button onPress={() => setSortOpen(false)}>{doneLabel}</Button>}>
        <View style={{paddingHorizontal: frame.cardInset, gap: theme.space.sm}}>{sort}</View>
      </BottomSheet>
    </>}
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
