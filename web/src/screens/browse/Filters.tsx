import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {filterValueName} from '@core/filter-value-names.ts';
import {defaultI18n} from '@i18n';
import type {BrowseNode} from '@core/browse.ts';
import {presentError} from '@core/presentation/errors.ts';
import {fieldSelectionSummary, filterControlOf, filterValuesField, rangePresets, activeRangePreset} from '@core/presentation/browse-toolbar.ts';
import type {BrowseToolbarModel} from '@portico/client-core/kit-model';
import {createInputCadence} from './input-cadence';
import {FilterBuilder, type BrowseLabels, type FilterFieldChange, type FilterFieldState} from '../../kit/browse';
import type {BrowseController, BrowseState} from './controller';
import type {BrowseFiltersProps} from '../../kit/frames';

export type LibraryFiltersOptions = Readonly<{controller: BrowseController; model: BrowseToolbarModel; labels: BrowseLabels; compact: boolean; open: boolean; resultCount?: string; canSaveView: boolean; onSaveView?: () => void; onClose: () => void; reportError: (error: unknown) => void}>;
const t = defaultI18n.t;
const valueLabel = (value: string) => {const key = `filter.value.${value}`;return defaultI18n.has(key) ? t(key) : value;};
const simple = (nodes: readonly BrowseNode[]) => nodes.flatMap(node => 'field' in node ? [node] : []);
type FilterOpening = {controller: BrowseController; cadence: ReturnType<typeof createInputCadence>; live: boolean; abort: AbortController; query?: BrowseState; pending?: Readonly<{id: string; raw: string}>};
type FacetFields = Readonly<Record<string, Extract<FilterFieldState, {kind: 'values'}>>>;
/** The library's Filters region for `BrowseFrame`: the fields and what Clear all, Done and Save view do. The frame picks the
 * surface for the form (a sheet at phone width, a side panel, docked on a touch tablet). */
