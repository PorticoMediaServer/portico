import {useRouteLibrary} from '../browse/route-library';
import {forwardIdentity} from '../detail/context';
import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {calendarDate, defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import type {ReadingDiscover, ReadingDiscoverRowKey, ReadingShortRead, ReadingPeriodicalIssue, ReadingPeriodicalGroup} from '@portico/client-core';
import type {FactPart} from '@portico/client-core/kit-model';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {useShellWidth} from '../../shell/viewport';
import {useReducedMotion} from '../../kit/overlays/shared';
import {Row, WindowedGrid} from '../../kit/layout';
import {CollectionBanner, CoverCard} from '../../kit/cards';
import {GeneratedCover} from '../../kit/artwork';
import {Skeleton, StateView} from '../../kit/states';
import {Menu, MenuItem} from '../../kit/overlays';
import {useLibraryScreenContext} from '../browse/screen-context';
import {ReadSelectionBar} from './ReadSelectionBar';
import {readingSelectionAuthority} from './reading-selection-authority';
import {bulkTargetLimit} from '@core/metadata-bulk.ts';
import {ReadBulkMetadata} from './ReadBulkMetadata';
import {ReadMetadata} from './ReadMetadata';
import {createReadBindings} from './bindings';
import {createReadingCollection, createReadingContinueCollection, type ReadingContinueItem, type ReadingLibraryItem} from './library-collection';
import {readChosenDestination} from './navigation';
import {recentCards, type RecentReadingItem} from './recent-cards';
import {ReadDiscovery, type ReadDiscoveryRow, type ReadResumeItem} from './ReadLibrary';
import styles from './entry.module.css';

type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type Resources = Readonly<{
  books: ReturnType<typeof createReadingCollection>;
  resume: ReturnType<typeof createReadingContinueCollection>;
  bindings: ReturnType<typeof createReadBindings>;
  reportError: (error: unknown) => void;
  artwork: Captured['context']['artwork'];
}>;
const {t} = defaultI18n;
/** The Discover rows that continue (the newspaper slot is one card). */
const pagedRows: readonly string[] = ['recent', 'short-reads', 'series', 'new-issues'];

/** Real facts only: the accepted author, never a length or a guess the DTO doesn't carry. */
const authorFacts = (author: Readonly<{value: string; source: string}> | undefined): readonly FactPart[] =>
  author ? [{id: 'author', text: author.value, role: 'identity'}] : [];

/** Generated art is the drawn fallback for every item without artwork; it never invents a cover. */
const generated = (item: Readonly<{title: string; author?: Readonly<{value: string}>}>) =>
  <GeneratedCover title={item.title} author={item.author?.value} glyph={Array.from(item.title)[0] ?? ''} tint={generatedCoverTint(item.title)} />;

/** The accepted Continue index already names the actual progressed version. */
function resumeRows(resources: Resources, items: readonly ReadingContinueItem[]): readonly ReadResumeItem[] {
  return items.map(row => ({id: row.item.id, card: {
    artwork: resources.artwork.foreground(row.item.coverUrl, 380, 2 / 3),
    /** desktop-read 1b: an issue keeps its own 3:4 cover, a book keeps the drawn spine. */
    kind: row.item.publicationType?.value === 'magazine' ? 'magazine' as const : row.item.publicationType?.value === 'newspaper' ? 'newspaper' as const : 'book' as const,
    model: {title: row.item.title, caption: authorFacts(row.item.author), progress: row.progress.location.progression},
    /** desktop-read 1b: where you are, from the saved location's own section. A stale
     *  location is not presented as current, and no remaining time is inferred. */
    ...(row.progress.stale || !row.progress.location.section ? {} : {location: {id: 'section', text: row.progress.location.section, role: 'context' as const}}),
    fallback: generated(row.item), disabled: !row.item.inventoryAvailable, visible: true, playLabel: t('next.reader.read'),
    onNavigate: () => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: row.item.id}}).catch(resources.reportError);},
    onPlay: row.item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(row.item.id)).catch(resources.reportError);} : undefined,
  }}));
}

