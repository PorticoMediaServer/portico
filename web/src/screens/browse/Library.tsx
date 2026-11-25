import {useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode} from 'react';
import {useCoarsePointer} from '../../kit/theme';
import {defaultI18n, type I18n} from '@i18n';
import {useI18n} from '../../shell/i18n';
import type {BrowseNode, BrowseSortSelection} from '@core/browse.ts';
import type {Library} from '@core/index.ts';
import {browseNameId} from '@core/presentation/browse-toolbar.ts';
import {filterValueName, namedFilterFields} from '@core/filter-value-names.ts';
import {appliedFilterParts, browseToolbarModel, type FactPart, type TextEnvironment} from '@portico/client-core/kit-model';
import {LibraryHeader, LibrarySwitcher, StickyBrowseHeader, BrowseToolbar, type LibraryChoice, type BrowseLabels} from '../../kit/browse';
import {Tabs} from '../../kit/controls';
import {AmbientArt} from '../../kit/artwork';
import {BrowseFrame, type BrowseHeaderProps} from '../../kit/frames';
import type {ShellArtwork} from '../../shell/artwork';
import {useShellWidth} from '../../shell/viewport';
import {BrowseResults} from './BrowseResults';
import {useLibraryFilters} from './Filters';
import {BrowseFilteredEmpty} from './FilteredEmpty';
import {BrowseScopeChips} from './ScopeChips';
import {useLibraryCardMenu} from './use-card-menu';
import type {ScreenContext} from '../detail/context';
import type {BrowseController, BrowseMember} from './controller';
import type {BrowseItemActions} from './EntryCard';
import styles from './browse.module.css';
import {browsePresentation, type BrowsePresentation, type ScreenAppearance} from './appearance';
import type {LibraryPlaySelection} from './play-selection';

