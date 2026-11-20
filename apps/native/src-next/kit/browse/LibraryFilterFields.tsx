import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import type {BrowseNode} from '../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FilterBuilder, type BrowseLabels, type FilterFieldState} from './index';
import {StateView} from '../states';
import {browsePlatform} from '../browse/presentation';
import {useTheme} from '../theme';
import type {BrowseFiltersProps} from '../frames';
import type {NativeShellRuntime} from '../../shell/runtime';
import {libraryFilterChange, libraryFilterField} from '../../screens/library/filter-controls';
import {NativeLibraryFilterDraft} from '../../screens/library/filters';
import {nativeLibraryCanSave, type NativeLibrarySnapshot, type NativeLibraryViewDraft} from '../../screens/library/model';

/** The Filters fields for one draft: the count's error, then the kit `FilterBuilder`. A live pane (docked beside the grid)
 * applies each change at once; a sheet or panel applies on its primary. */
export function LibraryFilterFields({draft, source, runtime, labels, owner, live, panelId}: {draft: NativeLibraryFilterDraft; source: NativeLibrarySnapshot; runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; live: boolean; panelId: string}) {
  const theme = useTheme(), focusPrefix = JSON.stringify([panelId, 'controls']), state = useSyncExternalStore(draft.subscribe, draft.getSnapshot);
  // The phone sheet draws its fields inline and starts with Genre open; a tablet pane draws a field's choices in a popover,
  // which opens only when its field is chosen (never over the other fields on opening); TV starts closed.
  const [openFields, setOpenFields] = useState<readonly string[]>(() => browsePlatform(theme) === 'phone' && source.capabilities?.fields.some(field => field.id === 'genre') ? ['genre'] : []), [failure, setFailure] = useState<unknown>();
  const [now] = useState(() => new Date());
  useEffect(() => {draft.setOpenFields(openFields);}, [draft, openFields]);
  const model = useMemo(() => source.capabilities && source.sort ? browseToolbarModel(source.capabilities, source.library.kind, state.predicates, source.sort, {...runtime.text, owner, textEntry: !theme.isTV}) : undefined,
    [source.capabilities, source.library.kind, source.sort, state.predicates, runtime.text, owner, theme.isTV]);
  if (!model || !source.capabilities) return null;
  const capabilities = source.capabilities;
  const fields = Object.fromEntries(model.groups.flatMap(group => group.fields.map(field => [field.id, libraryFilterField(field, state.predicates, state.facets[field.id], now)]))) as Readonly<Record<string, FilterFieldState>>;
  const results = state.count.value && defaultI18n.t(state.count.value.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: state.count.value.total, pivot: source.tab.pivot ?? 'other'});
  return <>
    {(failure || state.count.phase === 'error') && <StateView {...runtime.error(failure ?? state.count.error, () => {setFailure(undefined); draft.refresh();})}/>}
    {theme.isTV && results && <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: -4, marginBottom: 8}]}>{defaultI18n.t('next.browse.native.resultsMatch', {results})}</Text>}
    <FilterBuilder testID={`${panelId}-fields`} model={model} labels={labels} focusPrefix={focusPrefix} openFields={openFields} fields={fields}
      onFieldOpenChange={(id, open) => setOpenFields(previous => open ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id))}
      onFieldChange={(id, value) => {
        const field = capabilities.fields.find(candidate => candidate.id === id);
        if (!field) return;
        try {draft.setPredicates(libraryFilterChange(capabilities, field, state.predicates, value, (source.tab.fixedFilter ?? []) as readonly BrowseNode[], now)); if (live) draft.applyLive(); setFailure(undefined);}
        catch (error) {setFailure(error); runtime.reportError(error);}
      }} onSearchChange={draft.search} onRetry={draft.retryFacet}/>
  </>;
}

const noDraftSubscribe = () => () => {};
const noDraftSnapshot = () => undefined;

/** A library's Filters for `BrowseFrame` (phone and tablet): the draft's fields, its count and its actions. The frame picks the
 * surface, the sheet on a phone, the pane over the grid upright and docked beside it landscape, and plays its exit. `live` is
 * a tablet pane (either), which applies each change at once. Closing disposes the draft; the next opening replaces it. */
export function useLibraryFrameFilters({draft, generation, source, runtime, labels, owner, live, onSave}: {draft?: NativeLibraryFilterDraft;
  /** Counts the drafts opened, so each one's fields start fresh. */ generation: number; source: NativeLibrarySnapshot; runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; live: boolean;
  /** Omitted where the host has no real Save view action. */ onSave?: (view: NativeLibraryViewDraft) => void}): BrowseFiltersProps | undefined {
  const state = useSyncExternalStore(draft?.subscribe ?? noDraftSubscribe, draft?.getSnapshot ?? noDraftSnapshot);
  useEffect(() => {if (!draft) return; draft.refresh(); return () => draft.dispose();}, [draft]);
  if (!draft || !state || !source.capabilities || !source.sort) return undefined;
  const sort = source.sort, value = state.count.value;
  const count = value && defaultI18n.t(value.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: value.total, pivot: source.tab.pivot ?? 'other'});
  const panelId = JSON.stringify(['library-filters', runtime.getSnapshot().authority, source.library.id, source.tab.id]);
  return {open: state.open, canApply: live || state.count.phase === 'ready', count: count || undefined, onClose: draft.dispose,
    onClear: () => {draft.clear(); if (live) draft.applyLive();},
    onDone: () => {if (live) draft.dispose(); else draft.apply();},
    save: onSave && {canSave: nativeLibraryCanSave(source, {predicates: state.predicates, sort}), onSave: () => onSave({predicates: state.predicates, sort})},
    children: <LibraryFilterFields key={generation} draft={draft} source={source} runtime={runtime} labels={labels} owner={owner} live={live} panelId={panelId}/>};
}
