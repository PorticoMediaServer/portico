import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import type {BrowseNode} from '../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FilterBuilder, FiltersPane, type BrowseLabels, type FilterFieldState, type FiltersPaneGeometry} from '../../kit/browse';
import {frameSpec} from '@portico/design';
import {Button} from '../../kit/controls';
import {useFocusRegistry} from '../../kit/focus';
import {BottomSheet, SidePanel} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {browsePlatform, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {BrowseFiltersProps} from '../../kit/frames';
import type {NativeShellRuntime} from '../../shell/runtime';
import {libraryFilterChange, libraryFilterField} from './filter-controls';
import {NativeLibraryFilterDraft} from './filters';
import {nativeLibraryCanSave, type NativeLibrarySnapshot, type NativeLibraryViewDraft} from './model';

/** tablet.html #4 docks the panel beside the grid (`frameSpec.browse.filtersWidth`). */
export const libraryFiltersDockWidth = frameSpec.browse.filtersWidth.expanded;

export function LibraryFilters({draft, source, runtime, labels, owner, onClose, onExited, onSave, docked = false, geometry}: {draft: NativeLibraryFilterDraft; source: NativeLibrarySnapshot; runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; onClose: () => void;
  /** The phone sheet has finished closing: its host can drop the draft, so a closed sheet never stays mounted. */
  onExited?: () => void; /** Omitted where the host has no real Save view action (the reading libraries). */ onSave?: (view: NativeLibraryViewDraft) => void; /** tablet-read#1b and tablet#4/#5: the same draft and controls as a pane beside the grid. */ docked?: boolean; /** Omitted keeps the reading pane's own numbers. */ geometry?: FiltersPaneGeometry}) {
  const theme = useTheme(), registry = useFocusRegistry(), state = useSyncExternalStore(draft.subscribe, draft.getSnapshot);
  // A library that is still loading (just switched to) has no capabilities or sort yet: there is nothing to filter, so
  // the panel draws nothing rather than reading them (W3 F2: `resolvedPivot` of undefined took the screen down).
  const model = useMemo(() => source.capabilities && source.sort ? browseToolbarModel(source.capabilities, source.library.kind, state.predicates, source.sort, {...runtime.text, owner, textEntry: !theme.isTV}) : undefined,
    [source.capabilities, source.library.kind, source.sort, state.predicates, runtime.text, owner, theme.isTV]);
  const panelId = JSON.stringify(['library-filters', runtime.getSnapshot().authority, source.library.id, source.tab.id]);
  const focusPrefix = JSON.stringify([panelId, 'controls']);
  const first = model?.groups[0]?.fields[0];
  const firstState = first && libraryFilterField(first, state.predicates, state.facets[first.id], new Date());
  const firstId = first ? JSON.stringify([JSON.stringify([focusPrefix, 'field', first.id]), ...(firstState?.kind === 'toggle' ? ['toggle', 'any'] : ['open'])]) : 'apply';
  const initialFocusRef = useMemo(() => ({current: {focus: () => {registry.focus({regionId: panelId, childId: firstId});}}}), [registry, panelId, firstId]);
  useEffect(() => {draft.refresh(); return () => draft.dispose();}, [draft]);
  useEffect(() => {if (!state.open) onClose();}, [onClose, state.open]);
  if (!model || !source.capabilities || !source.sort) return null;
  const sort = source.sort;
  const close = () => {draft.dispose(); onClose();};
  const result = state.count.value;
  const results = result && defaultI18n.t(result.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: result.total, pivot: source.tab.pivot ?? 'other'});
  const footer = <View style={{flexDirection: 'row', gap: theme.isTV ? 16 : 10}}>
    {!theme.isTV && onSave && <Button focusId="save-view" variant="glass" disabled={!nativeLibraryCanSave(source, {predicates: state.predicates, sort: sort})}
      onPress={() => onSave({predicates: state.predicates, sort: sort})}>{defaultI18n.t('next.browse.native.saveView')}</Button>}
    <Button focusId="apply" testID={`${panelId}-apply`} variant="primary" disabled={state.count.phase !== 'ready'} onPress={() => {if (draft.apply()) onClose();}} style={{flex: 1}}>
      {results ? defaultI18n.t('next.browse.native.showResults', {results}) : null}
    </Button>
    {theme.isTV && <Button focusId="clear" variant="glass" onPress={draft.clear}>{defaultI18n.t('next.browse.native.clearAll')}</Button>}
  </View>;
  const content = <LibraryFilterFields draft={draft} source={source} runtime={runtime} labels={labels} owner={owner} live={docked} panelId={panelId}/>;
  const clear = () => {draft.clear(); if (docked) draft.applyLive();};
  if (docked) {
    const live = state.count.value, unfiltered = source.unfilteredTotal;
    const showing = live && (unfiltered
      ? defaultI18n.t('title.shownOfTotal', {shown: `${defaultI18n.number(live.total)}${live.totalIsLowerBound ? defaultI18n.t('next.browse.native.countPlus') : ''}`,
          total: defaultI18n.t(unfiltered.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: unfiltered.total, pivot: source.tab.pivot ?? 'other'})})
      : defaultI18n.t(live.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: live.total, pivot: source.tab.pivot ?? 'other'}));
    return <FiltersPane testID={panelId} geometry={geometry} summary={showing || undefined} onClose={onClose} onClear={clear} onDone={() => {draft.dispose(); onClose();}}>{content}</FiltersPane>;
  }
  return theme.isTV ? <SidePanel testID={panelId} open={state.open} onOpenChange={open => {if (!open) close();}} title={defaultI18n.t('next.browse.native.filters')}
    width={frameSpec.browse.filtersWidth.tv} topInset={0} initialFocusRef={initialFocusRef} showClose={false} titleType={{size: 44, weight: 800, tracking: -.03, lineHeight: 52.8}}
    style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}} footer={footer}>{content}</SidePanel>
    : <BottomSheet testID={panelId} open={state.open} onOpenChange={open => {if (!open) close();}} onExited={onExited} title={defaultI18n.t('next.browse.native.filters')}
      headerAction={<Button focusId="clear" variant="text" textStyle={theme.platform === 'phone' ? {color: theme.colors.textSecondary} : undefined} onPress={draft.clear}>{defaultI18n.t('next.browse.native.clearAll')}</Button>}
      footer={footer} scrollable={false} detents={theme.platform === 'phone' ? [580, 698] : [580, 780]}
      defaultDetent={theme.platform === 'phone' ? 1 : undefined} showClose={false}>{content}</BottomSheet>;
}

/** The Filters fields for one draft: the count's error, then the kit `FilterBuilder`. A live pane (docked beside the grid)
 * applies each change at once; a sheet or panel applies on its primary. */
function LibraryFilterFields({draft, source, runtime, labels, owner, live, panelId}: {draft: NativeLibraryFilterDraft; source: NativeLibrarySnapshot; runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; live: boolean; panelId: string}) {
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
    {theme.isTV && results && <Text style={[browseType(theme, 22, 400, 0, 33), {color: theme.colors.textTertiary, marginTop: -4, marginBottom: 8}]}>{defaultI18n.t('next.browse.native.resultsMatch', {results})}</Text>}
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
  return {open: state.open, count: count || undefined, onClose: draft.dispose,
    onClear: () => {draft.clear(); if (live) draft.applyLive();},
    onDone: () => {if (live) draft.dispose(); else draft.apply();},
    save: onSave && {canSave: nativeLibraryCanSave(source, {predicates: state.predicates, sort}), onSave: () => onSave({predicates: state.predicates, sort})},
    children: <LibraryFilterFields key={generation} draft={draft} source={source} runtime={runtime} labels={labels} owner={owner} live={live} panelId={panelId}/>};
}
