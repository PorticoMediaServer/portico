import {ReaderHighlightsExport} from './ReaderHighlightsExport';
import type {ReadingAnnotationPort} from '@core/reading-annotations.ts';
import {randomId} from '@core/random-id.ts';
import {readingLocationFromReader, readerErrorCode} from '@portico/reader-core';
import {ReaderAnnotationActions, ReaderAnnotationList, useReaderAnnotations, useAnnotationLease} from './ReaderAnnotations';
import {readingSearchStepIndex} from './search-step';
import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {ReadingPreferences} from '@portico/client-core';
import type {ReadingController, ReadingPublication, ReaderErrorCode, ReaderLocation, ReaderContentsTarget, ReaderSearchResult, ReaderTocItem} from '@portico/reader-core';
import {useShellWidth} from '../../shell/viewport';
import {useWindowHeight} from '../../kit/theme';
import {Skeleton, StateView} from '../../kit/states';
import {ReaderContents, ReaderPanel, type ReaderContentsEntry} from '../../kit/reader/ReaderPanel';
import {ReaderDisplayMark} from '../../kit/reader/ReaderDisplayMark';
import {ReaderSearch, type ReaderSearchHit} from '../../kit/reader/ReaderSearch';
import {ReadReader} from './ReadReader';
import {ReadingDisplay} from './ReadingDisplay';
import {ReaderFirstRunHint} from './ReaderFirstRunHint';
import {ComicPageFooter, FixedPageFooter, FixedPageMenus, FixedPageZoom, FixedPageStrip, FixedPagesPanel, fixedPageZoomLabel} from './fixed-page';
import {readingDisplayCopy} from './display-copy';
import {readerBookPresentation, type ReaderEnvironment} from './native/tablet-presentation';
import type {ReaderMountAuthority} from './mount';
import type {ReaderChapterMark} from './ReaderChrome';
import fixedStyles from './fixed-page.module.css';
import {ReaderMatchNavigation} from './ReaderMatchNavigation';
import {ReaderBrowserHint} from './ReaderBrowserHint';
const {t} = defaultI18n;
const directionIcon = (rtl: boolean): 'i-rtl' | 'i-ltr' => rtl ? 'i-rtl' : 'i-ltr';
/** Real outline boundaries, including nested chapters; coincident starts draw one tick. */
export function readerChapterMarks(items: readonly ReaderTocItem[], location?: ReaderLocation): readonly ReaderChapterMark[] {
  const outline: ReaderTocItem[] = [];
  const visit = (entries: readonly ReaderTocItem[]) => {for (const entry of entries) {
    const start = entry.location.progression;
    if (Number.isFinite(start) && start >= 0 && start <= 1) outline.push(entry);
    if (entry.children) visit(entry.children);
  }};
  visit(items);
  outline.sort((left, right) => left.location.progression - right.location.progression);
  const boundaries = outline.filter((entry, index) => index === 0 || entry.location.progression !== outline[index - 1].location.progression);
  return boundaries.map((item, index) => {
    const start = item.location.progression, end = boundaries[index + 1]?.location.progression ?? 1;
    return {id: item.id, start, end, current: location && location.format === item.location.format
      ? location.progression >= start && location.progression < end : undefined};
  });
}
/** Fixed page ordinals/counts are actual renderer facts, never inferred EPUB pages. */
export function readerPagePosition(location: ReaderLocation | null, count: number | undefined): string | undefined {
  if (!location) return;
  if (location.format !== 'epub' && count !== undefined && Number.isSafeInteger(count) && count > 0) {
    return t('next.reader.pageOf', {page: location.pageLabel ?? defaultI18n.number(location.page + 1), count: defaultI18n.number(count)});
  }
  return location.pageLabel ? t('next.reader.pageLabel', {page: location.pageLabel}) : undefined;
}
/** Use actual ordered outline positions and the live location, without inventing read/completion facts. */
export function readerCurrentContentsId(items: readonly ReaderTocItem[], location: ReaderLocation | null): string | undefined {
  if (!location || !Number.isFinite(location.progression)) return;
  const flat: ReaderTocItem[] = [];
  const visit = (entries: readonly ReaderTocItem[]) => {for (const entry of entries) {flat.push(entry); if (entry.children) visit(entry.children);}};
  visit(items);
  let current: ReaderTocItem | undefined;
  for (const item of flat) {
    if (item.location.format !== location.format || !Number.isFinite(item.location.progression)) return;
    if (item.location.progression <= location.progression && (!current || item.location.progression > current.location.progression)) current = item;
  }
  // Equal starts are ambiguous (for example a parent and its first child).
  if (!current) return;
  const start = current.location.progression;
  if (flat.filter(item => item.location.progression === start).length === 1) return current.id;
}
type ReaderSearchScope = Readonly<{query: string; active: boolean; abort: AbortController}>;
export type ReaderBookState = Readonly<{publication: ReadingPublication; controller: ReadingController; authority: ReaderMountAuthority; location?: ReaderLocation; contentsTarget?: ReaderContentsTarget; annotations?: ReadingAnnotationPort}>;

