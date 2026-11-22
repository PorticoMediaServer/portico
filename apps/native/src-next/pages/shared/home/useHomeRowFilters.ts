import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {BrowseCount, BrowseFacetPage, BrowseFieldCapability, BrowseNode} from '../../../../../../packages/client-core/src/browse';
import {activeRangePreset, browseValueLabel, filterControlOf, filterValuesField, orderFilterValues, rangePresets, type ToolbarPredicate} from '../../../../../../packages/client-core/src/presentation/browse-toolbar';
import {browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import type {FilterFieldState} from '../../../kit/browse';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeHomeRowController} from '../../../screens/home/row-controller';
import {nativeRowFilterChange, nativeRowFilterField, nativeRowFilterValues} from '../../../screens/home/row-filters';
import {homeBrowseLabel} from '../../../screens/home/browse-label';

type Label = Parameters<typeof homeBrowseLabel>[0];
type Facet = Readonly<{search: string; status: 'loading' | 'ready' | 'error'; page?: BrowseFacetPage; error?: unknown}>;
const leaves = (node: BrowseNode): readonly ToolbarPredicate[] => 'field' in node ? [node] : 'not' in node ? [] : ('all' in node ? node.all : node.any).flatMap(leaves);

/** A Home row's See all filters: a draft of the row's filters, its live result count, each field's values (searched and
 * loaded when opened) and Show results, which applies the draft and closes. Moved from the old sheet as it was. */
export function useHomeRowFilters({controller, runtime, onClose}: {controller: NativeHomeRowController; runtime: NativeShellRuntime; onClose: () => void}) {
  const theme = useTheme(), now = useRef(new Date()).current, t = runtime.text.t, labels = {t, has: defaultI18n.has};
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [draft, setDraft] = useState<readonly BrowseNode[]>(state.filters), [counts, setCounts] = useState<readonly BrowseCount[]>([]);
  const [open, setOpen] = useState<readonly string[]>([]), [facets, setFacets] = useState<Readonly<Record<string, Facet>>>({});
  const alive = useRef(true), tickets = useRef(new Map<string, number>()), countTicket = useRef(0);
  useEffect(() => {const lifetime = alive, pending = tickets.current; lifetime.current = true; return () => {lifetime.current = false; pending.clear();};}, []);
  useEffect(() => {
    const ticket = ++countTicket.current, abort = new AbortController();
    setCounts([]);
    void controller.previewCounts(draft, abort.signal).then(value => {if (!abort.signal.aborted && alive.current && ticket === countTicket.current) setCounts(value);})
      .catch(error => {if (!abort.signal.aborted && alive.current && ticket === countTicket.current) runtime.reportError(error);});
    return () => abort.abort();
  }, [controller, draft, runtime]);
  const load = async (field: BrowseFieldCapability, search: string, filters = draft) => {
    const ticket = (tickets.current.get(field.id) ?? 0) + 1; tickets.current.set(field.id, ticket);
    setFacets(previous => ({...previous, [field.id]: {search, status: 'loading'}}));
    try {
      const page = await controller.facets(filterValuesField(field).facet, search, filters);
      if (alive.current && tickets.current.get(field.id) === ticket) setFacets(previous => ({...previous, [field.id]: {search, page, status: 'ready'}}));
    } catch (error) {
      if (alive.current && tickets.current.get(field.id) === ticket) setFacets(previous => ({...previous, [field.id]: {search, status: 'error', error}}));
    }
  };
  if (!state.capabilities || !state.sort) return undefined;
  const capabilities = state.capabilities;
  const model = browseToolbarModel(capabilities, capabilities.library.kind || 'movies', draft, state.sort, {t: runtime.text.t, locale: runtime.text.locale,
    owner: runtime.getSnapshot().account.viewerRole === 'owner', scope: 'group', textEntry: !theme.isTV});
  const fields: Record<string, FilterFieldState> = {};
  for (const group of model.groups) for (const field of group.fields) {
    const control = filterControlOf(field, true), mapping = control === 'values' ? filterValuesField(field) : {field: field.id};
    const selected = draft.filter(node => nativeRowFilterField(node) === mapping.field), direct = selected.flatMap(leaves);
    const active = selected.length > 0;
    if (control === 'values') {
      const facet = facets[field.id];
      fields[field.id] = {...nativeRowFilterValues(field, draft), allowAll: field.operators.includes('contains-all'),
        showMatchMode: field.operators.includes('contains-all'), allowNone: false, cycleExclusion: false,
        values: facet?.page ? orderFilterValues(field, facet.page.values) : [], status: facet?.status ?? 'loading', total: facet?.page?.total,
        search: facet?.search ?? '', active, ...(facet?.error ? {statusLabel: runtime.error(facet.error, () => {void load(field, facet.search);}, 'home').body} : {})};
    } else if (control === 'toggle') fields[field.id] = {kind: 'toggle', value: typeof direct[0]?.value === 'boolean' ? direct[0].value : null, active};
    else if (control === 'choice') fields[field.id] = {kind: 'choice', value: typeof direct[0]?.value === 'string' ? direct[0].value : null,
      options: (field.allowedValues ?? []).map(value => ({value, label: browseValueLabel(value)})), active};
    else if (control === 'range') {
      const presets = rangePresets(field, now);
      fields[field.id] = {kind: 'range', value: activeRangePreset(presets, direct) ?? null,
        presets: presets.map(preset => ({value: preset.id, label: t(preset.label.id as MessageId, preset.label.values)})), active};
    } else fields[field.id] = {kind: 'text', value: typeof direct[0]?.value === 'string' ? direct[0].value : '', active};
  }
  const capability = (id: string) => capabilities.fields.find(value => value.id === id);
  const selectedCount = counts.find(count => count.id === (state.selectedKind ?? 'all'));
  return {
    title: t('next.browse.native.filters'), focusPrefix: `home-row-filters:${controller.rowId}`, model, fields, open,
    labels: {field: (value: Label) => homeBrowseLabel(value, labels), sort: (value: Label) => homeBrowseLabel(value, labels), quick: (value: Label) => homeBrowseLabel(value, labels)},
    showResults: selectedCount && !selectedCount.totalIsLowerBound ? {label: t('next.browse.native.showResults', {results: t('count.titles', {count: selectedCount.total})}),
      onPress: () => {if (controller.setFilters(draft)) onClose();}} : undefined,
    openChange: (id: string, next: boolean) => {setOpen(previous => next ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id));
      const field = capability(id); if (next && field && filterControlOf(field, true) === 'values') void load(field, facets[id]?.search ?? '');},
    change: (id: string, value: Parameters<typeof nativeRowFilterChange>[1]) => {const field = capability(id); if (!field) return;
      try {const next = nativeRowFilterChange(field, value, draft, now); setDraft(next); for (const openId of open) {
        const other = capability(openId); if (other && filterControlOf(other, true) === 'values') void load(other, facets[openId]?.search ?? '', next);
      }} catch (error) {runtime.reportError(error);}},
    search: (id: string, query: string) => {const field = capability(id); if (field) void load(field, query);},
    retry: (id: string) => {const field = capability(id); if (field) void load(field, facets[id]?.search ?? '');},
  };
}
