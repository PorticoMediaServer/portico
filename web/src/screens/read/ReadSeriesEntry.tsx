import {useRouteLibrary} from '../browse/route-library';
import {forwardIdentity} from '../detail/context';
import {ReadGroupSavedAction} from './ReadGroupSavedAction';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import {useLocation} from '@tanstack/react-router';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {useCommittedLibraryScreenContext} from '../browse/screen-context';
import type {ScreenContext} from '../detail/context';
import {router} from '../../router';
import {CoverCard} from '../../kit/cards';
import {Button} from '../../kit/controls';
import {WindowedGrid} from '../../kit/layout';
import {ReadBookHero} from '../../kit/reader/ReadBookHero';
import type {ArtworkSource} from '../../kit/artwork';
import {Skeleton, StateView} from '../../kit/states';
import {ReadSelectionBar} from './ReadSelectionBar';
import {readingSelectionAuthority} from './reading-selection-authority';
import {ServerMark} from '../../kit/shell';
import {bulkTargetLimit} from '@core/metadata-bulk.ts';
import {ReadBulkMetadata} from './ReadBulkMetadata';
import {useShellWidth} from '../../shell/viewport';
import {createReadBindings} from './bindings';
import {createReadingSeriesCollection, type ReadingContinueItem, type ReadingLibraryItem} from './library-collection';
import {readChosenDestination} from './navigation';
import styles from './entry.module.css';
import readItemStyles from './read-item.module.css';

const {t} = defaultI18n;
type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type Resources = Readonly<{id: string; context: Captured['context']; artwork: Captured['context']['artwork']; comic: boolean; bindings: ReturnType<typeof createReadBindings>;
  libraryId: string; seriesId: string; rows: ReturnType<typeof createReadingSeriesCollection>; reportError: (error: unknown) => void}>;

/** The canonical series cursor owns complete membership and ordering, including unnumbered volumes. */
export function ReadSeriesEntry({captured: provided, libraryId, seriesId}: {captured?: Captured; libraryId: string; seriesId: string}) {
  const original = useProtectedWebScreenContext(), captured = provided ?? original;
  return captured ? <BoundSeries key={JSON.stringify([libraryId, seriesId])} captured={captured} libraryId={libraryId} seriesId={seriesId}/> : null;
}
function BoundSeries({captured, libraryId, seriesId}: {captured: Captured; libraryId: string; seriesId: string}) {
  const kind = useRouteLibrary(captured.context, libraryId)?.kind;
  const [resources, setResources] = useState<Resources>(), [retry, setRetry] = useState(0), [retired, setRetired] = useState(false);
  // The opening reports through this commit's shell binding; a later binding identity
  // must not replay the committed cursor.
  const shellEffects = useRef(captured.binding.home.effects);
  useLayoutEffect(() => {shellEffects.current = captured.binding.home.effects;});
  // A committed opening owns the bindings and the cursor: a strict replay publishes the new pair
  // in the same commit that retires the old one, so a disposed owner is never left on screen.
  useLayoutEffect(() => {
    if (kind !== 'book' && kind !== 'comic') {setResources(undefined);return;}
    const effects = shellEffects.current;
    const bindings = createReadBindings(captured.context), rows = createReadingSeriesCollection(bindings.client, libraryId, seriesId);
    setRetired(false);
    const off = bindings.authority.subscribe(() => {setRetired(true); setResources(undefined);});
    setResources({id: crypto.randomUUID(), context: captured.context, artwork: captured.context.artwork, bindings, comic: kind === 'comic', libraryId, seriesId, rows,
      reportError: error => {if (bindings.authority.isCurrent()) effects.reportError(error);}});
    return () => {off(); rows.dispose(); void bindings.dispose().catch(() => {});};
  }, [captured.context, libraryId, seriesId, kind, retry]);
  if (retired) return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => setRetry(value => value + 1)}}/>;
  return resources && resources.context === captured.context ? <SeriesRows resources={resources} captured={captured} retry={() => setRetry(value => value + 1)}/> : <Skeleton quiet height={320}/>;
}
/** The holder acquires the committed context; the live body below always receives one. */
function SeriesRows({resources, captured, retry}: {resources: Resources; captured: Captured; retry: () => void}) {
  const href = useLocation({select: location => location.href});
  const context = useCommittedLibraryScreenContext(captured, href, () => router.state.location.href === href);
  return context ? <LiveSeriesRows key={resources.id} resources={resources} context={context} retry={retry}/> : <Skeleton quiet height={320}/>;
}
/** A fresh resource id keys this body, so a new cursor or a strict replay never inherits the
 *  previous selection or bulk draft. */
