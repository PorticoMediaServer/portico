import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {BrowseCapabilities, BrowseNode, BrowseSortSelection} from '../../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {searchGenreBrowsePivot} from '../../../../../../packages/client-core/src/search-genres';
import type {FilterFieldState} from '../../../kit/browse';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLibraryFilterDraft} from '../../../screens/library/filters';
import {libraryFilterChange, libraryFilterField} from '../../../screens/library/filter-controls';

/** A genre page's Filters, backed by the real across-video source: the toolbar model's fields with their facets, the
 * live result count for Show results, and edits that keep the genre fixed. It has no fabricated Library snapshot or
 * unsupported saved-view mutation. Moved from the old sheet as it was. */
export function useGenreFilters({draft, capabilities, sort, fixed, runtime, owner, onClose}: Readonly<{
  draft: NativeLibraryFilterDraft; capabilities: BrowseCapabilities; sort: BrowseSortSelection; fixed: readonly BrowseNode[];
  runtime: NativeShellRuntime; owner: boolean; onClose: () => void;
}>) {
  const t = runtime.text.t, locale = runtime.text.locale;
  const state = useSyncExternalStore(draft.subscribe, draft.getSnapshot);
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
  return {t, state, model, fields, prefix, openFields, close,
    problem: failure || state.count.phase === 'error' ? runtime.error(failure ?? state.count.error, () => {setFailure(undefined); draft.refresh();}) : undefined,
    resultLabel: result ? t('next.browse.native.showResults', {results: t(result.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: result.total, pivot: searchGenreBrowsePivot})}) : null,
    apply: () => {if (draft.apply()) close();}, clear: draft.clear,
    fieldOpenChange: (id: string, open: boolean) => setOpenFields(previous => open ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id)),
    fieldChange: (id: string, value: unknown) => {
      const field = capabilities.fields.find(candidate => candidate.id === id);
      if (!field) return;
      try {draft.setPredicates(libraryFilterChange(capabilities, field, state.predicates, value as never, fixed, now)); setFailure(undefined);}
      catch (error) {setFailure(error); runtime.reportError(error);}
    }, search: draft.search, retry: draft.retryFacet};
}