export type {BrowsePresentation} from './appearance';
export type LibraryBindings = Readonly<{
  controller: BrowseController;
  /** The screen's captured context: the title menu's effects, authority and currentness. */
  menuContext: ScreenContext;
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
const noSelectionSubscribe = () => () => {};
const kindShape = (kind: string): 'square' | 'poster' => ['music', 'audiobook'].includes(kind) ? 'square' : 'poster';
const noSelectionSnapshot = () => undefined;
function makeBrowseLabels(i18n: I18n): BrowseLabels {
  return {
    field: value => {const id = browseNameId(value.id, value.labelKey, i18n.has);return id && i18n.has(id) ? i18n.t(id) : value.labelKey;},
    sort: value => {const id = browseNameId(value.id, value.labelKey, i18n.has);return id && i18n.has(id) ? i18n.t(id) : i18n.has(value.labelKey) ? i18n.t(value.labelKey) : value.labelKey;},
    quick: value => i18n.has(value.labelKey) ? i18n.t(value.labelKey) : value.labelKey,
  };
}
export const browseLabels: BrowseLabels = makeBrowseLabels(defaultI18n);
/** A library tab's grid on the library frame (`BrowseFrame`): the library's header, its toolbar, the grid and Filters. Until
 *  its grid has come from the viewer's cache (`controller` absent) the frame draws the header over the grid's loading boxes. */
export function LibraryScreen(props: Omit<LibraryBindings, 'controller'> & {controller?: LibraryBindings['controller']}) {
  const {controller} = props, header = useLibraryHeader(props);
  // `grid` says the loading boxes are a grid's (the frame draws its own while `state` is loading).
  if (!controller) return <BrowseFrame header={header} grid state={{kind: 'loading'}}/>;
  return <LoadedLibraryScreen {...props} controller={controller}/>;
}
function LoadedLibraryScreen(bindings: LibraryBindings) {
  const {t} = useI18n() ?? defaultI18n;
  const {controller} = bindings;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const header = useLibraryHeader(bindings);
  // A failed browse answer keeps the library the viewer is already in: its name, switcher
  // and tabs are known facts, and losing them would move the viewer out of context.
  if (state.phase === 'error') return <BrowseFrame header={header} state={{icon: 'i-warn', tone: 'warning', title: t('next.states.pageFailed'), body: t('next.states.pageFailedBody'), primaryAction: {id: 'retry', label: t('next.states.tryAgain'), onPress: controller.retry}}}/>;
  if (state.phase !== 'ready' || !state.capabilities || !state.sort) return <BrowseFrame header={header} grid state={{kind: 'loading'}}/>;
  return <LibraryBrowse {...bindings} header={header} help={bindings.unmatchedHelp && state.predicates.some(node => 'field' in node && node.field === 'match' && node.operator === 'equals' && node.value === 'unmatched')
    ? <p className={styles.unmatchedHelp}>{bindings.unmatchedHelp}</p> : undefined}/>;
}
type LibraryHeaderInputs = Pick<LibraryBindings, 'library' | 'libraries' | 'pivot' | 'tabs' | 'onPivot' | 'onLibrary' | 'onLibraries' | 'libraryMenu' | 'appearance' | 'preferences' | 'libraryViewOverride' | 'artwork'> & Readonly<{
  /** A surface's own drawn wash, in place of the single latest-artwork ambient. */
  wash?: ReactNode;
  /** The drawn count line under the library name; absent draws none. */
  counts?: readonly FactPart[];
  /** A drawn header action before the ⋯ (Music Discover's Shuffle all); wide headers only, as phone-web 6 and phone-app draw none. */
  action?: ReactNode;
}>;
/** A library's header regions for `BrowseFrame`: its name is the switcher, ‹ Libraries on a phone, ⋯, the tabs, the counts and
 *  its wash. One place builds them; `LibraryIdentity` draws the same header for the library pages not yet on the frame. */
export function useLibraryHeader({library, libraries, pivot, tabs, onPivot, onLibrary, onLibraries, libraryMenu, appearance: base, preferences, libraryViewOverride, artwork, wash, counts, action}: LibraryHeaderInputs): BrowseHeaderProps {
  const {t} = useI18n() ?? defaultI18n;
  const appearance = browsePresentation(base, preferences, libraryViewOverride);
  return {title: library.name, back: {label: t('next.browse.libraries'), onPress: onLibraries}, counts, trailing: action, menu: libraryMenu,
    switcher: <LibrarySwitcher libraries={libraries} value={library.id} onValueChange={onLibrary} compact={useShellWidth() < 600}/>,
    tabs: tabs.length && onPivot ? {items: tabs, value: pivot, onValueChange: onPivot} : undefined,
    ambient: appearance.backdrops ? wash ?? (library.latestArtwork ? <AmbientArt source={artwork.ambient(library.latestArtwork, {group: library.id, order: 0})} treatment="library" width="100%" height={560}/> : undefined) : undefined};
}
/** The library header alone, for the library pages not yet on the frame (Discover, reading libraries, unmatched). */
export function LibraryIdentity(props: LibraryHeaderInputs) {
  const header = useLibraryHeader(props), compact = useShellWidth() < 600;
  const navigation = header.tabs && <Tabs className={compact ? styles.phoneTabs : styles.headerTabs} value={header.tabs.value} onValueChange={header.tabs.onValueChange} items={header.tabs.items} phone={compact}/>;
  return compact ? <StickyBrowseHeader name={header.title} switcher={header.switcher} menu={header.menu} onLibraries={props.onLibraries} topInset={0}>{navigation}</StickyBrowseHeader> :
    <LibraryHeader name={header.title} counts={header.counts} switcher={header.switcher} menu={header.trailing ? <>{header.trailing}{header.menu}</> : header.menu} ambient={header.ambient}>{navigation}</LibraryHeader>;
}
export type LibraryBrowseFrameBindings = LibraryBrowseBindings & Readonly<{
  /** The frame's header: the library's (`useLibraryHeader`), or a pushed list's Back, title, intro and Play / Shuffle. */
  header: BrowseHeaderProps;
  /** A quiet line under the grid (the owner's help for the unmatched filter). */
  help?: ReactNode;
}>;
export function LibraryBrowse(bindings: LibraryBrowseFrameBindings) {
  const i18n = useI18n() ?? defaultI18n, t = i18n.t;
  const labels = useMemo(() => makeBrowseLabels(i18n), [i18n]);
  const {controller, library, env: bindingEnv, selection, reportError} = bindings;
  const env = useMemo(() => i18n === defaultI18n ? bindingEnv : {t: i18n.t, locale: i18n.region.formatLocale}, [i18n, bindingEnv]);
  const appearance = browsePresentation(bindings.appearance, bindings.preferences, bindings.libraryViewOverride);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const snapshot = useSyncExternalStore(controller.collection.subscribe, controller.collection.getSnapshot, controller.collection.getSnapshot);
  const selected = useSyncExternalStore(selection.playSelection?.subscribe ?? noSelectionSubscribe, selection.playSelection?.getSnapshot ?? noSelectionSnapshot, selection.playSelection?.getSnapshot ?? noSelectionSnapshot);
  const selectionActive = selection.playSelection ? selected?.phase === 'ready' && selected.selection?.active === true : undefined;
  const [filtersOpen, setFiltersOpen] = useState(false), [oneKind, setOneKind] = useState<string>(), width = useShellWidth(), compact = width < 600, coarse = useCoarsePointer();
  const {wrap: wrapCard, menu: cardMenu} = useLibraryCardMenu({admission: controller, queryRevision: state.queryRevision, context: bindings.menuContext});
  const cardActions = useCallback((member: BrowseMember, index: number) => wrapCard(bindings.selection.actions(member, index), member.entry), [wrapCard, bindings.selection]);
  const model = useMemo(() => state.capabilities && state.sort ? browseToolbarModel(state.capabilities, library.kind, state.predicates, state.sort, {...env, owner: bindings.owner, scope: controller.source.base?.length ? 'group' : 'library'}) : undefined,
    [state.capabilities, state.sort, state.predicates, library.kind, env, bindings.owner, controller]);
  const sort = useCallback((sort: BrowseSortSelection) => {try {controller.setQuery(state.predicates, sort, state);} catch (error) {reportError(error);}}, [controller, state, reportError]);
  // An author, series or collection chip whose name this app has not listed yet (a link, a reload): read each
  // of its ids once so the chip says the name, not the id, wherever it sits in the library's list.
  const [, named] = useState(0);
  const unnamed = [...new Set((model?.applied ?? []).flatMap(node => 'field' in node && namedFilterFields.has(node.field)
    ? (Array.isArray(node.value) ? node.value : [node.value]).flatMap(value => typeof value === 'string' && filterValueName(value) === undefined ? [`${node.field}:${value}`] : []) : []))].join(',');
  useEffect(() => {
    if (!unnamed) return;
    const stop = new AbortController(), wanted = unnamed.split(',').map(pair => pair.split(':') as ['author' | 'series' | 'collection', string]);
    void Promise.all((['author', 'series', 'collection'] as const).map(field => controller.nameValues(field, wanted.flatMap(([each, id]) => each === field ? [id] : []), stop.signal)))
      .then(() => {if (!stop.signal.aborted) named(count => count + 1);}, () => undefined);
    return () => stop.abort();
  }, [unnamed, controller]);
  if (!model) return <BrowseFrame header={bindings.header} grid state={{kind: 'loading'}}/>;
  const pivot = state.capabilities?.resolvedPivot ?? state.capabilities?.pivots.find(pivot => pivot.id === controller.source.pivot);
  const views = (pivot?.supportedViews ?? []).filter((view): view is BrowsePresentation['view'] => view === 'grid' || view === 'list');
  const view = views.find(view => view === appearance.view) ?? views[0];
  if (!view) return <BrowseFrame header={bindings.header}/>;
  const chips = model.applied.flatMap((node, index) => 'field' in node ? [{id: String(index), parts: appliedFilterParts(node, id => {const field = state.capabilities!.fields.find(field => field.id === id);return field ? labels.field(field) : id;}, env,
    value => filterValueName(value) ?? (namedFilterFields.has(node.field) ? '' : String(value)))}] : []);
  const setPredicates = (nodes: readonly BrowseNode[]) => {try {controller.setQuery(nodes, undefined, state);} catch (error) {bindings.reportError(error);}};
  // The retained count describes the same accepted generation still painted by
  // the collection. A pending replacement cannot use it as its new match count.
  const resultCount = snapshot.total === undefined ? undefined : t(snapshot.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: snapshot.total, pivot: controller.source.pivot});
  const resultTotal = snapshot.refreshing || snapshot.status !== 'ready' || snapshot.totalIsLowerBound ? undefined : snapshot.total;
  const filterCount = snapshot.refreshing || snapshot.status !== 'ready' ? undefined : resultCount;
  // A grid of one kind takes that kind's card shape: the scope chip chosen, the only kind a mixed row holds, or the one library
  // a row or tab belongs to. A mixed row keeps the server's shape (posters for All).
  const one = oneKind ?? (library.kind || undefined);
  const shape = bindings.pivot === 'collections' || bindings.shape === 'collection' ? 'collection' : one ? kindShape(one) : bindings.shape ?? 'poster';
  const toolbar = <BrowseToolbar model={model} labels={labels} platform={compact ? 'phone' : 'desktop'} resultCount={resultCount} resultTotal={resultTotal} accepted={snapshot} acceptedSource={controller.collection} topInset={compact ? 94 : width < 1024 || coarse ? 0 : 76} sticky
      onQuickChange={(id, checked) => {const quick = [...model.quick, ...model.filterQuick].find(quick => quick.id === id);if (quick) setPredicates(checked ? [...state.predicates, quick.query] : state.predicates.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query)));}}
      onFilters={() => setFiltersOpen(true)} filtersOpen={filtersOpen} onSortChange={sort}
      onReshuffle={() => sort({...model.sort, seed: crypto.getRandomValues(new Uint32Array(1))[0]})}
      sortLabel={bindings.sortLabel} quickIds={bindings.quickIds}
      view={views.includes('grid') && views.includes('list') ? {value: view, onValueChange: view => bindings.onAppearanceChange({view})} : undefined}
      cardSize={!compact && view === 'grid' ? {value: appearance.cardSize, options: (['small', 'medium', 'large'] as const).map(value => ({value, label: t(`next.browse.${value}`)})), onValueChange: value => {if (value === 'small' || value === 'medium' || value === 'large') bindings.onAppearanceChange({cardSize: value});}} : undefined}
      actions={<BrowseScopeChips controller={controller} libraries={bindings.scopeLibraries} reportError={reportError} labelFor={bindings.scopeLabel} onKind={setOneKind}/>} save={bindings.showSaveView === false || !bindings.onSaveView ? undefined : {canSave: bindings.canSaveView, onSave: bindings.onSaveView}} applied={chips}
      onRemoveFilter={id => setPredicates(state.predicates.filter(node => node !== model.applied[Number(id)]))} onClearFilters={() => setPredicates([])} selection={selection.toolbar} selectionActive={selectionActive}/>;
  const grid = <BrowseResults controller={controller} label={library.name} view={view} cardWidth={appearance.cardWidth} shape={shape} artwork={bindings.artwork} env={env} backdrops={appearance.backdrops}
      reducedMotion={bindings.preferences?.reducedMotion ?? bindings.reducedMotion} actions={cardActions} familyForEntry={bindings.familyForEntry} onVisibleRange={selection.registerWindow} onSort={sort}
      empty={state.predicates.length ? <BrowseFilteredEmpty controller={controller} model={model} labels={labels} predicates={state.predicates}
        library={library.name} onDrop={setPredicates} onClear={() => setPredicates([])}/> : bindings.empty}/>;
  return <LibraryFrame header={bindings.header} toolbar={toolbar} grid={grid} help={bindings.help} cardMenu={cardMenu}
    filters={{controller, model, labels, compact, open: filtersOpen, resultCount: filterCount, canSaveView: bindings.canSaveView, onSaveView: bindings.onSaveView, onClose: () => setFiltersOpen(false), reportError: bindings.reportError}}/>;
}

/** The frame for a loaded grid: its Filters region is the library's own (`useLibraryFilters`), so it runs once the model is known. */
function LibraryFrame({header, toolbar, grid, help, cardMenu, filters}: Readonly<{header: BrowseHeaderProps; toolbar: ReactNode; grid: ReactNode; help?: ReactNode; cardMenu: ReactNode; filters: Parameters<typeof useLibraryFilters>[0]}>) {
  // The owner's unmatched-filter help is a line under the toolbar: the frame's grid fills the room below it.
  return <><BrowseFrame header={header} toolbar={<>{toolbar}{help}</>} grid={grid} filters={useLibraryFilters(filters)}/>{cardMenu}</>;
}