function LiveSeriesRows({resources, context, retry}: {resources: Resources; context: ScreenContext; retry: () => void}) {
  const state = useSyncExternalStore(resources.rows.collection.subscribe, resources.rows.collection.getSnapshot, resources.rows.collection.getSnapshot);
  const overview = useSyncExternalStore(resources.rows.overview.subscribe, resources.rows.overview.getSnapshot, resources.rows.overview.getSnapshot);
  const width = useShellWidth(), grid = useRef<HTMLDivElement>(null), [height, setHeight] = useState(600);
  /** phone-read 4: Select is every signed-in viewer's; it edits this series' own volumes in this order. */
  const authority = readingSelectionAuthority(context.identity?.viewer), canSelect = authority.select, canEdit = authority.edit;
  const [selection, setSelection] = useState<ReadonlyMap<string, ReadingLibraryItem>>(), [bulk, setBulk] = useState<readonly ReadingLibraryItem[]>();
  const bulkAnchor = useRef<HTMLElement | null>(null);
  const toggle = (item: ReadingLibraryItem) => {
    if (!canSelect || !context.isCurrent() || !resources.bindings.authority.isCurrent() || !resources.rows.collection.residentItems().includes(item)) return;
    setSelection(previous => {const next = new Map(previous); if (next.has(item.id)) next.delete(item.id); else if (next.size < bulkTargetLimit) next.set(item.id, item); return next;});
  };
  // The actual progressed member of this series comes from the canonical continue
  // index; its version and non-stale flag are the server's, never a guess. A
  // failed optional read leaves the first member as the real action.
  const [continued, setContinued] = useState<Readonly<{item: ReadingLibraryItem; progression: number}>>();
  useEffect(() => {
    const life = new AbortController();
    void resources.bindings.client.continue({libraryId: resources.libraryId, limit: 60}, life.signal).then(page => {
      if (life.signal.aborted || !resources.bindings.authority.isCurrent()) return;
      const row = page.items.find((row: ReadingContinueItem) => row.item.series?.id === resources.seriesId && row.item.inventoryAvailable && !row.progress.stale && row.progress.location.progression > 0);
      setContinued(row ? {item: row.item, progression: row.progress.location.progression} : undefined);
    }).catch(() => { /* An optional continuation read never replaces the series' own rows. */ });
    return () => life.abort();
  }, [resources]);
  useEffect(() => {
    const element = grid.current; if (!element) return;
    const measure = () => setHeight(Math.max(280, window.innerHeight - element.getBoundingClientRect().top));
    const observer = new ResizeObserver(measure); observer.observe(element); window.addEventListener('resize', measure); measure();
    return () => {observer.disconnect(); window.removeEventListener('resize', measure);};
  }, [state.status]);
  const resident = resources.rows.collection.residentItems();
  const [coverItems, setCoverItems] = useState<readonly ReadingLibraryItem[]>([]);
  useEffect(() => {
    if (!resources.comic || coverItems.length >= 3) return;
    const next = [...coverItems, ...resident.filter(item => item.coverUrl && !coverItems.some(cover => cover.id === item.id))].slice(0, 3);
    if (next.length > coverItems.length) setCoverItems(next);
  }, [resources.comic, resident, coverItems]);
  const seriesCovers = resources.comic ? (coverItems.length ? coverItems : resident.filter(item => item.coverUrl).slice(0, 3)).map(item => resources.artwork.foreground(item.coverUrl, 460, 2 / 3))
    .filter((source): source is ArtworkSource => source !== undefined) : undefined;
  const title = overview?.title ?? resident.find(item => item.series)?.series?.name;
  const report = (error: unknown) => {resources.reportError(error);};
  const cardWidth = width < 600 ? 108 : 190;
  if (state.status === 'error') return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: retry}}/>;
  const first = resident.find(item => item.inventoryAvailable);
  const target = continued?.item ?? first, number = target?.series?.number;
  const actionLabel = continued ? number === undefined ? t('next.reader.readNext') : t('next.reader.readVolume', {number: defaultI18n.number(number)}) : t('next.reader.readNext');
  const start = () => {if (!target || !resources.bindings.authority.isCurrent()) return; void router.navigate(readChosenDestination(target.id)).catch(report);};
  const author = overview?.author?.value;
  const total = overview?.total ?? (state.total !== undefined && !state.totalIsLowerBound ? state.total : undefined);
  const tint = generatedCoverTint(title ?? '');
  const coverItem = target ?? resident[0];
  return <section className={styles.library}>
    {title ? <><ReadBookHero title={title} author={author ? {name: author} : undefined}
      facts={[...(total === undefined ? [] : [{id: 'count', text: t(resources.comic ? 'next.reader.volumeCount' : 'next.kit.books', {count: total}), role: 'value' as const}]), ...(overview ? [{id: 'read', text: t('next.reader.seriesReadCount', {read: defaultI18n.number(overview.completed), count: defaultI18n.number(overview.total)}), role: 'value' as const}] : [])]}
      cover={resources.artwork.foreground(coverItem?.coverUrl, 784, 2 / 3)}
      seriesCovers={seriesCovers}
      ambient={<span className={readItemStyles.ambient} style={{'--read-tint-first': tint.first, '--read-tint-second': tint.second} as CSSProperties} aria-hidden="true"/>}
      actions={<><Button variant="primary" icon="i-book" onClick={start} disabled={!target}>{actionLabel}</Button>{state.status === 'ready' && <ReadGroupSavedAction client={resources.bindings.client} libraryId={resources.libraryId} groupKind="series" seriesId={resources.seriesId} isCurrent={() => resources.bindings.authority.isCurrent() && resources.rows.collection.getSnapshot().status === 'ready'} reportError={report}/>}</>}/>
      <div className={styles.seriesHeading}><h2>{t(resources.comic ? 'next.reader.volumes' : 'next.kit.booksInSeries')}</h2>
        {total === undefined ? null : <span>{t(resources.comic ? 'next.reader.volumeCount' : 'next.kit.books', {count: total})}</span>}
        {canSelect && <button type="button" className={styles.seriesSelect} aria-pressed={selection !== undefined}
          onClick={() => setSelection(selection === undefined ? new Map() : undefined)}>{t('next.selection.select')}{canEdit && <ServerMark size={22}/>}</button>}</div></> : <h2>{t('library.tab.series')}</h2>}
    {selection && canSelect && <ReadSelectionBar context={context} selection={selection} kind={resources.comic ? 'comic' : 'ebook'}
      current={item => resources.bindings.authority.isCurrent() && resources.rows.collection.residentItems().includes(item)}
      onExit={() => setSelection(undefined)} onEdit={anchor => {bulkAnchor.current = anchor; setBulk(Object.freeze([...selection.values()]));}}/>}
    <div ref={grid} className={styles.books}><WindowedGrid collection={resources.rows.collection} label={title ?? t('library.tab.series')}
      height={height} cardWidth={cardWidth} rowHeight={cardWidth * 1.5 + 58} gap={20} rowGap={30}
      renderPlaceholder={() => <Skeleton width={cardWidth} height={cardWidth * 1.5}/>}
      renderItem={item => <CoverCard artwork={resources.artwork.foreground(item.coverUrl, cardWidth * 2, 2 / 3)} kind={resources.comic ? 'comic' : 'book'} context="grid" density="grid" visible
        model={{title: item.title, progress: continued?.item.id === item.id ? continued.progression : undefined,
          caption: [...(item.series?.number !== undefined ? [{id: 'number', text: t(resources.comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(item.series.number, {maximumFractionDigits: 20})}), role: 'identity' as const}] : []), ...(item.author ? [{id: 'author', text: item.author.value, role: 'identity' as const}] : [])]}}
        generated={{title: item.title, glyph: Array.from(item.title)[0] ?? '', tint: generatedCoverTint(item.title)}} disabled={!item.inventoryAvailable}
        onNavigate={() => {if (resources.bindings.authority.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: item.id}}).catch(report);}}
        onPlay={item.inventoryAvailable ? () => {if (resources.bindings.authority.isCurrent()) void router.navigate(readChosenDestination(item.id)).catch(report);} : undefined}
        playLabel={t('next.reader.read')}
        selection={canSelect ? {active: selection !== undefined, checked: selection?.has(item.id) ?? false, onToggle: () => toggle(item), onExit: () => setSelection(undefined)} : undefined}/>} /></div>
    {bulk && canEdit && <ReadBulkMetadata context={forwardIdentity(context, {isCurrent: () => context.isCurrent() && !!bulk && resources.bindings.authority.isCurrent() && bulk.every(item => resources.rows.collection.residentItems().includes(item))})}
      items={bulk} kind={resources.comic ? 'comic' : 'ebook'} anchor={bulkAnchor} onClose={() => {setBulk(undefined); setSelection(undefined);}}/>}
  </section>;
}