export function ReaderBook({book, active, reportError, onBack, onOpenSettings, onSearchAll, environment = 'web', crease, saveHighlights}: {saveHighlights?: (revision: number, signal: AbortSignal) => Promise<'saved' | 'cancelled'>; environment?: ReaderEnvironment; book: ReaderBookState; active: boolean; reportError: (error: unknown) => void; onBack: () => void; onOpenSettings: () => void; onSearchAll?: (query: string) => void; /** Actual vertical book fold for this window, or absent. */ crease?: Readonly<{left: number; right: number}>}) {
  const {controller, publication, authority} = book;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), width = useShellWidth(), height = useWindowHeight();
  const [panel, updatePanel] = useState<'display' | 'contents' | 'search'>(), [query, setQuery] = useState('');
  const [fixedMenu, setFixedMenu] = useState<'zoom' | 'direction' | 'more'>();
  const [fixedPane, updateFixedPane] = useState<'contents' | 'pages' | 'bookmarks' | 'highlights'>('contents');
  const annotationPanel=useRef(new AbortController());
  const setPanel=(next:typeof panel)=>{annotationPanel.current.abort();annotationPanel.current=new AbortController();updatePanel(next);};
  const setFixedPane=(next:typeof fixedPane)=>{annotationPanel.current.abort();annotationPanel.current=new AbortController();updateFixedPane(next);};
  const zoomAnchor = useRef<HTMLButtonElement>(null), directionAnchor = useRef<HTMLButtonElement>(null), moreAnchor = useRef<HTMLButtonElement>(null);
  const fixed = publication.format !== 'epub', comic = publication.format === 'cbz' || publication.format === 'cbr';
  const notes = useReaderAnnotations(book.annotations, controller, authority, active, snapshot.status === 'ready');
  const annotationLease=useAnnotationLease(notes.service);
  const listSignal=annotationPanel.current.signal;
  if(!active)annotationPanel.current.abort();
  useLayoutEffect(()=>{const original=annotationPanel.current;return()=>original.abort();},[]);
  const [hits, setHits] = useState<readonly ReaderSearchResult[]>([]), [searching, setSearching] = useState(false), [currentHit, setCurrentHit] = useState<string>();
  // phone-read 14: taught once per browser, never re-shown after Got it.
  const hintKey = 'portico.reader.tapZonesHint.v1';
  const [hint, setHint] = useState(() => {try {return !globalThis.localStorage?.getItem(hintKey);} catch {return true;}});
  const dismissHint = useCallback(() => {setHint(false); try {globalThis.localStorage?.setItem(hintKey, '1');} catch { /* Disabled storage simply keeps the hint dismissed for this mount. */ }}, []);
  const [hitsQuery, setHitsQuery] = useState('');
  const queryRef = useRef(query); queryRef.current = query;
  const searchMove = useRef<object | undefined>(undefined);
  const activeRef = useRef(active); activeRef.current = active;
  const [searchScope, setSearchScope] = useState<ReaderSearchScope>();
  const searchScopeRef = useRef<ReaderSearchScope | undefined>(undefined);
  const [hitsScope, setHitsScope] = useState<ReaderSearchScope>();
  const visibleHits = hitsQuery === query && hitsScope === searchScope ? hits : [];
  useLayoutEffect(() => {
    const scope: ReaderSearchScope = Object.freeze({query, active, abort: new AbortController()});
    searchScopeRef.current = scope; setSearchScope(scope);
    return () => {scope.abort.abort(); if (searchScopeRef.current === scope) searchScopeRef.current = undefined;};
  }, [query, active, controller, authority, publication]);
  const changeQuery = (next: string) => {
    const scope = searchScope;
    if (!scope || searchScopeRef.current !== scope || !activeRef.current || queryRef.current !== query || next === query) return;
    // Retire queued/in-flight commands before the input state can be replaced.
    scope.abort.abort(); setQuery(next);
  };
  const anchor = useRef<HTMLButtonElement>(null);
  const contentsError = useRef(reportError); contentsError.current = reportError;
  useEffect(() => {
    const target = book.contentsTarget;
    if (!target) return;
    const operation = new AbortController(); let applied = false;
    const apply = () => {
      if (applied || operation.signal.aborted || controller.getSnapshot().status !== 'ready' || !authority.isCurrent()) return;
      applied = true;
      try {
        const current = publication.contentsTarget(target.index);
        if (current.sourceId !== target.sourceId || current.sourceRevision !== target.sourceRevision) throw new Error('source_changed');
        void controller.navigateContents(current, operation.signal).catch(error => {
          if (!operation.signal.aborted && authority.isCurrent()) contentsError.current(error);
        });
      } catch (error) {if (authority.isCurrent()) contentsError.current(error);}
    };
    const off = controller.subscribe(apply); apply();
    return () => {operation.abort(); off();};
  }, [book.contentsTarget, publication, authority, controller]);
  const engineError = useCallback((code: ReaderErrorCode) => reportError({code}), [reportError]);
  const layout = readerBookPresentation(width, environment, height), {presentation} = layout;
  const compactDocument = publication.format === 'pdf' && presentation === 'phone';
  useEffect(() => {if (!active) {setPanel(undefined); setFixedMenu(undefined);}}, [active]);
  const navigate = (location: ReaderLocation) => {
    const current = controller.getSnapshot();
    if (!active || !authority.isCurrent() || publication.signal.aborted || current.status !== 'ready'
      || current.sourceId !== publication.source.id || current.sourceRevision !== publication.source.revision) return;
    void controller.navigate({kind: 'location', location}).catch(reportError);
    if (presentation === 'phone') setPanel(undefined);
  };
  useEffect(() => {
    const scope = searchScope;
    setCurrentHit(undefined); setHitsQuery(query); setHitsScope(scope);
    if (!scope || searchScopeRef.current !== scope || scope.query !== query || !scope.active || scope.abort.signal.aborted || !active || !query.trim()
      || snapshot.status !== 'ready' || snapshot.sourceId !== publication.source.id || snapshot.sourceRevision !== publication.source.revision) {setHits([]); setSearching(false); return;}
    const life = new AbortController(), signal = AbortSignal.any([life.signal, scope.abort.signal]); let queryCurrent = true;
    setSearching(true); setHits([]);
    void (async () => {
      const found: ReaderSearchResult[] = [];
      for await (const hit of controller.search(query, {signal, limit: 100})) {
        if (!queryCurrent || searchScopeRef.current !== scope || signal.aborted || queryRef.current !== query || !authority.isCurrent()) return;
        found.push(hit); setHits([...found]);
      }
      // Iterator exhaustion can be a bounded indexed-search stop. It does not
      // establish a complete book-wide count or a no-matches claim.
    })().catch(error => {if (queryCurrent && searchScopeRef.current === scope && !signal.aborted && authority.isCurrent()) reportError(error);})
      .finally(() => {if (queryCurrent && searchScopeRef.current === scope) setSearching(false);});
    return () => {queryCurrent = false; life.abort();};
  }, [searchScope, controller, authority, active, query, reportError, snapshot.status, snapshot.sourceId, snapshot.sourceRevision, publication]);
  const openSearchHit = (hit: ReaderSearchResult) => {
    const scope = searchScope, current = controller.getSnapshot();
    if (!hit.location || !scope || searchScopeRef.current !== scope || scope.abort.signal.aborted || !activeRef.current || searchMove.current || queryRef.current !== query || !visibleHits.includes(hit) || !active || !authority.isCurrent()
      || publication.signal.aborted || current.status !== 'ready' || current.sourceId !== publication.source.id
      || current.sourceRevision !== publication.source.revision) return;
    const operation = {}; searchMove.current = operation;
    void controller.navigateSearchResult(hit, scope.abort.signal).then(() => {
      const accepted = controller.getSnapshot();
      if (searchScopeRef.current !== scope || scope.abort.signal.aborted || searchMove.current !== operation || queryRef.current !== query || !activeRef.current || !authority.isCurrent() || publication.signal.aborted
        || accepted.status !== 'ready' || accepted.sourceId !== publication.source.id || accepted.sourceRevision !== publication.source.revision) return;
      setCurrentHit(hit.id);
      if (presentation === 'phone') setPanel(undefined);
    }).catch(error => {if (searchScopeRef.current === scope && !scope.abort.signal.aborted && queryRef.current === query && activeRef.current && authority.isCurrent() && !publication.signal.aborted) reportError(error);})
      .finally(() => {if (searchMove.current === operation) searchMove.current = undefined;});
  };
  const stepSearch = (direction: 'previous' | 'next') => {
    if (queryRef.current !== query) return;
    // Display-only indexed excerpts stay visible, but stepping can only choose
    // hits whose exact source/quote mapping produced a real location.
    const navigable = visibleHits.filter(hit => hit.location !== undefined);
    const index = readingSearchStepIndex(navigable.length, navigable.findIndex(hit => hit.id === currentHit), direction);
    const next = index === undefined ? undefined : navigable[index];
    if (next) openSearchHit(next);
  };
  const currentContentsId = snapshot.status === 'ready' && snapshot.sourceId === publication.source.id && snapshot.sourceRevision === publication.source.revision
    ? readerCurrentContentsId(snapshot.toc, snapshot.location) : undefined;
  const contents = (items: readonly ReaderTocItem[]): readonly ReaderContentsEntry[] => items.map(item => ({id: item.id, label: item.label,
    page: item.location.pageLabel, current: item.id === currentContentsId,
    remaining: item.id === currentContentsId && snapshot.location?.pagesLeftInSection !== undefined
      ? t('next.reader.sectionRemaining', {count: snapshot.location.pagesLeftInSection}) : undefined, onNavigate: () => navigate(item.location), children: item.children ? contents(item.children) : undefined}));
  const searchHits: ReaderSearchHit[] = visibleHits.map(hit => {
    const match = hit.match;
    const context = !match ? [{text: hit.excerpt}] : [{text: hit.excerpt.slice(0, match.start)},
      {text: hit.excerpt.slice(match.start, match.start + match.length), match: true}, {text: hit.excerpt.slice(match.start + match.length)}];
    return {id: hit.id, chapter: hit.chapter, page: hit.location?.pageLabel, context, ...(hit.location ? {onNavigate: () => openSearchHit(hit)} : {})};
  });
  const annotationPane = notes.service && (fixedPane === 'bookmarks' || fixedPane === 'highlights') ? fixedPane : undefined;
  const panelContent = !active ? undefined : panel === 'contents' ? <ReaderPanel label={t('next.reader.contents')} returnFocusRef={anchor}
    tabs={[...(!fixed || snapshot.toc.length > 0 ? [{id:'contents',label:t('next.reader.contents')}] : []), ...(fixed ? [{id:'pages',label:t('next.reader.pagesStrip')}] : []),
      ...(notes.service ? [{id:'bookmarks',label:t('next.reader.bookmarks')},{id:'highlights',label:t('next.reader.highlights')}] : [])]}
    selected={annotationPane ?? (fixed && !snapshot.toc.length ? 'pages' : fixedPane === 'pages' ? 'pages' : 'contents')}
    onSelect={selected => {if (selected === 'bookmarks' || selected === 'highlights' || selected === 'pages' || selected === 'contents') setFixedPane(selected);}} onClose={() => setPanel(undefined)} footer={(environment === 'web' || !!saveHighlights) && annotationPane === 'highlights' && notes.service ? <ReaderHighlightsExport key={publication.source.id + publication.source.revision} service={notes.service} signal={listSignal} nativeSave={environment === 'native' ? saveHighlights : undefined} title={publication.metadata.title} author={publication.metadata.author} reportError={reportError}/> : undefined}>
    {annotationPane && notes.service ? <ReaderAnnotationList key={fixedPane} signal={listSignal} kind={annotationPane === 'bookmarks' ? 'bookmark' : 'highlight'} service={notes.service} state={notes.state} controller={controller} reportError={reportError}/>
      : !fixed || snapshot.toc.length > 0 && fixedPane === 'contents' ? <ReaderContents entries={contents(snapshot.toc)}/>
        : <FixedPagesPanel controller={controller} authority={authority} format={publication.format} reportError={engineError} onNavigate={() => {if (presentation === 'phone') setPanel(undefined);}}/>}
  </ReaderPanel>
    : panel === 'search' ? <ReaderPanel label={t('next.reader.search')} returnFocusRef={anchor} tabs={[{id: 'search', label: t('next.reader.search')}]}
      selected="search" onSelect={() => {}} onClose={() => setPanel(undefined)}><ReaderSearch query={query} onQuery={changeQuery} hits={searchHits} current={currentHit} loading={searching}
        resultSummary={hitsQuery === query && query.trim() && !searching && visibleHits.length > 0
          ? t('next.reader.matchesShown', {count: visibleHits.length}) : undefined}
        onSearchAll={onSearchAll && query.trim() ? () => {
          const current = controller.getSnapshot(), value = query.trim();
          if (!value || !active || !authority.isCurrent() || publication.signal.aborted || current.status !== 'ready'
            || current.sourceId !== publication.source.id || current.sourceRevision !== publication.source.revision) return;
          onSearchAll(value);
        } : undefined}
        onStep={visibleHits.some(hit => hit.location !== undefined) ? stepSearch : undefined}
        copy={{search: t('next.reader.search'), clear: t('next.reader.clear'), move: t('next.reader.move'), wholeWords: t('next.reader.wholeWords'), searchAll: t('next.reader.searchAll')}}/></ReaderPanel>
      : panel === 'display' ? <ReadingDisplay controller={controller} authority={authority} copy={readingDisplayCopy(defaultI18n, presentation, 7)}
        scale={{min: 1, max: 7, step: 1, ticks: [1, 2, 3, 4, 5, 6, 7]}} presentation={presentation} popover={layout.displayPopover} label={t('next.reader.display')}
        open onOpenChange={open => {if (!open) setPanel(undefined);}} anchorRef={anchor} reportError={code => reportError({code})}
        onOpenSettings={() => {setPanel(undefined); onOpenSettings();}}/> : undefined;
  return <><ReadReader<ReadingPreferences> controller={controller} source={publication.source} format={publication.format} initialLocation={book.location} crease={crease}
    metadata={publication.metadata} signal={publication.signal} authority={authority} reportError={engineError} onBack={onBack}
    state={state => state.status === 'error' ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}/> : <Skeleton height="100%"/>}
    presentation={value => ({copy: {back: t('next.reader.back'), previous: t('next.reader.previous'), next: t('next.reader.next'), progress: t('next.reader.position'), showControls: t('next.reader.hintShowControls')},
      holdOpen: fixedMenu !== undefined, presentation,
      direction: comic && value.preferences.comicDirection !== 'file' ? value.preferences.comicDirection === 'rtl' ? 'rtl' : 'ltr'
        : value.metadata?.direction === 'rtl' ? 'rtl' : 'ltr', tapTurnEnabled: value.preferences.tapToTurn, showPageArrows: layout.showPageArrows,
      chapters: readerChapterMarks(value.toc, value.location ?? undefined),
      chapter: value.location?.chapter, pages: readerPagePosition(value.location, value.metadata?.pageCount),
      remainingInSection: value.location?.pagesLeftInSection === undefined ? undefined : t('next.reader.sectionRemaining', {count: value.location.pagesLeftInSection}),
      hint: presentation === 'phone' && hint && !panel && value.status === 'ready' && value.preferences.tapToTurn && value.selection === null ? <ReaderFirstRunHint onDone={dismissHint} swipe={publication.format === 'epub' && value.preferences.layout === 'pages'}
        direction={comic && value.preferences.comicDirection !== 'file' ? value.preferences.comicDirection === 'rtl' ? 'rtl' : 'ltr' : value.metadata?.direction === 'rtl' ? 'rtl' : 'ltr'}/> : undefined,
      percent: value.location ? defaultI18n.percent(value.location.progression) : undefined,
      searchNavigation: active && value.status === 'ready' && value.sourceId === publication.source.id && value.sourceRevision === publication.source.revision && presentation === 'phone' && currentHit && visibleHits.some(hit => hit.id === currentHit)
        ? <ReaderMatchNavigation query={query} onStep={stepSearch} onClose={() => changeQuery('')}/> : undefined, panel: panelContent, panelReflows: layout.panelReflows(panel),
      fixedPageFooter: comic ? <ComicPageFooter controller={controller} authority={authority} format={publication.format} reportError={engineError} crease={crease} presentation={presentation}/>
        : compactDocument ? <FixedPageFooter controller={controller} authority={authority} format={publication.format} reportError={engineError}
        pages={readerPagePosition(value.location, value.metadata?.pageCount)} chapters={readerChapterMarks(value.toc, value.location ?? undefined)}
        onPages={() => {setFixedPane('pages'); setPanel('contents');}} onContents={() => {setFixedPane('contents'); setPanel('contents');}}
        onFit={element => {zoomAnchor.current = element; setFixedMenu(fixedMenu === 'zoom' ? undefined : 'zoom');}}/> : undefined,
      fixedPageZoom: publication.format === 'pdf' && value.location && value.location.format !== 'epub' && value.location.viewport
        ? <FixedPageZoom controller={controller} authority={authority} format={publication.format} reportError={engineError} viewport={value.location.viewport} presentation={presentation}/> : undefined,
      fixedPageStrip: publication.format === 'pdf' && !compactDocument ? <FixedPageStrip controller={controller} authority={authority} format={publication.format} reportError={engineError}/> : undefined,
      onPanelDismiss: () => setPanel(undefined), actions: [
        ...(compactDocument ? [{id: 'search', label: t('next.reader.search'), icon: 'i-search' as const, expanded: panel === 'search', onPress: (element: HTMLButtonElement) => {anchor.current = element; setPanel(panel === 'search' ? undefined : 'search');}}] : []),
        ...(notes.service ? [{id:'bookmark',label:t('next.reader.addBookmark'),icon:'i-bookmark' as const,
          disabled:notes.state.loading || notes.state.saving || notes.state.revision === undefined || !value.location,
          onPress:()=>{if (!notes.service?.current() || controller.getSnapshot() !== value || !value.location) return;
            if(annotationLease&&!annotationLease.retired)void notes.service.write({id:randomId(),kind:'bookmark',location:readingLocationFromReader(value.location),text:'',note:'',colour:''},{revision:notes.state.revision,signal:annotationLease.life.signal,isCurrent:()=>!annotationLease.retired&&authority.isCurrent()&&controller.getSnapshot()===value});}}] : []),
        ...(!compactDocument ? [{id: 'contents', label: t('next.reader.contents'), icon: 'i-chapters' as const, expanded: panel === 'contents', onPress: (element: HTMLButtonElement) => {anchor.current = element; setPanel(panel === 'contents' ? undefined : 'contents');}}] : []),
        ...(publication.format !== 'cbz' && !compactDocument ? [{id: 'search', label: t('next.reader.search'), icon: 'i-search' as const, expanded: panel === 'search', onPress: (element: HTMLButtonElement) => {anchor.current = element; setPanel(panel === 'search' ? undefined : 'search');}}] : []),
        ...(publication.format === 'epub' ? [{id: 'display', label: t('next.reader.displayAction'), text: <ReaderDisplayMark sample={t('next.reader.sample')}/>, expanded: panel === 'display', onPress: (element: HTMLButtonElement) => {anchor.current = element; setPanel(panel === 'display' ? undefined : 'display');}}] : []),
        ...(compactDocument ? [{id: 'more', label: t('next.controls.more'), icon: 'i-more' as const, expanded: fixedMenu === 'more',
          onPress: (element: HTMLButtonElement) => {moreAnchor.current = element; setFixedMenu(fixedMenu === 'more' ? undefined : 'more');}}] : []),
        ...(fixed && presentation !== 'phone' ? [{id: 'pages', label: t('next.reader.pagesStrip'), icon: 'i-thumbs' as const, disabled: value.status !== 'ready' || !value.metadata?.pageCount,
          expanded: panel === 'contents' && fixedPane === 'pages', onPress: () => {setFixedPane('pages'); setPanel('contents');}},
          {id: 'zoom-out', label: t('next.reader.zoomOut'), text: <span className={fixedStyles.zoomStep}>−</span>, disabled: value.status !== 'ready' || !value.zoom || value.zoom.scale <= .05,
            onPress: () => {if (value.status === 'ready' && value.zoom && authority.isCurrent() && controller.getSnapshot() === value) void controller.setZoom({kind: 'scale', value: Math.max(.05, Math.round(value.zoom.scale / 1.25 * 100) / 100)}).catch(error => {if (authority.isCurrent()) engineError(readerErrorCode(error));});}},
          {id: 'zoom-in', label: t('next.reader.zoomIn'), text: <span className={fixedStyles.zoomStep}>+</span>, disabled: value.status !== 'ready' || !value.zoom || value.zoom.scale >= 8,
            onPress: () => {if (value.status === 'ready' && value.zoom && authority.isCurrent() && controller.getSnapshot() === value) void controller.setZoom({kind: 'scale', value: Math.min(8, Math.round(value.zoom.scale * 1.25 * 100) / 100)}).catch(error => {if (authority.isCurrent()) engineError(readerErrorCode(error));});}},
          {id: 'layout', label: t(value.preferences.twoPages ? 'next.reader.onePage' : 'next.reader.twoPages'), icon: 'i-spread' as const, pressed: value.preferences.twoPages, disabled: value.status !== 'ready' || !value.zoom,
            onPress: () => {if (value.status === 'ready' && value.zoom && authority.isCurrent() && controller.getSnapshot() === value) void controller.setOverride({twoPages: !value.preferences.twoPages}).catch(error => {if (authority.isCurrent()) engineError(readerErrorCode(error));});}}] : []),
        ...(fixed && !compactDocument ? [{id: 'zoom', label: t('next.reader.zoom'), text: <span className={fixedStyles.zoomLabel}>{fixedPageZoomLabel(value.zoom)}</span>, disabled: value.status !== 'ready' || !value.zoom,
          expanded: fixedMenu === 'zoom', onPress: (element: HTMLButtonElement) => {zoomAnchor.current = element; setFixedMenu(fixedMenu === 'zoom' ? undefined : 'zoom');}},
          ...(publication.format === 'cbz' ? [{id: 'direction', label: t('next.reader.direction'), icon: directionIcon(value.preferences.comicDirection === 'rtl' || value.preferences.comicDirection === 'file' && publication.metadata.direction === 'rtl'),
            expanded: fixedMenu === 'direction', onPress: (element: HTMLButtonElement) => {directionAnchor.current = element; setFixedMenu(fixedMenu === 'direction' ? undefined : 'direction');}}] : [])] : []),
        ...(layout.fullscreen && document.fullscreenEnabled ? [{id: 'fullscreen', label: t('next.reader.fullscreen'), icon: 'i-fs' as const, separatorBefore: true,
          onPress: (element: HTMLButtonElement) => {const root = element.closest<HTMLElement>('[data-reader-root]'); void (document.fullscreenElement ? document.exitFullscreen() : root?.requestFullscreen())?.catch(reportError);}}] : []),
      ]})}/>
    {active && <ReaderAnnotationActions service={notes.service} snapshot={snapshot} controller={controller} onSearch={publication.format !== 'cbz' ? text => {changeQuery(text);setPanel('search');} : undefined} reportError={reportError}/>}
    {active && environment === 'web' && snapshot.status === 'ready' && <ReaderBrowserHint visible={presentation === 'phone' && !panel && !fixedMenu && !currentHit && snapshot.selection === null && !hint}/>}
    {active && fixed && <FixedPageMenus controller={controller} authority={authority} format={publication.format} reportError={engineError}
      open={fixedMenu} onOpenChange={setFixedMenu} anchorRef={zoomAnchor} directionRef={directionAnchor} moreRef={moreAnchor}/>}
  </>;
}

