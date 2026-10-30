import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {BrowseNode} from '../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FilterBuilder, type BrowseLabels, type FilterFieldState} from '../../kit/browse';
import {Button, Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {useFocusRegistry} from '../../kit/focus';
import {BottomSheet, SidePanel} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {libraryFilterChange, libraryFilterField} from './filter-controls';
import {NativeLibraryFilterDraft} from './filters';
import {nativeLibraryCanSave, type NativeLibrarySnapshot, type NativeLibraryViewDraft} from './model';

/** The docked Filters pane's own drawn numbers. tablet.html #4/#5 draw 24 pt
 * padding, a 26 pt title and 50 pt footer buttons over the tablet's status bar
 * and home indicator; the reading pane keeps its older numbers by passing none. */
export type LibraryFiltersPaneGeometry = Readonly<{padding: number; titleSize: number; titleTracking: number; headerTop: number; headerBottom: number; footerTop: number; footerBottom: number; gap: number; buttonHeight?: number; safeTop: boolean; safeBottom: boolean}>;
export const libraryFiltersTabletPane: LibraryFiltersPaneGeometry = Object.freeze({padding: 24, titleSize: 26, titleTracking: -.03, headerTop: 16, headerBottom: 6, footerTop: 16, footerBottom: 28, gap: 12, buttonHeight: 50, safeTop: true, safeBottom: true});
/** tablet.html #4 docks the panel at 380; #5 draws it 420 over the upright grid. */
export const libraryFiltersDockWidth = 380;
export const libraryFiltersOverWidth = 420;
const libraryFiltersReadingPane: LibraryFiltersPaneGeometry = Object.freeze({padding: 20, titleSize: 22, titleTracking: -.02, headerTop: 18, headerBottom: 12, footerTop: 20, footerBottom: 20, gap: 10, safeTop: false, safeBottom: false});

export function LibraryFilters({draft, source, runtime, labels, owner, onClose, onSave, docked = false, geometry}: {draft: NativeLibraryFilterDraft; source: NativeLibrarySnapshot; runtime: NativeShellRuntime; labels: BrowseLabels; owner: boolean; onClose: () => void; /** Omitted where the host has no real Save view action (the reading libraries). */ onSave?: (view: NativeLibraryViewDraft) => void; /** tablet-read#1b and tablet#4/#5: the same draft and controls as a pane beside the grid. */ docked?: boolean; /** Omitted keeps the reading pane's own numbers. */ geometry?: LibraryFiltersPaneGeometry}) {
  const theme = useTheme(), registry = useFocusRegistry(), insets = useSafeAreaInsets(), state = useSyncExternalStore(draft.subscribe, draft.getSnapshot);
  const [openFields, setOpenFields] = useState<readonly string[]>(() => !theme.isTV && source.capabilities?.fields.some(field => field.id === 'genre') ? ['genre'] : []), [failure, setFailure] = useState<unknown>();
  const [now] = useState(() => new Date());
  const model = useMemo(() => browseToolbarModel(source.capabilities!, source.library.kind, state.predicates, source.sort!, {...runtime.text, owner, textEntry: !theme.isTV}),
    [source.capabilities, source.library.kind, source.sort, state.predicates, runtime.text, owner, theme.isTV]);
  const panelId = JSON.stringify(['library-filters', runtime.getSnapshot().authority, source.library.id, source.tab.id]);
  const focusPrefix = JSON.stringify([panelId, 'controls']);
  const fields = Object.fromEntries(model.groups.flatMap(group => group.fields.map(field => [field.id, libraryFilterField(field, state.predicates, state.facets[field.id], now)]))) as Readonly<Record<string, FilterFieldState>>;
  const first = model.groups[0]?.fields[0];
  const firstState = first && fields[first.id];
  const firstId = first ? JSON.stringify([JSON.stringify([focusPrefix, 'field', first.id]), ...(firstState?.kind === 'toggle' ? ['toggle', 'any'] : ['open'])]) : 'apply';
  const initialFocusRef = useMemo(() => ({current: {focus: () => {registry.focus({regionId: panelId, childId: firstId});}}}), [registry, panelId, firstId]);
  useEffect(() => {draft.refresh(); return () => draft.dispose();}, [draft]);
  useEffect(() => {draft.setOpenFields(openFields);}, [draft, openFields]);
  useEffect(() => {if (!state.open) onClose();}, [onClose, state.open]);
  const close = () => {draft.dispose(); onClose();};
  const result = state.count.value;
  const results = result && defaultI18n.t(result.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: result.total, pivot: source.tab.pivot ?? 'other'});
  const footer = <View style={{flexDirection: 'row', gap: theme.isTV ? 16 : 10}}>
    {!theme.isTV && onSave && <Button focusId="save-view" variant="glass" disabled={!nativeLibraryCanSave(source, {predicates: state.predicates, sort: source.sort!})}
      onPress={() => onSave({predicates: state.predicates, sort: source.sort!})}>{defaultI18n.t('next.browse.native.saveView')}</Button>}
    <Button focusId="apply" testID={`${panelId}-apply`} variant="primary" disabled={state.count.phase !== 'ready'} onPress={() => {if (draft.apply()) onClose();}} style={{flex: 1}}>
      {results ? defaultI18n.t('next.browse.native.showResults', {results}) : null}
    </Button>
    {theme.isTV && <Button focusId="clear" variant="glass" onPress={draft.clear}>{defaultI18n.t('next.browse.native.clearAll')}</Button>}
  </View>;
  const content = <>
    {(failure || state.count.phase === 'error') && <StateView {...runtime.error(failure ?? state.count.error, () => {setFailure(undefined); draft.refresh();})}/>}
    {theme.isTV && results && <Text style={[browseType(theme, 22, 400, 0, 33), {color: theme.colors.textTertiary, marginTop: -4, marginBottom: 8}]}>{defaultI18n.t('next.browse.native.resultsMatch', {results})}</Text>}
    <FilterBuilder testID={`${panelId}-fields`} model={model} labels={labels} focusPrefix={focusPrefix} openFields={openFields} fields={fields}
      onFieldOpenChange={(id, open) => setOpenFields(previous => open ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id))}
      onFieldChange={(id, value) => {
        const field = source.capabilities!.fields.find(candidate => candidate.id === id);
        if (!field) return;
        try {draft.setPredicates(libraryFilterChange(source.capabilities!, field, state.predicates, value, (source.tab.fixedFilter ?? []) as readonly BrowseNode[], now)); if (docked) draft.applyLive(); setFailure(undefined);}
        catch (error) {setFailure(error); runtime.reportError(error);}
      }} onSearchChange={draft.search} onRetry={draft.retryFacet}/>
  </>;
  const clear = () => {draft.clear(); if (docked) draft.applyLive();};
  if (docked) {
    const g = geometry ?? libraryFiltersReadingPane;
    const live = state.count.value, unfiltered = source.unfilteredTotal;
    const showing = live && (unfiltered
      ? defaultI18n.t('title.shownOfTotal', {shown: `${defaultI18n.number(live.total)}${live.totalIsLowerBound ? defaultI18n.t('next.browse.native.countPlus') : ''}`,
          total: defaultI18n.t(unfiltered.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: unfiltered.total, pivot: source.tab.pivot ?? 'other'})})
      : defaultI18n.t(live.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: live.total, pivot: source.tab.pivot ?? 'other'}));
    const button = g.buttonHeight ? {height: g.buttonHeight} : undefined;
    return <View testID={panelId} style={{flex: 1}}>
      <View style={{paddingHorizontal: g.padding, paddingTop: (g.safeTop ? insets.top : 0) + g.headerTop, paddingBottom: g.headerBottom, gap: 4}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
          <Text accessibilityRole="header" style={[browseType(theme, g.titleSize, 800, g.titleTracking), {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('next.browse.native.filters')}</Text>
          <Focusable testID={`${panelId}-close`} focusId={`${panelId}-close`} accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.overlays.close')}
            onPress={onClose} style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center', marginRight: -10}}>
            <Icon name="i-x" size={20} color={theme.colors.textSecondary}/>
          </Focusable>
        </View>
        {showing ? <Text style={[browseType(theme, 14.5, 500), {color: theme.colors.textTertiary}]}>{showing}</Text> : null}
      </View>
      <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: g.padding, paddingTop: 8, paddingBottom: 24}}>{content}</ScrollView>
      <View style={{flexDirection: 'row', gap: g.gap, paddingHorizontal: g.padding, paddingTop: g.footerTop, borderTopWidth: 1, borderTopColor: theme.colors.line,
        paddingBottom: g.safeBottom ? Math.max(g.footerBottom, insets.bottom + 8) : g.footerBottom}}>
        <Button testID={`${panelId}-clear`} variant="glass" onPress={clear} style={button}>{defaultI18n.t('next.browse.native.clearAll')}</Button>
        <Button testID={`${panelId}-done`} variant="primary" onPress={() => {draft.dispose(); onClose();}} style={{flex: 1, ...button}}>{defaultI18n.t('next.controls.done')}</Button>
      </View>
    </View>;
  }
  return theme.isTV ? <SidePanel testID={panelId} open={state.open} onOpenChange={open => {if (!open) close();}} title={defaultI18n.t('next.browse.native.filters')}
    width={680} topInset={0} initialFocusRef={initialFocusRef} showClose={false} titleType={{size: 44, weight: 800, tracking: -.03, lineHeight: 52.8}}
    style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}} footer={footer}>{content}</SidePanel>
    : <BottomSheet testID={panelId} open={state.open} onOpenChange={open => {if (!open) close();}} title={defaultI18n.t('next.browse.native.filters')}
      headerAction={<Button focusId="clear" variant="text" textStyle={theme.platform === 'phone' ? {color: theme.colors.textSecondary} : undefined} onPress={draft.clear}>{defaultI18n.t('next.browse.native.clearAll')}</Button>}
      footer={footer} scrollable={false} detents={theme.platform === 'phone' ? [580, 698] : [580, 780]}
      defaultDetent={theme.platform === 'phone' ? 1 : undefined} showClose={false}>{content}</BottomSheet>;
}
