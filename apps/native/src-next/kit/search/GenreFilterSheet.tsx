import React from 'react';
import {View} from 'react-native';
import type {BrowseCapabilities, BrowseNode, BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {FilterBuilder, type BrowseLabels} from '../browse';
import {Button} from '../controls';
import {BottomSheet} from '../overlays';
import {StateView} from '../states';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLibraryFilterDraft} from '../../screens/library/filters';
import {useGenreFilters} from '../../pages/shared/search/useGenreFilters';

/** A genre page's Filters sheet: Clear all, the filter fields, and Show results with the live count. */
export function GenreFilters(props: Readonly<{
  draft: NativeLibraryFilterDraft; capabilities: BrowseCapabilities; sort: BrowseSortSelection; fixed: readonly BrowseNode[];
  runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; onClose: () => void;
}>) {
  const theme = useTheme(), view = useGenreFilters(props), {t, state} = view;
  const footer = <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>
    <Button focusId="apply" testID="genre-filters-apply" variant="primary" disabled={state.count.phase !== 'ready'} onPress={view.apply}>{view.resultLabel}</Button>
  </View>;
  return <BottomSheet testID="search-genre-filters" open={state.open} onOpenChange={open => {if (!open) view.close();}}
    title={t('next.browse.native.filters')} detents={theme.platform === 'phone' ? [580, 698] : [580, 780]}
    defaultDetent={theme.platform === 'phone' ? 1 : undefined} showClose={false} scrollable={false} footer={footer}
    headerAction={<Button focusId="clear" variant="text" onPress={view.clear}>{t('next.browse.native.clearAll')}</Button>}>
    {view.problem && <StateView {...view.problem}/>}
    <FilterBuilder testID={`${view.prefix}-fields`} model={view.model} labels={props.labels} focusPrefix={view.prefix} fields={view.fields} openFields={view.openFields}
      onFieldOpenChange={view.fieldOpenChange} onFieldChange={view.fieldChange} onSearchChange={view.search} onRetry={view.retry}/>
  </BottomSheet>;
}
