import {useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode} from 'react';
import {useCoarsePointer} from '../../kit/theme';
import {defaultI18n} from '@i18n';
import type {BrowseNode, BrowseSortSelection} from '@core/browse.ts';
import type {Library} from '@core/index.ts';
import {browseNameId} from '@core/presentation/browse-toolbar.ts';
import {appliedFilterParts, browseToolbarModel, type FactPart, type TextEnvironment} from '@portico/client-core/kit-model';
import {LibraryHeader, LibrarySwitcher, StickyBrowseHeader, BrowseToolbar, type LibraryChoice, type BrowseLabels} from '../../kit/browse';
import {Tabs} from '../../kit/controls';
import {AmbientArt} from '../../kit/artwork';
import {StateView} from '../../kit/states';
import {LoadingGrid} from '../../kit/states/LoadingGrid';
import type {ShellArtwork} from '../../shell/artwork';
import {useShellWidth} from '../../shell/viewport';
import {BrowseResults} from './BrowseResults';
import {BrowseFilters} from './Filters';
import {BrowseFilteredEmpty} from './FilteredEmpty';
import {BrowseScopeChips} from './ScopeChips';
import {useLibraryCardMenu} from './use-card-menu';
import type {BrowseController, BrowseMember} from './controller';
import type {BrowseItemActions} from './EntryCard';
import styles from './browse.module.css';
import {browsePresentation, type BrowsePresentation, type ScreenAppearance} from './appearance';
import type {LibraryPlaySelection} from './play-selection';