export function useLibraryFilters({controller, model, labels, compact, open, resultCount, canSaveView, onSaveView, onClose, reportError}: LibraryFiltersOptions): BrowseFiltersProps {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const yearAsRange = controller.source.homeRowId !== undefined;
  const [openFields, setOpenFields] = useState<readonly string[]>([]), [queries, setQueries] = useState<Readonly<Record<string, string>>>({}), [retries, setRetries] = useState<Readonly<Record<string, number>>>({});
  const [facetState, setFacetState] = useState<Readonly<{opening: FilterOpening; query: BrowseState; fields: FacetFields}>>();
  const facetRequests = useRef(new Map<string, {key: string; timer?: ReturnType<typeof setTimeout>; stop: AbortController}>());
  const [expandedValues, setExpandedValues] = useState<readonly string[]>([]);
  const fields = useMemo(() => model.groups.flatMap(group => group.fields), [model]);
  const [drafts, setDrafts] = useState<Readonly<Record<string, string>>>({});
  const draftsRef = useRef<Readonly<Record<string, string>>>({});
  const cadence = useRef<ReturnType<typeof createInputCadence> | undefined>(undefined);
  const openingRef = useRef<FilterOpening | undefined>(undefined), [published, publishOpening] = useState<FilterOpening>();
  const opening = open && published?.controller === controller && published.live && openingRef.current === published ? published : undefined;
  const owns = (original: FilterOpening | undefined): original is FilterOpening =>
    !!original && original.live && original.controller === controller && openingRef.current === original && cadence.current === original.cadence;
  const facets: FacetFields = facetState && opening && facetState.opening === opening && controller.acceptsQuery(facetState.query) ? facetState.fields : {};
  const updateDrafts = (next: Readonly<Record<string, string>>) => {draftsRef.current = next; setDrafts(next);};
  // The cadence belongs to the actual controller/opening (useBrowseLease owns the
  // controller). An ordinary accepted page/model publication does not retire a
  // valid same draft; only closing or replacing that opening does.
  useLayoutEffect(() => {
    updateDrafts({});
    if (!open) {publishOpening(undefined);return;}
    const original: FilterOpening = {controller, cadence: createInputCadence(250), live: true, abort: new AbortController()};
    openingRef.current = original;cadence.current = original.cadence;publishOpening(original);
    return () => {
      original.live = false;original.pending = undefined;original.cadence.close();original.abort.abort();
      if (openingRef.current === original) openingRef.current = undefined;
      if (cadence.current === original.cadence) cadence.current = undefined;
    };
  }, [controller, open]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({owns, state, controller});
  useLayoutEffect(() => {setupPorts.current = {owns, state, controller};});
  useLayoutEffect(() => {
    const {owns, state, controller} = setupPorts.current;
    if (!owns(opening)) return;
    if (opening.query && !controller.acceptsQuery(opening.query)) {opening.pending = undefined;opening.cadence.reset();updateDrafts({});}
    opening.query = state;
  }, [opening, state.queryRevision, state.capabilities, state.predicates, state.sort]);
  const clearDrafts = (keys: readonly (string | undefined)[]) => {
    const next = {...draftsRef.current}; let changed = false;
    for (const key of keys) if (key && Object.hasOwn(next, key)) {delete next[key]; changed = true;}
    if (changed) updateDrafts(next);
  };
  const clearAll = () => {if (!owns(opening) || !controller.acceptsQuery(state)) return;opening.pending = undefined;opening.cadence.reset();updateDrafts({});controller.setQuery([], undefined, state);};
  const closeFilters = () => {
    if (!owns(opening)) return;
    opening.live = false;opening.pending = undefined;opening.cadence.close();opening.abort.abort();
    if (openingRef.current === opening) openingRef.current = undefined;
    if (cadence.current === opening.cadence) cadence.current = undefined;
    updateDrafts({});onClose();
  };
  const drawn = () => owns(opening) && controller.acceptsQuery(state);
  const saveView = () => {if (drawn() && canSaveView) onSaveView?.();};
  const predicates = state.predicates, atomic = simple(predicates);
  const predicatesKey = JSON.stringify(predicates);
  useLayoutEffect(() => {
    const requests = facetRequests.current;
    return () => {
      for (const entry of requests.values()) {if (entry.timer) clearTimeout(entry.timer); entry.stop.abort();}
      requests.clear();
    };
  }, [controller, opening]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({owns, state, fields, predicates, queries, retries, reportError});
  useLayoutEffect(() => {setupPorts1.current = {owns, state, fields, predicates, queries, retries, reportError};});
  useLayoutEffect(() => {
    const {owns, state, fields, predicates, queries, retries, reportError} = setupPorts1.current;
    if (!owns(opening)) return;
    const original = opening, originalQuery = state, requests = facetRequests.current;
    const openSet = new Set(openFields);
    for (const [id, entry] of requests) {
      if (!openSet.has(id)) {
        if (entry.timer) clearTimeout(entry.timer);
        entry.stop.abort();
        requests.delete(id);
      }
    }
    const baseKey = JSON.stringify([predicatesKey, state.queryRevision, yearAsRange]);
    for (const id of openFields) {
      const field = fields.find(field => field.id === id); if (!field || filterControlOf(field, yearAsRange) !== 'values') continue;
      const query = queries[id] ?? '', retry = retries[id] ?? 0, descriptor = filterValuesField(field);
      const key = JSON.stringify([baseKey, query, retry]);
      if (requests.get(id)?.key === key) continue;
      const prior = requests.get(id);
      if (prior) {if (prior.timer) clearTimeout(prior.timer); prior.stop.abort(); requests.delete(id);}
      const stop = new AbortController();
      const current = () => owns(original) && !stop.signal.aborted && controller.acceptsQuery(originalQuery);
      const signal = AbortSignal.any([original.abort.signal, stop.signal]);
      const setFacets = (update: (before: FacetFields) => FacetFields) => {
        if (!current()) return;
        setFacetState(before => current() ? {opening: original, query: originalQuery, fields: update(before?.opening === original && controller.acceptsQuery(before.query) ? before.fields : {})} : before);
      };
      const entry: {key: string; timer?: ReturnType<typeof setTimeout>; stop: AbortController} = {key, stop};
      requests.set(id, entry);
      entry.timer = setTimeout(() => {
        entry.timer = undefined;
        if (!current()) return;
        if (!field.facetSource && field.allowedValues) {
          setFacets(before => ({...before, [id]: {kind: 'values', values: field.allowedValues!.filter(value => value.toLocaleLowerCase().includes(query.toLocaleLowerCase())).map(value => ({value, label: valueLabel(value)})), included: [], excluded: [], match: 'any', query, status: 'ready', error: undefined}}));
          return;
        }
        setFacets(before => ({...before, [id]: {...(before[id] ?? {kind: 'values', values: [], included: [], excluded: [], match: 'any'}), query, status: 'loading', error: undefined}}));
        void controller.facets(descriptor.facet, query, predicates, signal).then(page => {
          if (current()) setFacets(before => ({...before, [id]: {kind: 'values', values: page.values, total: page.total, included: [], excluded: [], match: 'any', query, status: 'ready', error: undefined}}));
        }).catch(error => {
          if (current()) {
            const presented = presentError(error, 'browse', {operation: 'load', i18n: defaultI18n});
            setFacets(before => ({...before, [id]: {...(before[id] ?? {kind: 'values', values: [], included: [], excluded: [], match: 'any'}), query, status: 'error', error: presented.body}}));
            reportError(error);
          }
        });
      }, 250);
    }
  }, [controller, opening, predicatesKey, queries, openFields, retries, state.queryRevision, state.capabilities, yearAsRange]);
  const displayed = Object.fromEntries(fields.map(field => {
    const kind = filterControlOf(field, yearAsRange), descriptor = filterValuesField(field), own = atomic.filter(node => node.field === field.id || node.field === descriptor.field);
    const summary = fieldSelectionSummary(field, own, value => facets[field.id]?.values.find(option => option.value === String(value))?.label ?? filterValueName(value) ?? valueLabel(String(value)), t);
    let value: FilterFieldState;
    if (kind === 'values') {
      const included = own.filter(node => !['not-in', 'not-equals'].includes(node.operator)).flatMap(node => (Array.isArray(node.value) ? node.value : [node.value]).filter((value): value is string | number => typeof value === 'string' || typeof value === 'number').map(String));
      const excluded = own.filter(node => ['not-in', 'not-equals'].includes(node.operator)).flatMap(node => (Array.isArray(node.value) ? node.value : [node.value]).map(String));
      const page = facets[field.id] ?? {kind: 'values', values: [], query: '', status: 'loading'};
      const activeQuery = queries[field.id] ?? page.query;
      const expanded = expandedValues.includes(field.id), total = page.total ?? page.values.length;
      const reading = state.capabilities?.library.kind === 'book' || state.capabilities?.library.kind === 'comic';
      const values = page.status === 'loading' && !reading ? [] : activeQuery || expanded ? page.values : page.values.slice(0, compact ? 11 : 9);
      // Show all expands a complete bounded facet response. Larger vocabularies
      // retain real server search; this endpoint never claims to enumerate them.
      value = {...page, query: activeQuery, values, total: page.status !== 'loading' && !expanded && total <= page.values.length ? total : undefined, kind: 'values', included, excluded, match: own.some(node => node.operator === 'contains-all') ? 'all' : excluded.length && !included.length ? 'none' : 'any', summary};
    } else if (kind === 'toggle') value = {kind, value: typeof own[0]?.value === 'boolean' ? own[0].value : undefined, summary};
    else if (kind === 'range') {const presets = rangePresets(field, new Date());value = {kind, options: presets.map(preset => ({value: preset.id, label: t(preset.label.id, preset.label.values)})), value: activeRangePreset(presets, own), summary};}
    else if (kind === 'choice') value = {kind, options: (field.allowedValues ?? []).map(value => ({value, label: valueLabel(value)})), value: typeof own[0]?.value === 'string' ? own[0].value : undefined, summary};
    else value = {kind, value: opening ? drafts[field.id] ?? (typeof own[0]?.value === 'string' ? own[0].value : '') : typeof own[0]?.value === 'string' ? own[0].value : '', summary};
    return [field.id, value];
  }));
  const change = (id: string, change: FilterFieldChange) => {
    if (!owns(opening) || !controller.acceptsQuery(state)) return;
    const original = opening, originalQuery = state;
    const field = fields.find(field => field.id === id), shown = displayed[id];if (!field || !shown) return;
    const descriptor = filterValuesField(field), before = predicates.filter(node => !('field' in node) || node.field !== field.id && node.field !== descriptor.field);
    if (change.kind !== 'text') {
      const prior = original.cadence.pendingKey();
      original.pending = undefined;original.cadence.reset(); clearDrafts([id, prior]);
    }
    const parsed = (value: unknown) => controller.parseNode(value);
    let next: readonly BrowseNode[] = [];
    try {
      if ((change.kind === 'facet' || change.kind === 'match') && shown.kind === 'values') {
        const match = change.kind === 'match' ? change.value : shown.match;
        const included = change.kind === 'facet' ? descriptor.single ? (change.selection === 'include' ? [change.value] : [])
          : [...shown.included.filter(v => v !== change.value), ...(change.selection === 'include' ? [change.value] : [])] : shown.match === 'none' && match !== 'none' ? shown.excluded : shown.included;
        const excluded = change.kind === 'facet' ? [...shown.excluded.filter(v => v !== change.value), ...(change.selection === 'exclude' ? [change.value] : [])] : shown.match === 'none' && match !== 'none' ? [] : shown.excluded;
        next = [...(included.length && descriptor.single ? [parsed({field: descriptor.field, operator: 'equals', value: included[0]})] : []),
          ...(included.length && !descriptor.single ? [parsed({field: descriptor.field, operator: match === 'none' ? 'not-in' : match === 'all' && field.operators.includes('contains-all') ? 'contains-all' : descriptor.operator, value: included.map(value => descriptor.field === 'decade' ? Number(value) : value)})] : []),
          ...(excluded.length ? [parsed({field: descriptor.field, operator: 'not-in', value: excluded.map(value => descriptor.field === 'decade' ? Number(value) : value)})] : [])];
      } else if (change.kind === 'toggle' && change.value !== undefined) next = [parsed({field: id, operator: 'equals', value: change.value})];
      else if (change.kind === 'choice' && change.value !== undefined) next = shown.kind === 'range' ? (rangePresets(field, new Date()).find(preset => preset.id === change.value)?.predicates ?? []).map(parsed) : [parsed({field: id, operator: 'equals', value: change.value})];
      else if (change.kind === 'text' && change.value.trim()) {
        const raw = change.value, value = raw.trim(), prior = original.cadence.pendingKey();
        clearDrafts(prior && prior !== id ? [prior] : []);
        updateDrafts({...draftsRef.current, [id]: raw});
        const ticket = Object.freeze({id, raw});original.pending = ticket;
        original.cadence.schedule(id, value, typed => {
          if (!owns(original) || original.pending !== ticket || !controller.acceptsQuery(originalQuery)) return;
          try {
            controller.setQuery([...before, parsed({field: id, operator: field.operators.includes('contains') ? 'contains' : 'equals', value: typed})], undefined, originalQuery);
          } catch (error) {if (owns(original) && controller.acceptsQuery(originalQuery)) reportError(error);}
          finally {if (owns(original) && original.pending === ticket) {original.pending = undefined;clearDrafts([id]);}}
        });
        return;
      } else if (change.kind === 'text') {
        const prior = original.cadence.pendingKey();
        original.pending = undefined;original.cadence.reset(); clearDrafts([id, prior]);
        // Empty text keeps its original immediate meaning: drop this field's text
        // predicate now, while retiring any pending typed value that could re-add it.
        try {controller.setQuery([...before], undefined, originalQuery);} catch (error) {if (owns(original) && controller.acceptsQuery(originalQuery)) reportError(error);}
        return;
      }
      controller.setQuery([...before, ...next], undefined, originalQuery);
    } catch (error) {if (owns(original) && controller.acceptsQuery(originalQuery)) reportError(error);}
  };
  const body = <FilterBuilder model={model} labels={labels} platform={compact ? 'phone' : 'desktop'} fields={displayed} openFields={openFields}
    onFieldOpenChange={(id, opened) => {if (drawn()) setOpenFields(before => opened ? [...before.filter(value => value !== id), id] : before.filter(value => value !== id));}}
    onFieldChange={change} onSearchChange={(id, query) => {if (drawn()) setQueries(before => ({...before, [id]: query}));}} onShowAll={id => {if (drawn()) setExpandedValues(before => [...before.filter(value => value !== id), id]);}}
    onRetry={id => {if (drawn()) setRetries(before => ({...before, [id]: (before[id] ?? 0) + 1}));}}/>;
  return {open, onClose: closeFilters, onClear: clearAll, count: resultCount, save: onSaveView ? {canSave: canSaveView, onSave: saveView} : undefined, children: body};
}
