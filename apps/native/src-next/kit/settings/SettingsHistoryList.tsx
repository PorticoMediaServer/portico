import React, {type ReactNode} from 'react';
import {FlatList, View} from 'react-native';
import type {PlayHistoryEntry} from '../../../../../packages/client-core/src/play-history';
import {useFrameMetrics} from '../frames/metrics';
import {SettingsRow} from '../shell';

export function SettingsHistoryList({entries, row, header, empty, footer}: {
  entries: readonly PlayHistoryEntry[];
  row: (entry: PlayHistoryEntry) => {title: string; description: readonly string[]};
  header: ReactNode; empty: ReactNode; footer: ReactNode;
}) {
  const frame = useFrameMetrics();
  return <FlatList testID="server-history-list" data={entries} keyExtractor={entry => entry.id} keyboardShouldPersistTaps="handled"
    contentContainerStyle={{paddingBottom: frame.bottomClear}}
    ListHeaderComponentStyle={{paddingVertical: frame.sectionGap}}
    ListHeaderComponent={<View style={{gap: frame.sectionGap}}>{header}</View>} ListEmptyComponent={<>{empty}</>}
    ListFooterComponentStyle={{paddingTop: frame.sectionGap}} ListFooterComponent={<>{footer}</>}
    renderItem={({item, index}) => {
      const {title, description} = row(item);
      return <SettingsRow first={index === 0} label={title} description={description} style={{marginHorizontal: frame.cardInset}}/>;
    }}/>;
}
