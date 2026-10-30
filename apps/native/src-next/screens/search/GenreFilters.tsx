import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import type {BrowseCapabilities, BrowseNode, BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {searchGenreBrowsePivot} from '../../../../../packages/client-core/src/search-genres';
import {FilterBuilder, type BrowseLabels, type FilterFieldState} from '../../kit/browse';
import {Button} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLibraryFilterDraft} from '../library/filters';
import {libraryFilterChange, libraryFilterField} from '../library/filter-controls';

/** The existing filter-sheet parts, backed by the real across-video source.
 * It has no fabricated Library snapshot or unsupported saved-view mutation. */
export function GenreFilters({draft, capabilities, sort, fixed, runtime, labels, owner, onClose}: Readonly<{
  draft: NativeLibraryFilterDraft; capabilities: BrowseCapabilities; sort: BrowseSortSelection; fixed: readonly BrowseNode[];
  runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; onClose: () => void;
}>) {
  const t = runtime.text.t, locale = runtime.text.locale;
  const theme = useTheme(), state = useSyncExternalStore(draft.subscribe, draft.getSnapshot);
  const [openFields, setOpenFields] = useState<readonly string[]>([]), [failure, setFailure] = useState<unknown>(), [now] = useState(() => new Date());
  const model = useMemo(() => browseToolbarModel(capabilities, capabilities.library.kind, state.predicates, sort,
    {t, locale, owner, textEntry: true}), [capabilities, state.predicates, sort, t, locale, owner]);
  const fields = Object.fromEntries(model.groups.flatMap(group => group.fields.map(field =>
    [field.id, libraryFilterField(field, state.predicates, state.facets[field.id], now)]))) as Readonly<Record<string, FilterFieldState>>;
  const prefix = JSON.stringify(['search-genre-filters', runtime.getSnapshot().authority, fixed]);
  useEffect(() => {draft.refresh(); return () => draft.dispose();}, [draft]);
  useEffect(() => {draft.setOpenFields(openFields);}, [draft, openFields]);
  useEffect(() => {if (!state.open) onClose();}, [onClose, state.open]);
  const close = () => {draft.dispose(); onClose();};
  const result = state.count.value;
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button focusId="apply" testID="genre-filters-apply" variant="primary" disabled={state.count.phase !== 'ready'} onPress={() => {if (draft.apply()) close();}}>
      {result ? t('next.browse.native.showResults', {results: t(result.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: result.total, pivot: searchGenreBrowsePivot})}) : null}
    </Button>
  </View>;
  return <BottomSheet testID="search-genre-filters" open={state.open} onOpenChange={open => {if (!open) close();}}
    title={t('next.browse.native.filters')} detents={theme.platform === 'phone' ? [580, 698] : [580, 780]}
    defaultDetent={theme.platform === 'phone' ? 1 : undefined} showClose={false} scrollable={false} footer={footer}
    headerAction={<Button focusId="clear" variant="text" onPress={draft.clear}>{t('next.browse.native.clearAll')}</Button>}>
    {(failure || state.count.phase === 'error') && <StateView {...runtime.error(failure ?? state.count.error, () => {setFailure(undefined); draft.refresh();})}/>}
    <FilterBuilder testID={`${prefix}-fields`} model={model} labels={labels} focusPrefix={prefix} fields={fields} openFields={openFields}
      onFieldOpenChange={(id, open) => setOpenFields(previous => open ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id))}
      onFieldChange={(id, value) => {
        const field = capabilities.fields.find(candidate => candidate.id === id);
        if (!field) return;
        try {draft.setPredicates(libraryFilterChange(capabilities, field, state.predicates, value, fixed, now)); setFailure(undefined);}
        catch (error) {setFailure(error); runtime.reportError(error);}
      }} onSearchChange={draft.search} onRetry={draft.retryFacet}/>
  </BottomSheet>;
}
