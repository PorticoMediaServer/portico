import {serviceI18n} from '@core/presentation/service-text.ts';
import {musicFormatBadges, formatSignalText} from '@core/music-signal.ts';
import {activateCard, useSelectionRangePreview, useCardInteractionAdmission} from '../../kit/cards';
import {useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {timelineFacts, type TextEnvironment} from '@portico/client-core/kit-model';
import {durationText} from '@core/presentation/kit-model/text.ts';
import type {BrowseSortSelection} from '@core/browse.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {AlphabetRail, WindowedGrid, type WindowedGridHandle} from '../../kit/layout';
import {gridColumns} from '../../kit/layout/geometry';
import {Skeleton, StateView} from '../../kit/states';
import {Artwork, GeneratedArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {SelectionCircle} from '../../kit/selection';
import {usePhone} from '../../kit/overlays/shared';
import type {ShellArtwork} from '../../shell/artwork';
import {useRetainedLayoutPaint} from '../../kit/layout/retained-paint';
import {BrowseEntryCard, type BrowseItemActions} from './EntryCard';
import type {BrowseController, BrowseMember} from './controller';
import styles from './browse.module.css';

export type BrowseResultsProps = Readonly<{
  controller: BrowseController; label: string; view: 'grid' | 'list'; cardWidth: number; shape: 'poster' | 'square' | 'collection';
  artwork: ShellArtwork; env: TextEnvironment; reducedMotion: boolean; backdrops: boolean;
  actions: (member: BrowseMember, index: number) => BrowseItemActions;
  /** The actual selected viewer's library list supplies family for mixed rows. */
  familyForEntry?: (entry: ContentEntry) => string | undefined;
  onVisibleRange?: (first: number, last: number) => void;
  onSort: (sort: BrowseSortSelection) => void;
  empty?: ReactNode;
}>;
const t = defaultI18n.t;
// Frames #grid/#big draw the poster grid as `repeat(7, minmax(0, 1fr))`: the card fills its
// column, so the painted width is the measured column at the reported Medium size, never a
// fixed width that leaves the row short of the gutter.
const gridGap = 20, gridRowGap = 30;
// The grid measures one pitch for every row, so it must cover the tallest drawn card: 10px
// caption offset, two 14px/1.3 title lines, the 2px year offset and one 13px line at the
// font's 1.4 normal leading. Frames #grid (49px, one line) and #big (66.6px, two lines) agree.
const gridCaption = 66.6;
type BrowseColumn = Readonly<{id: string; label: MessageId; sort?: string}>;
const columns: readonly BrowseColumn[] = [
  {id: 'title', label: 'library.column.title'}, {id: 'year', label: 'library.column.year'},
  {id: 'duration', label: 'library.column.runtime'}, {id: 'communityRating', label: 'library.column.rating'},
  {id: 'resolution', label: 'library.column.resolution'}, {id: 'added', label: 'library.column.added'},
];
// desktop-read 2c draws a reading list as Title, Author, Progress, Series, Pages and Added.
// Each cell is filled from the row's own published fact and stays empty when the row has none,
// so a column is never filled with a guess.
const readingColumns: readonly BrowseColumn[] = [
  {id: 'title', label: 'library.column.title'}, {id: 'author', label: 'library.column.author'},
  {id: 'progress', label: 'library.column.progress'}, {id: 'series', label: 'library.column.series'},
  {id: 'pages', label: 'library.column.pages'}, {id: 'added', label: 'library.column.added'},
];
// desktop-listen 4 / music-plus 9: the music list's own columns, from published facts only.
const musicColumns: readonly BrowseColumn[] = [
  {id: 'number', label: 'next.browse.number'}, {id: 'title', label: 'library.column.title', sort: 'title'},
  {id: 'artist', label: 'library.column.artist', sort: 'artist'}, {id: 'album', label: 'library.column.album', sort: 'album'},
  {id: 'duration', label: 'library.column.duration', sort: 'duration'}, {id: 'plays', label: 'library.column.plays', sort: 'playCount'},
  {id: 'added', label: 'library.column.added', sort: 'added'},
];
export function BrowseResults(props: BrowseResultsProps) {
  const {controller, view, shape, cardWidth, onVisibleRange} = props;
  const root = useRef<HTMLDivElement>(null), grid = useRef<WindowedGridHandle>(null), phone = usePhone();
  const [size, setSize] = useState({width: 0, grid: 0, height: 600}), [visible, setVisible] = useState({first: -1, last: -1});
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const snapshot = useSyncExternalStore(controller.collection.subscribe, controller.collection.getSnapshot, controller.collection.getSnapshot);
  // WindowedGrid owns incoming arrival separately from its bounded original outgoing paint.
  const failed = snapshot.status === 'error';
  useLayoutEffect(() => {
    const element = root.current; if (!element) return;
    // The grid owns its scrollbar, so its content width — not the outer box — decides the fill.
    const grids = new Set<Element>();
    let observer: ResizeObserver;
    const read = () => {
      const bounds = element.getBoundingClientRect(), node = element.querySelector<HTMLElement>('[role="list"]');
      if (node && !grids.has(node)) {grids.add(node);observer.observe(node);}
      setSize({width: element.clientWidth, grid: node?.clientWidth ?? 0, height: Math.max(280, window.innerHeight - bounds.top)});
    };
    observer = new ResizeObserver(read);
    observer.observe(element); window.addEventListener('resize', read);read();
    return () => {observer.disconnect();window.removeEventListener('resize', read);};
  }, [failed]);
  const range = useCallback((first: number, last: number) => {setVisible(before => before.first === first && before.last === last ? before : {first, last});onVisibleRange?.(first, last);}, [onVisibleRange]);
  const anchors = Object.entries(snapshot.positionIndex ?? {}).map(([key, index]) => ({key, index}));
  const list = view === 'list', ratio = shape === 'poster' ? 2 / 3 : shape === 'collection' ? 16 / 9 : 1;
  const musicLibrary = state.capabilities?.library.kind === 'music';
  // The canonical Songs tab maps its pivot to 'songs' (library-tabs.ts); only that tab is the song table.
  const music = musicLibrary && controller.source.pivot === 'songs';
  const musicGenres = musicLibrary && controller.source.pivot === 'genres';
  const reading = state.capabilities?.library.kind === 'book' || state.capabilities?.library.kind === 'comic';
  const drawnColumns = reading ? readingColumns : music ? musicColumns : columns;
  // Frame #collections draws three 16:9 series tiles at 24px: a page of about ten groups reads as
  // banners, not as seven poster columns. Every other grid fills its columns to the gutter.
  const tiles = shape === 'collection' && !list, gap = tiles ? 24 : gridGap;
  const painted = tiles ? 3 : size.width > 0 ? gridColumns(size.width, cardWidth, gridGap) : 1;
  // Fill the grid's own content box so every row reaches the gutter. The hundredth of a pixel
  // keeps the canonical column count exactly `painted` when the grid re-derives it from this width.
  const overflow = size.grid > 0 ? Math.max(0, size.width - size.grid) : 0;
  const width = list ? Math.max(1, size.grid || size.width) : size.grid > 0 ? (size.grid - (painted - 1) * gap) / painted - .01 : cardWidth;
  const height = musicGenres ? 152 : width / ratio + (shape === 'collection' ? 0 : gridCaption);
  const gutter = {'--grid-overflow': `${overflow}px`} as CSSProperties;
  if (failed) return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: controller.retry}}/>;
  return <div className={styles.results} ref={root} data-view={view} data-music={music || undefined} data-reading={reading || undefined} data-shape={tiles ? 'collection' : undefined} data-empty={snapshot.total === 0 ? '' : undefined} style={gutter}>
    {tiles && <p className={styles.collectionsNote}><Icon name="i-layers" size={18}/>{t('next.browse.collectionsAutomatic')}</p>}
    {list && <div className={styles.listHead} role="row">{drawnColumns.map(column => {
      const field = column.sort ?? column.id, offered = state.capabilities?.sorts.find(sort => sort.id === field);
      return <div key={column.id} role="columnheader" className={column.id === 'pages' || column.id === 'added' || column.id === 'number' ? styles.listNumber : undefined} aria-sort={state.sort?.field === field ? state.sort.direction === 'asc' ? 'ascending' : 'descending' : 'none'}>
        {offered ? <button type="button" onClick={() => props.onSort({field: offered.id, direction: state.sort?.field === offered.id && offered.directions.length > 1 ? state.sort.direction === 'asc' ? 'desc' : 'asc' : offered.defaultDirection})}>{t(column.label)}{state.sort?.field === field && <Icon name={state.sort.direction === 'asc' ? 'i-up' : 'i-down'} size={14}/>}</button> : t(column.label)}
      </div>;
    })}{!reading && !music && <span aria-label={t('next.cards.watched')} role="columnheader"/>}<span/></div>}
    <WindowedGrid collection={controller.collection} ref={grid} label={props.label} height={size.height - (list ? 44 : 0)} retainRemovals paintScope={controller} empty={props.empty}
      cardWidth={width} rowHeight={list ? music ? 56 : 70 : height} gap={list ? 0 : gap} rowGap={list ? 0 : tiles ? 24 : gridRowGap} reducedMotion={props.reducedMotion}
      onVisibleRange={range} renderPlaceholder={() => <Skeleton height={list ? 54 : musicGenres ? 152 : width / ratio} width={list ? '100%' : width}/>}
      renderItem={(member, context) => list ? <BrowseListRow {...props} member={member} index={context.index} reading={reading} music={music}/> : <BrowseEntryCard entry={member.entry} actions={props.actions(member, context.index)} artwork={props.artwork} env={props.env}
        width={width} family={props.familyForEntry?.(member.entry) ?? state.capabilities?.library.kind} mixed={controller.source.libraryId === '*'} visible order={context.index} group={controller.source.libraryId} backdrops={props.backdrops}/>} />
    {state.sort?.field === 'title' && anchors.length > 0 && !tiles && <AlphabetRail anchors={anchors} visibleRange={visible} onJump={anchor => grid.current?.scrollToIndex(anchor.index)} platform={phone ? 'phone' : 'desktop'} className={styles.rail}/>}
  </div>;
}
function BrowseListRow({member, index, artwork, env, actions, reading, music}: BrowseResultsProps & {member: BrowseMember; index: number; reading: boolean; music: boolean}) {
  const interactionRoot = useRef<HTMLDivElement>(null), retained = useRetainedLayoutPaint();
  const source = useRef<'pointer' | 'keyboard'>('pointer');
  const committed = useRef<{member: BrowseMember; index: number; actions: BrowseResultsProps['actions']; value: BrowseItemActions} | undefined>(undefined);
  // Live descriptors must still reflect ordinary selected/preview publications.
  // Only outgoing paint keeps the last committed original action descriptor.
  const liveAction = retained ? undefined : actions(member, index);
  const previous = committed.current;
  const originalAction = retained
    ? previous?.member === member && previous.index === index && previous.actions === actions ? previous.value : undefined
    : liveAction;
  useLayoutEffect(() => {if (liveAction) committed.current = {member, index, actions, value: liveAction};}, [liveAction, member, index, actions]);
  const selection = useCardInteractionAdmission(originalAction?.selection, interactionRoot, retained);
  const {entry} = member, title = entry.displayTitle ?? entry.title, progress = timelineFacts(entry.duration, entry.progressSeconds).progress;
  const badges = entry.kind === 'song' && entry.audioFacts ? musicFormatBadges(entry.audioFacts).map(badge => typeof badge === 'string' ? badge : formatSignalText(badge, serviceI18n().t)) : [];
  const range = useSelectionRangePreview(selection);
  const art = useMemo(() => artwork.foreground(entry.posterUrl, music ? 42 * devicePixelRatio : 72 * devicePixelRatio, music ? 1 : 2 / 3, {group: entry.libraryId, order: index}), [artwork, entry.posterUrl, entry.libraryId, music, index]);
  if (!originalAction) return null;
  const action = {...originalAction, selection};
  const added = entry.addedAt && Number.isFinite(Date.parse(entry.addedAt)) ? new Intl.DateTimeFormat(env.locale ?? 'en-US', {month: 'short', day: 'numeric', year: 'numeric'}).format(new Date(entry.addedAt)) : undefined;
  const duration = durationText(entry.duration, env.t);
  const resolution = entry.resolution === '4k' ? t('quality.4k') : entry.resolution === '1080p' ? t('downloads.quality.1080p') : entry.resolution === '720p' ? t('downloads.quality.720p') : entry.resolution === 'sd' ? t('quality.sd') : undefined;
  const select = action.selection && <SelectionCircle selected={action.selection.checked} selecting={action.selection.active} onSelectedChange={(_next, intent) => {if (!retained) action.selection?.onToggle({range: intent.intent === 'range' || intent.shiftKey === true, source: source.current});}} onKeyDownCapture={() => {source.current = 'keyboard';}} onPointerDownCapture={() => {source.current = 'pointer';}} accessibilityLabel={t('next.selection.select')} variant="row"/>;
  return <div ref={interactionRoot} className={styles.listRow} data-browse-entry={entry.id} data-selected={action.selection?.checked || undefined} data-preview={action.selection?.preview || undefined} {...range} onKeyDown={event => {range.onKeyDown(event);if (!retained && event.key === 'Escape' && action.selection?.active) {event.preventDefault();action.selection.onExit();}}}>
    {music && <span className={styles.musicNumber}>{select ?? <span>{index + 1}</span>}</span>}
    <div className={styles.listTitle}>{!music && select}
      <button type="button" data-layout-primary onClick={event => {if (!retained) activateCard(action.onOpen, action.selection, {range: event.shiftKey, source: event.detail === 0 ? 'keyboard' : 'pointer'});}}><span className={styles.listArt}><Artwork source={art} visible alt="" fallback={<GeneratedArt glyph={Array.from(title)[0] ?? ''}/>}/></span><span className={styles.listName}>{title}</span>{badges.map(badge => <span key={badge} className={styles.songBadge}>{badge}</span>)}</button></div>
    {music ? <>
      <span className={styles.musicMeta}>{entry.artist?.name}</span><span className={styles.musicMeta}>{entry.album?.name}</span>
      <span className={styles.musicNumber}>{duration}</span><span className={styles.musicNumber}>{entry.plays === undefined ? undefined : defaultI18n.number(entry.plays)}</span>
      <span className={styles.musicNumber}>{added}</span>
    </> : reading ? <>
      <span className={styles.listAuthor}>{entry.author}</span>
      {/* A finished mark draws the drawn state. An explicit unread mark only says the mark was
          reset — the title may still hold reading progress — so it draws nothing. */}
      <span className={styles.listProgress}>{entry.readingMark?.read === true ? <><Icon name="i-check" size={16}/>{t('next.cards.finished')}</> : undefined}</span>
      <span className={styles.listSeries}>{entry.series?.[0] ? entry.series[0].position ? t('library.seriesPosition', {series: entry.series[0].name, number: entry.series[0].position}) : entry.series[0].name : undefined}</span>
      <span className={styles.listNumber}>{entry.pageCount === undefined ? undefined : defaultI18n.number(entry.pageCount)}</span>
      <span className={styles.listNumber}>{added}</span>
    </> : <>
      <span>{entry.year}</span><span>{durationText(entry.duration, env.t)}</span><span>{entry.rating === undefined ? '–' : defaultI18n.number(entry.rating, {minimumFractionDigits: 1, maximumFractionDigits: 1})}</span>
      <span>{resolution && <span className={styles.resolution}>{resolution}</span>}</span><span>{added}</span>
      <span className={styles.listWatched}>{entry.watched ? <Icon name="i-check" size={18}/> : progress !== undefined && <span className={styles.miniProgress}><span style={{width: `${progress * 100}%`}}/></span>}</span></>}
    <span className={styles.listActions}>{action.onPlay && !action.selection?.active && <Button variant="primary" size="small" icon="i-play" iconOnly onClick={() => {if (!retained) action.onPlay?.();}} aria-label={t('next.browse.play', {title})}/>}{action.onMore && <Button variant="icon" size="small" icon="i-more" iconOnly onClick={event => {if (!retained) action.onMore?.(event.currentTarget);}} aria-label={t('next.browse.moreFor', {title})}/>}</span>
  </div>;
}