export type {BrowsePresentation} from './appearance';
export type LibraryBindings = Readonly<{
  controller: BrowseController;
  libraries: readonly LibraryChoice[];
  library: Readonly<{id: string; name: string; kind: string; latestArtwork?: string}>;
  pivot: string;
  tabs: readonly Readonly<{value: string; label: string}>[];
  appearance: BrowsePresentation;
  preferences?: ScreenAppearance;
  /** Only an actual per-library choice overrides the confirmed device default. */
  libraryViewOverride?: 'grid' | 'list';
  onAppearanceChange: (change: Partial<Pick<BrowsePresentation, 'view' | 'cardSize'>>) => void;
  onLibrary: (id: string) => void;
  onLibraries: () => void;
  onPivot?: (pivot: string) => void;
  libraryMenu: ReactNode;
  /** Required real selection bar and per-entry capabilities from the selected viewer. */
  selection: Readonly<{toolbar?: ReactNode; actions: (member: BrowseMember, index: number) => BrowseItemActions; registerWindow?: (first: number, last: number) => void; playSelection?: LibraryPlaySelection}>;
  familyForEntry?: Parameters<typeof BrowseResults>[0]['familyForEntry'];
  scopeLibraries?: readonly Library[];
  shape?: 'poster' | 'square' | 'collection';
  onSaveView?: () => void;
  /** A surface that names its own sort on the bar (a row's order reads "Best match"). */
  sortLabel?: string;
  /** The quick filters this surface draws; absent draws every one the server offers. */
  quickIds?: readonly string[];
  /** Names the scope chips from the viewer's own library list rather than the kind's generic name. */
  scopeLabel?: (kind: string) => string;
  canSaveView: boolean;
  showSaveView?: boolean;
  artwork: ShellArtwork;
  env: TextEnvironment;
  reducedMotion: boolean;
  owner: boolean;
  reportError: (error: unknown) => void;
  empty: ReactNode;
  /** Quiet owner guidance for the ordinary unmatched filter, never a badge or warning. */
  unmatchedHelp?: string;
}>;
export type LibraryBrowseBindings = Omit<LibraryBindings, 'libraries' | 'tabs' | 'onLibrary' | 'onLibraries' | 'onPivot' | 'libraryMenu'>;
const t = defaultI18n.t;
const noSelectionSubscribe = () => () => {};
const noSelectionSnapshot = () => undefined;
export const browseLabels: BrowseLabels = {
  field: value => {const id = browseNameId(value.id, value.labelKey, defaultI18n.has);return id && defaultI18n.has(id) ? t(id) : value.labelKey;},
  sort: value => {const id = browseNameId(value.id, value.labelKey, defaultI18n.has);return id && defaultI18n.has(id) ? t(id) : value.labelKey;},
  quick: value => defaultI18n.has(value.labelKey) ? t(value.labelKey) : value.labelKey,
};
export function LibraryScreen(bindings: LibraryBindings) {
  const {controller} = bindings;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const width = useShellWidth();
  useEffect(() => {void controller.load();}, [controller]);
  // A failed browse answer keeps the library the viewer is already in: its name, switcher
  // and tabs are known facts, and losing them would move the viewer out of context.
  if (state.phase === 'error') return <div className={styles.library}><LibraryIdentity {...bindings}/>
    <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: controller.retry}}/></div>;
  return <div className={styles.library}>
    <LibraryIdentity {...bindings}/>
    {state.phase === 'ready' && state.capabilities && state.sort ? <LibraryBrowse {...bindings}/> : <LoadingGrid key={bindings.library.id} width={width}/>}
    {state.phase === 'ready' && bindings.unmatchedHelp && state.predicates.some(node => 'field' in node && node.field === 'match' && node.operator === 'equals' && node.value === 'unmatched') ? <p className={styles.unmatchedHelp}>{bindings.unmatchedHelp}</p> : null}
  </div>;
}
export function LibraryIdentity({library, libraries, pivot, tabs, onPivot, onLibrary, onLibraries, libraryMenu, appearance: base, preferences, libraryViewOverride, artwork, wash, counts}: Pick<LibraryBindings,
  'library' | 'libraries' | 'pivot' | 'tabs' | 'onPivot' | 'onLibrary' | 'onLibraries' | 'libraryMenu' | 'appearance' | 'preferences' | 'libraryViewOverride' | 'artwork'> & Readonly<{
  /** A surface's own drawn wash, in place of the single latest-artwork ambient. */
  wash?: ReactNode;
  /** The drawn count line under the library name; absent draws none. */
  counts?: readonly FactPart[];
}>) {
  const appearance = browsePresentation(base, preferences, libraryViewOverride);
  const compact = useShellWidth() < 600;
  const switcher = <LibrarySwitcher libraries={libraries} value={library.id} onValueChange={onLibrary} compact={compact}/>;
  const navigation = tabs.length && onPivot ? <Tabs className={compact ? styles.phoneTabs : styles.headerTabs} value={pivot} onValueChange={onPivot} items={tabs} phone={compact}/> : undefined;
  const ambient = appearance.backdrops ? wash ?? (library.latestArtwork ? <AmbientArt source={artwork.ambient(library.latestArtwork, {group: library.id, order: 0})} treatment="library" width="100%" height={560}/> : undefined) : undefined;
  return compact ? <StickyBrowseHeader name={library.name} switcher={switcher} menu={libraryMenu} onLibraries={onLibraries} topInset={0}>{navigation}</StickyBrowseHeader> :
    <LibraryHeader name={library.name} counts={counts} switcher={switcher} menu={libraryMenu} ambient={ambient}>{navigation}</LibraryHeader>;
}
export function LibraryBrowse(bindings: LibraryBrowseBindings) {
  const {controller, library, env, selection, reportError} = bindings;
  const appearance = browsePresentation(bindings.appearance, bindings.preferences, bindings.libraryViewOverride);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const snapshot = useSyncExternalStore(controller.collection.subscribe, controller.collection.getSnapshot, controller.collection.getSnapshot);
  const selected = useSyncExternalStore(selection.playSelection?.subscribe ?? noSelectionSubscribe, selection.playSelection?.getSnapshot ?? noSelectionSnapshot, selection.playSelection?.getSnapshot ?? noSelectionSnapshot);
  const selectionActive = selection.playSelection ? selected?.phase === 'ready' && selected.selection?.active === true : undefined;
  const [filtersOpen, setFiltersOpen] = useState(false), width = useShellWidth(), compact = width < 600, coarse = useCoarsePointer();
  const docked = width >= 1024 && coarse;
  const {wrap: wrapCard, menu: cardMenu} = useLibraryCardMenu({admission: controller, queryRevision: state.queryRevision});
  const cardActions = useCallback((member: BrowseMember, index: number) => wrapCard(bindings.selection.actions(member, index), member.entry), [wrapCard, bindings.selection]);
  const model = useMemo(() => state.capabilities && state.sort ? browseToolbarModel(state.capabilities, library.kind, state.predicates, state.sort, {...env, owner: bindings.owner, scope: controller.source.base?.length ? 'group' : 'library'}) : undefined,
    [state.capabilities, state.sort, state.predicates, library.kind, env, bindings.owner, controller]);
  const sort = useCallback((sort: BrowseSortSelection) => {try {controller.setQuery(state.predicates, sort, state);} catch (error) {reportError(error);}}, [controller, state, reportError]);
  if (!model) return null;
  const pivot = state.capabilities?.resolvedPivot ?? state.capabilities?.pivots.find(pivot => pivot.id === controller.source.pivot);
  const views = (pivot?.supportedViews ?? []).filter((view): view is BrowsePresentation['view'] => view === 'grid' || view === 'list');
  const view = views.find(view => view === appearance.view) ?? views[0];
  if (!view) return null;
  const chips = model.applied.flatMap((node, index) => 'field' in node ? [{id: String(index), parts: appliedFilterParts(node, id => {const field = state.capabilities!.fields.find(field => field.id === id);return field ? browseLabels.field(field) : id;}, env)}] : []);
  const setPredicates = (nodes: readonly BrowseNode[]) => {try {controller.setQuery(nodes, undefined, state);} catch (error) {bindings.reportError(error);}};
  // The retained count describes the same accepted generation still painted by
  // the collection. A pending replacement cannot use it as its new match count.
  const resultCount = snapshot.total === undefined ? undefined : t(snapshot.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: snapshot.total, pivot: controller.source.pivot});
  const filterCount = snapshot.refreshing || snapshot.status !== 'ready' ? undefined : resultCount;
  const shape = bindings.shape ?? (bindings.pivot === 'collections' ? 'collection' : ['music', 'audiobook'].includes(library.kind) ? 'square' : 'poster');
  return <>
    <div className={styles.browseContent} data-filters-docked={docked && filtersOpen}>
    <BrowseToolbar model={model} labels={browseLabels} platform={compact ? 'phone' : 'desktop'} resultCount={resultCount} accepted={snapshot} acceptedSource={controller.collection} topInset={compact ? 94 : width < 1024 || coarse ? 0 : 76} sticky
      onQuickChange={(id, checked) => {const quick = [...model.quick, ...model.filterQuick].find(quick => quick.id === id);if (quick) setPredicates(checked ? [...state.predicates, quick.query] : state.predicates.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query)));}}
      onFilters={() => setFiltersOpen(true)} filtersOpen={filtersOpen} onSortChange={sort}
      onReshuffle={() => sort({...model.sort, seed: crypto.getRandomValues(new Uint32Array(1))[0]})}
      sortLabel={bindings.sortLabel} quickIds={bindings.quickIds}
      view={views.includes('grid') && views.includes('list') ? {value: view, onValueChange: view => bindings.onAppearanceChange({view})} : undefined}
      cardSize={!compact && view === 'grid' ? {value: appearance.cardSize, options: (['small', 'medium', 'large'] as const).map(value => ({value, label: t(`next.browse.${value}`)})), onValueChange: value => {if (value === 'small' || value === 'medium' || value === 'large') bindings.onAppearanceChange({cardSize: value});}} : undefined}
      actions={<BrowseScopeChips controller={controller} libraries={bindings.scopeLibraries} reportError={reportError} labelFor={bindings.scopeLabel}/>} save={bindings.showSaveView === false || !bindings.onSaveView ? undefined : {canSave: bindings.canSaveView, onSave: bindings.onSaveView}} applied={chips}
      onRemoveFilter={id => setPredicates(state.predicates.filter(node => node !== model.applied[Number(id)]))} onClearFilters={() => setPredicates([])} selection={selection.toolbar} selectionActive={selectionActive}/>
    <BrowseResults controller={controller} label={library.name} view={view} cardWidth={appearance.cardWidth} shape={shape} artwork={bindings.artwork} env={env} backdrops={appearance.backdrops}
      reducedMotion={bindings.preferences?.reducedMotion ?? bindings.reducedMotion} actions={cardActions} familyForEntry={bindings.familyForEntry} onVisibleRange={selection.registerWindow} onSort={sort}
      empty={state.predicates.length ? <BrowseFilteredEmpty controller={controller} model={model} labels={browseLabels} predicates={state.predicates}
        library={library.name} onDrop={setPredicates} onClear={() => setPredicates([])}/> : bindings.empty}/>
    </div>
    <BrowseFilters controller={controller} model={model} labels={browseLabels} compact={compact} medium={width >= 600 && width < 1024} docked={docked} open={filtersOpen} resultCount={filterCount} canSaveView={bindings.canSaveView} onSaveView={bindings.onSaveView} onClose={() => setFiltersOpen(false)} reportError={bindings.reportError}/>
    {cardMenu}
  </>;
}
