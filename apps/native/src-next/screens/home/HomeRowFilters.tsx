import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {BrowseCount, BrowseFacetPage, BrowseFieldCapability, BrowseNode} from '../../../../../packages/client-core/src/browse';
import {activeRangePreset, browseValueLabel, filterControlOf, filterValuesField, orderFilterValues, rangePresets, type ToolbarPredicate} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {FilterBuilder, type FilterFieldState} from '../../kit/browse';
import {Button} from '../../kit/controls';
import {BottomSheet, SidePanel} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {NativeHomeRowController} from './row-controller';
import {nativeRowFilterChange, nativeRowFilterField, nativeRowFilterValues} from './row-filters';
import {homeBrowseLabel} from './browse-label';

type Facet = Readonly<{search: string; status: 'loading' | 'ready' | 'error'; page?: BrowseFacetPage; error?: unknown}>;
const leaves = (node: BrowseNode): readonly ToolbarPredicate[] => 'field' in node ? [node] : 'not' in node ? [] : ('all' in node ? node.all : node.any).flatMap(leaves);

export function HomeRowFilters({controller, runtime, onClose}: {controller: NativeHomeRowController; runtime: NativeShellRuntime; onClose: () => void}) {
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
  if (!state.capabilities || !state.sort) return null;
  const model = browseToolbarModel(state.capabilities, 'movies', draft, state.sort, {t: runtime.text.t, locale: runtime.text.locale,
    owner: runtime.getSnapshot().account.viewerRole === 'owner', scope: 'group', textEntry: !theme.isTV});
  const fields: Record<string, FilterFieldState> = {};
  for (const group of model.groups) for (const field of group.fields) {
    const control = filterControlOf(field), mapping = control === 'values' ? filterValuesField(field) : {field: field.id};
    const selected = draft.filter(node => nativeRowFilterField(node) === mapping.field), direct = selected.flatMap(leaves);
    const active = selected.length > 0;
    if (control === 'values') {
      const facet = facets[field.id];
      fields[field.id] = {...nativeRowFilterValues(field, draft), allowAll: field.operators.includes('contains-all'),
        showMatchMode: true, values: facet?.page ? orderFilterValues(field, facet.page.values) : [], status: facet?.status ?? 'loading', total: facet?.page?.total,
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
  const selectedCount = counts.find(count => count.id === (state.selectedKind ?? 'all'));
  const footer = selectedCount && !selectedCount.totalIsLowerBound ? <View style={{alignItems: 'stretch'}}><Button focusId="show-results" onPress={() => {if (controller.setFilters(draft)) onClose();}}>{t('next.browse.native.showResults', {results: t('count.titles', {count: selectedCount.total})})}</Button></View> : undefined;
  const body = <FilterBuilder focusPrefix={`home-row-filters:${controller.rowId}`} model={model} labels={{field: value => homeBrowseLabel(value, labels), sort: value => homeBrowseLabel(value, labels), quick: value => homeBrowseLabel(value, labels)}} openFields={open} fields={fields}
    onFieldOpenChange={(id, next) => {setOpen(previous => next ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id));
      const field = state.capabilities!.fields.find(value => value.id === id); if (next && field && filterControlOf(field) === 'values') void load(field, facets[id]?.search ?? '');}}
    onFieldChange={(id, value) => {const field = state.capabilities!.fields.find(candidate => candidate.id === id); if (!field) return;
      try {const next = nativeRowFilterChange(field, value, draft, now); setDraft(next); for (const openId of open) {
        const other = state.capabilities!.fields.find(candidate => candidate.id === openId); if (other && filterControlOf(other) === 'values') void load(other, facets[openId]?.search ?? '', next);
      }} catch (error) {runtime.reportError(error);}}}
    onSearchChange={(id, query) => {const field = state.capabilities!.fields.find(value => value.id === id); if (field) void load(field, query);}}
    onRetry={id => {const field = state.capabilities!.fields.find(value => value.id === id); if (field) void load(field, facets[id]?.search ?? '');}}/>;
  return theme.isTV ? <SidePanel open title={t('next.browse.native.filters')} onOpenChange={next => {if (!next) onClose();}} footer={footer} testID="home-row-filters">{body}</SidePanel>
    : <BottomSheet open title={t('next.browse.native.filters')} onOpenChange={next => {if (!next) onClose();}} footer={footer} testID="home-row-filters">{body}</BottomSheet>;
}