function LibraryRows({resources, libraryId, name, comic, view, onMore, onRetry, selection, onToggle, onExitSelection}: {resources: Resources; libraryId: string; name: string; comic: boolean; view: 'discover' | 'books'; onMore?: (item: ReadingLibraryItem, anchor: HTMLButtonElement) => void; onRetry: () => void; selection?: ReadonlyMap<string, ReadingLibraryItem>; onToggle?: (item: ReadingLibraryItem) => void; onExitSelection: () => void}) {
  const selecting = selection !== undefined;
  const grid = useRef<HTMLDivElement>(null), width = useShellWidth(), reducedMotion = useReducedMotion(), [height, setHeight] = useState(600);
  const state = useSyncExternalStore(resources.books.collection.subscribe, resources.books.collection.getSnapshot, resources.books.collection.getSnapshot), failed = state.status === 'error';
  const reading = useSyncExternalStore(resources.resume.collection.subscribe, resources.resume.collection.getSnapshot, resources.resume.collection.getSnapshot);
  const [recent, setRecent] = useState<readonly RecentReadingItem[]>();
  const [newspaper, setNewspaper] = useState<ReadingPeriodicalGroup>();
  const [newIssues, setNewIssues] = useState<readonly ReadingPeriodicalIssue[]>([]);
  const [shortReads, setShortReads] = useState<readonly ReadingShortRead[]>([]);
  const [series, setSeries] = useState<NonNullable<ReadingDiscover['rows'][number]['series']>>([]);
  const [previewMarks, setPreviewMarks] = useState<ReadonlyMap<string, Readonly<{itemId: string; read: boolean; revision: number}>>>(new Map());
  /** Each Discover row's own continuation (WEB-053): the open request's life, the
   *  server's next cursor per row and the rows already asking for their next page. */
  const discovery = useRef<{life: AbortController; cursors: Map<ReadingDiscoverRowKey, string>; loading: Set<ReadingDiscoverRowKey>}>(undefined);
  useLayoutEffect(() => {
    const element = grid.current; if (!element) return;
    const read = () => setHeight(Math.max(280, window.innerHeight - element.getBoundingClientRect().top));
    const observer = new ResizeObserver(read); observer.observe(element); window.addEventListener('resize', read); read();
    return () => {observer.disconnect(); window.removeEventListener('resize', read);};
    // The grid mounts only on Books (or when Discover has no rows); re-measure and observe it then.
  }, [failed, reading.status, view, selecting]);
  useEffect(() => {resources.resume.collection.ensureRange(0, 11);}, [resources]);
  /** phone-read 1: the server's own recently-added row. An empty row draws nothing; a failed
   *  request is reported through the screen's error channel rather than pretending the library
   *  has no recently-added values (Root §3), and the Reading list stays reachable. */
  useEffect(() => {
    const operation = new AbortController();
    const cursors = new Map<ReadingDiscoverRowKey, string>();
    discovery.current = {life: operation, cursors, loading: new Set()};
    setSeries([]); setPreviewMarks(new Map()); setShortReads([]); setNewIssues([]); setNewspaper(undefined);
    resources.bindings.client.discover({libraryId, limit: 20, seriesLimit: 12, ...(!comic ? {shortReadsLimit: 12, newIssuesLimit: 12, newspaperLimit: 1} : {})}, operation.signal)
      .then(response => {if (!operation.signal.aborted && resources.bindings.authority.isCurrent()) {
        for (const row of response.rows) if (row.nextCursor && row.key !== 'newspaper') cursors.set(row.key as ReadingDiscoverRowKey, row.nextCursor);
        setRecent(response.rows[0]?.items ?? []); setSeries(response.rows.find(row => row.key === 'series')?.series ?? []); setPreviewMarks(response.previewMarks); setShortReads(response.rows.find(row => row.key === 'short-reads')?.shortReads ?? []); setNewIssues(response.newIssueCards); setNewspaper(response.newspaperCard);}})
      .catch(error => {
        if (operation.signal.aborted) return;
        setRecent([]); setSeries([]); setPreviewMarks(new Map()); setShortReads([]); setNewIssues([]); setNewspaper(undefined);
        if (resources.bindings.authority.isCurrent()) resources.reportError(error);
      });
    return () => operation.abort();
  }, [resources, libraryId, comic]);
  /** Near a row's end, ask for that row's next page alone and append it, skipping
   *  any entry already drawn. A failed page ends the row and is reported. */
  const continueRow = (key: ReadingDiscoverRowKey) => {
    const current = discovery.current, cursor = current?.cursors.get(key);
    if (!current || !cursor || current.loading.has(key) || current.life.signal.aborted) return;
    current.loading.add(key);
    const appended = <T,>(id: (entry: T) => string, next: readonly T[]) => (prior: readonly T[] | undefined) => {
      const seen = new Set((prior ?? []).map(id));
      return [...(prior ?? []), ...next.filter(entry => !seen.has(id(entry)))];
    };
    resources.bindings.client.discoverRow(key, {libraryId, cursor, limit: key === 'recent' ? 20 : 12}, current.life.signal)
      .then(page => {
        if (current.life.signal.aborted || !resources.bindings.authority.isCurrent()) return;
        if (page.row.nextCursor) current.cursors.set(key, page.row.nextCursor); else current.cursors.delete(key);
        if (key === 'recent') setRecent(appended<RecentReadingItem>(entry => entry.id, page.row.items));
        else if (key === 'short-reads') setShortReads(appended(entry => entry.item.id, page.row.shortReads ?? []));
        else if (key === 'new-issues') setNewIssues(appended(entry => entry.item.id, page.newIssueCards));
        else {
          setSeries(appended(entry => entry.id, page.row.series ?? []));
          setPreviewMarks(prior => new Map([...prior, ...page.previewMarks]));
        }
      })
      .catch(error => {
        if (current.life.signal.aborted) return;
        current.cursors.delete(key);
        if (resources.bindings.authority.isCurrent()) resources.reportError(error);
      })
      .finally(() => current.loading.delete(key));
  };
  const cardWidth = width < 600 ? 108 : 190;
  /** The viewer's own calendar day, so an archive edition dated today reads as today's paper. */
  const now = new Date(), today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  /** The same rows at phone width take the handheld scale and gutters (phone-read 1). */
  const platform = width < 600 ? 'phone-web' as const : 'desktop' as const;
  if (state.status === 'error') return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: onRetry}}/>;
  const resume = reading.status === 'error' ? [] : resources.resume.collection.residentItems();
  if (state.total === 0 && resume.length === 0) return <StateView title={t('library.emptyLibrary')} body={t('library.emptyLibraryBody')}/>;
  const rows: readonly ReadDiscoveryRow[] = [
    ...(resume.length > 0 ? [{id: 'continue', kind: 'resume' as const, heading: t('next.cards.read'), items: resumeRows(resources, resume)}] : []),
    ...(recent && recent.length > 0 ? [{
      id: 'recent', kind: 'covers' as const, heading: t('home.row.recentlyAdded'),
      items: recentCards(recent, {kind: comic ? 'comic' : 'book', read: t('next.reader.read'),
        open: entry => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: entry.id}}).catch(resources.reportError);},
        artwork: url => resources.artwork.foreground(url, 380, 2 / 3),
        play: entry => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(entry.id)).catch(resources.reportError);}}),
    }] : []),
    ...(shortReads.length ? [{id: 'short-reads', kind: 'covers' as const, heading: t('next.reader.shortReads'), subtitle: t('next.reader.shortReadsFromWordCount'),
      items: shortReads.map(entry => ({id: entry.item.id, card: {
        artwork: resources.artwork.foreground(entry.item.coverUrl, 380, 2 / 3), kind: 'book' as const, context: 'shelf' as const, density: 'shelf' as const, visible: true,
        model: {title: entry.item.title, caption: [...authorFacts(entry.item.author), {id: 'estimate', text: t('next.reader.aboutDuration', {duration: defaultI18n.duration(entry.estimatedSeconds)}), role: 'identity' as const}]},
        generated: {title: entry.item.title, glyph: Array.from(entry.item.title)[0] ?? '', tint: generatedCoverTint(entry.item.title)},
        disabled: !entry.item.inventoryAvailable, playLabel: t('next.reader.read'),
        onNavigate: () => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: entry.item.id}}).catch(resources.reportError);},
        onPlay: entry.item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(entry.item.id)).catch(resources.reportError);} : undefined,
      }})),
    }] : []),
    ...(series.length ? [{id: 'series', kind: 'series' as const, heading: t('next.reader.yourSeries'),
      /** The drawn bar needs every member's own mark: a three-item preview cannot stand for a
       *  whole series, so a partial preview draws no bar rather than unread slots it invented. */
      subtitle: series.some(summary => summary.preview.length === summary.count && summary.preview.every(item => previewMarks.has(item.id))) ? t('next.reader.oneMarkPerBook') : undefined,
      items: series.map(summary => ({id: summary.id, card: {
      model: {title: summary.name, caption: [
        /** desktop-read 1b/4c: the real number of titles, then the real read tally. */
        {id: 'count', text: comic ? t('next.reader.volumeCount', {count: summary.count}) : t('lib.count', {count: summary.count, pivot: 'books'}), role: 'context' as const},
        {id: 'read', text: t('next.reader.seriesReadCount', {read: defaultI18n.number(summary.readCount), count: defaultI18n.number(summary.count)}), role: 'identity' as const},
      ]},
      covers: summary.preview.map(item => resources.artwork.foreground(item.coverUrl, 380, 2 / 3) ?? {id: item.id, generated: {title: item.title, glyph: Array.from(item.title)[0] ?? '', tint: generatedCoverTint(item.title)}}),
      segments: summary.preview.length === summary.count && summary.preview.every(item => previewMarks.has(item.id))
        ? summary.preview.map(item => ({id: item.id, state: previewMarks.get(item.id)!.read ? 'read' as const : 'unread' as const})) : [],
      visible: true,
      onNavigate: () => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {view: 'series', entityId: summary.id}}).catch(resources.reportError);},
    }}))}] : []),
    ...(newIssues.length ? [{id: 'new-issues', kind: 'covers' as const, heading: t('next.reader.newIssues'), subtitle: t('next.reader.datesFromFileNames'),
      onSeeAll: () => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {view: 'series', pivot: 'magazines'}}).catch(resources.reportError);},
      items: newIssues.map(issue => ({id: issue.item.id, card: {
        kind: 'magazine' as const, context: 'shelf' as const, density: 'shelf' as const, visible: true,
        artwork: resources.artwork.foreground(issue.item.coverUrl, 380, 3 / 4), fallback: generated(issue.item),
        model: {title: issue.item.series?.name ?? issue.item.title, caption: [{id: 'date', text: calendarDate(defaultI18n, issue.issueDate), role: 'identity' as const}]},
        disabled: !issue.item.inventoryAvailable, playLabel: t('next.reader.read'),
        onNavigate: () => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: issue.item.id}}).catch(resources.reportError);},
        onPlay: issue.item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(issue.item.id)).catch(resources.reportError);} : undefined,
      }})),
    }] : []),
  ];
  /** Root §3: Discover draws the server's rows; Books draws the bounded Reading list. When no
   *  authorized row exists there is nothing to draw on Discover, so the list stays reachable. */
  const showGrid = view === 'books' || rows.length === 0 && !newspaper;
  return <div className={styles.library}>
    <ReadDiscovery focusScope={resources} rows={view === 'books' ? [] : rows} platform={platform} reducedMotion={reducedMotion}
      newspaper={view !== 'books' && newspaper ? <Row heading={t('library.tab.newspapers')} subtitle={t('next.reader.yourArchive')} platform={platform}>
        {/* desktop-read 1b/phone-read 1b: the approved collection banner, drawn from the archive's
            own latest issue: its front page, its date and the archive's issue count. The drawn
            edition strip and prose are not published, so neither is invented. */}
        <CollectionBanner kindLabel={newspaper.latest.issueDate === today || newspaper.latest.issueDate === today.slice(0, 7) ? t('next.reader.todaysPaper') : t('next.reader.onThisDay')}
          model={{title: newspaper.name, caption: [], detail: []}}
          description={t('next.reader.archiveIssues', {date: calendarDate(defaultI18n, newspaper.latest.issueDate), count: newspaper.issueCount})}
          covers={[resources.artwork.foreground(newspaper.latest.item.coverUrl, 1000, 3 / 4)].flatMap(cover => cover ? [cover] : [])}
          artwork={resources.artwork.foreground(newspaper.latest.item.coverUrl, 1000, 3 / 4)} fallback={generated(newspaper.latest.item)}
          actionLabel={t('next.reader.readThisEdition')} actionVariant="primary"
          onAction={newspaper.latest.item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(newspaper.latest.item.id)).catch(resources.reportError);} : undefined}
          visible disabled={!newspaper.latest.item.inventoryAvailable}
          onNavigate={() => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {view: 'series', pivot: 'newspapers', entityId: newspaper.id}}).catch(resources.reportError);}}/>
      </Row> : undefined}
      onVisibleRange={(row, range) => {
        if (row.kind === 'resume') resources.resume.collection.ensureRange(Math.max(0, range.first), Math.max(0, range.last));
        else if (pagedRows.includes(row.id) && range.last >= row.items.length - 4) continueRow(row.id as ReadingDiscoverRowKey);
      }}>
      {state.total === 0 || !showGrid ? null : <div ref={grid} className={styles.books} data-view="grid">
        <WindowedGrid collection={resources.books.collection} label={name}
          height={height} cardWidth={cardWidth} rowHeight={cardWidth * 1.5 + 58} gap={20} rowGap={30} renderPlaceholder={() => <Skeleton width={cardWidth} height={cardWidth * 1.5}/>}
          renderItem={item => <CoverCard artwork={resources.artwork.foreground(item.coverUrl, cardWidth * 2, 2 / 3)} kind={comic ? 'comic' : 'book'} model={{title: item.title, caption: authorFacts(item.author)}} context="grid" density="grid" visible
            generated={{title: item.title, glyph: Array.from(item.title)[0] ?? '', tint: generatedCoverTint(item.title)}} disabled={!item.inventoryAvailable && !onToggle}
            onNavigate={() => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: item.id}}).catch(resources.reportError);}}
            onPlay={item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(item.id)).catch(resources.reportError);} : undefined}
            selection={onToggle ? {active: selecting, checked: selection?.has(item.id) ?? false, onToggle: () => onToggle(item), onExit: onExitSelection} : undefined}
            playLabel={t('next.reader.read')} onMore={onMore ? anchor => {if (resources.bindings.authority.isCurrent()) onMore(item, anchor);} : undefined}/>} />
      </div>}
    </ReadDiscovery>
  </div>;
}

