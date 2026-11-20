import React, {useEffect, useMemo, useSyncExternalStore} from 'react';
import {LibraryFilterFields} from '../../kit/browse/LibraryFilterFields';
import {Text, View} from 'react-native';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FiltersPane, type BrowseLabels, type FiltersPaneGeometry} from '../../kit/browse';
import {frameSpec} from '@portico/design';
import {Button} from '../../kit/controls';
import {useFocusRegistry} from '../../kit/focus';
import {BottomSheet, SidePanel} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {libraryFilterField} from './filter-controls';
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

export {useLibraryFrameFilters} from '../../kit/browse/LibraryFilterFields';
