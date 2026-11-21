import React, {type ReactElement, type ReactNode} from 'react';
import {FlatList, View} from 'react-native';
import {useFrameMetrics} from '../frames/metrics';

export function SettingsDataList<T>({items, itemKey, row, header, footer, empty, testID}: {
  items: readonly T[]; itemKey: (item: T) => string; row: (item: T, index: number) => ReactElement | null;
  header?: ReactNode; footer?: ReactNode; empty?: ReactNode; testID?: string;
}) {
  const frame = useFrameMetrics();
  return <FlatList data={items} keyExtractor={itemKey} testID={testID} keyboardShouldPersistTaps="handled"
    contentContainerStyle={{paddingBottom: frame.bottomClear}}
    ListHeaderComponent={header != null ? <View style={{gap: frame.sectionGap, paddingVertical: frame.sectionGap}}>{header}</View> : null}
    ListEmptyComponent={empty != null ? <>{empty}</> : null}
    ListFooterComponent={footer != null ? <View style={{gap: frame.sectionGap, paddingTop: frame.sectionGap}}>{footer}</View> : null}
    renderItem={({item, index}) => row(item, index)}/>;
}