/** Primary Library registration supplies its drawn header/navigation. This
 * component supplies real bounded Read rows, not an audiobook projection.
 * `view` is the selected library tab: Discover draws the server's rows, Books
 * draws the bounded Reading list (Root §3). */
export function ReadLibraryEntry({captured: provided, libraryId, header, onMore, view = 'discover'}: {captured?: Captured; libraryId: string; header: ReactNode; onMore?: (item: ReadingLibraryItem, anchor: HTMLButtonElement) => void; view?: 'discover' | 'books'}) {
  const original = useProtectedWebScreenContext(), captured = provided ?? original;
  return captured ? <BoundReadLibrary key={libraryId} captured={captured} libraryId={libraryId} header={header} onMore={onMore} view={view}/> : null;
}
function BoundReadLibrary({captured, libraryId, header, onMore, view}: {captured: Captured; libraryId: string; header: ReactNode; onMore?: (item: ReadingLibraryItem, anchor: HTMLButtonElement) => void; view: 'discover' | 'books'}) {
  const context = useLibraryScreenContext(captured);
  const [menu, setMenu] = useState<Readonly<{item: ReadingLibraryItem; anchor: HTMLButtonElement}>>(), [editor, setEditor] = useState<Readonly<{item: ReadingLibraryItem; anchor: HTMLButtonElement}>>();
  const [selection, setSelection] = useState<ReadonlyMap<string, ReadingLibraryItem>>(), [bulk, setBulk] = useState<readonly ReadingLibraryItem[]>();
  const bulkAnchor = useRef<HTMLElement | null>(null);
  const errorSink = useRef(captured.binding.home.effects.reportError); errorSink.current = captured.binding.home.effects.reportError;
  const library = useRouteLibrary(captured.context, libraryId);
  const isReading = library?.kind === 'book' || library?.kind === 'comic';
  const [resources, setResources] = useState<Resources>();
  const [retry, setRetry] = useState(0), [retired, setRetired] = useState(false);
  useEffect(() => {
    if (!isReading) return;
    const bindings = createReadBindings(captured.context), books = createReadingCollection(bindings.client, libraryId),
      resume = createReadingContinueCollection(bindings.client, libraryId);
    setRetired(false);
    setMenu(undefined); setEditor(undefined); setSelection(undefined); setBulk(undefined);
    const off = bindings.authority.subscribe(() => {setRetired(true); setMenu(undefined); setEditor(undefined); setSelection(undefined); setBulk(undefined);});
    setResources({books, resume, bindings, artwork: captured.context.artwork, reportError: error => {if (bindings.authority.isCurrent()) errorSink.current(error);}});
    return () => {off();books.dispose();resume.dispose(); void bindings.dispose().catch(() => {});};
  }, [captured.context, libraryId, library?.kind, isReading, retry]);
  // Justin: members keep Select; only metadata editing is the owner's (phone-read 2, desktop-read 2b).
  const authority = readingSelectionAuthority(context.identity?.viewer), canSelect = authority.select, canEdit = authority.edit;
  const editorContext = useMemo(() => (forwardIdentity(context, {isCurrent: () => context.isCurrent() && !!editor && resources !== undefined && resources.bindings.authority.isCurrent()
    && editor.item.libraryId === libraryId && resources.books.collection.residentItems().includes(editor.item)})), [context, editor, resources, libraryId]);
  const bulkContext = useMemo(() => (forwardIdentity(context, {isCurrent: () => context.isCurrent() && !!bulk && resources !== undefined && resources.bindings.authority.isCurrent()
    && bulk.every(item => item.libraryId === libraryId && selection?.get(item.id) === item)})), [context, bulk, resources, libraryId, selection]);
  const toggle = (item: ReadingLibraryItem) => {
    if (!canSelect || !context.isCurrent() || !resources?.bindings.authority.isCurrent() || !resources.books.collection.residentItems().includes(item) || item.libraryId !== libraryId) return;
    setSelection(previous => {const next = new Map(previous); if (next.has(item.id)) next.delete(item.id); else if (next.size < bulkTargetLimit) next.set(item.id, item); return next;});
  };
  const openMore = onMore ?? (canEdit ? (item: ReadingLibraryItem, anchor: HTMLButtonElement) => {
    if (context.isCurrent() && resources?.bindings.authority.isCurrent() && item.libraryId === libraryId && resources.books.collection.residentItems().includes(item)) setMenu({item, anchor});
  } : undefined);
  if (!isReading || !library) return <StateView title={t('next.states.notFound')} body={t('next.states.notFoundBody')}/>;
  const again = () => setRetry(value => value + 1);
  return <>{header}{selection && canSelect && <ReadSelectionBar context={context} selection={selection} kind={library.kind === 'comic' ? 'comic' : 'ebook'}
    current={item => !retired && !!resources && resources.bindings.authority.isCurrent() && item.libraryId === libraryId && resources.books.collection.residentItems().includes(item)}
    onExit={() => setSelection(undefined)} onEdit={anchor => {bulkAnchor.current = anchor; setBulk(Object.freeze([...selection.values()]));}}/>}{retired ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: again}}/>
    : resources ? <LibraryRows resources={resources} libraryId={libraryId} name={library.name} comic={library.kind === 'comic'} view={view} onMore={openMore} onRetry={again} selection={selection} onToggle={canSelect ? toggle : undefined} onExitSelection={() => setSelection(undefined)}/> : <Skeleton height={320}/>}
    {menu && canEdit && <Menu open title={menu.item.title} anchorRef={{current: menu.anchor}} onOpenChange={open => {if (!open) setMenu(undefined);}}>
      <MenuItem onSelect={() => toggle(menu.item)}>{t('next.selection.select')}</MenuItem>
      <MenuItem onSelect={() => {if (context.isCurrent() && resources?.bindings.authority.isCurrent() && resources.books.collection.residentItems().includes(menu.item)) setEditor(menu);}}>{t('entry.editMetadata')}</MenuItem>
    </Menu>}
    {bulk && canEdit && <ReadBulkMetadata context={bulkContext} items={bulk} kind={library.kind === 'comic' ? 'comic' : 'ebook'} anchor={bulkAnchor} onClose={() => {setBulk(undefined); setSelection(undefined); again();}}/>}
    {editor && canEdit && <ReadMetadata context={editorContext} reading={captured.context} sourceId={editor.item.sourceId} format={editor.item.format} itemId={editor.item.id} libraryId={libraryId} anchor={{current: editor.anchor}} onClose={() => {setEditor(undefined); setMenu(undefined); again();}}/>}
  </>;
}
