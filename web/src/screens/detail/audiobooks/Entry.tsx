import {audiobookChapterCount} from './chapter-count';
import {cloneElement, Fragment, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type MouseEvent, type ReactElement} from 'react';
import {serviceI18n} from '@core/presentation/service-text.ts';
import {formatClock} from '@core/presentation/content.ts';
import {ApiError} from '@core/index.ts';
import type {ContentEntry, ContentProjection} from '@core/library-content.ts';
import type {RelationPage} from '@core/relation-rows.ts';
import {Artwork, GeneratedCover, PersonCircle, WholeArtwork, nameInitials, type ArtworkSource} from '../../../kit/artwork';
import {AboutTable, PanelBlock, PanelHeading, TitleFrame, type AboutValue, type TitleAction, type TitlePrimary} from '../../../kit/frames';
import {generatedCoverTint} from '@design';
import {CoverHeroLink} from '../../../kit/title/CoverHero';
import {Button, Chip} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {createAudiobookBindings, type AudiobookBindings, type AudiobookTarget} from './bindings';
import {runScreenCommand, type ScreenContext} from '../context';
import {DetailLoading} from '../Loading';
import {ScreenProblem} from '../ScreenProblem';
import {t} from '../text';
import styles from './audiobooks.module.css';
import {AudiobookMore} from './More';
import {TitleRow} from '../Detail';
import {TitleFactsLine} from '../../../kit/title/TitleParts';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {useRelationRow} from '../relation-row';
import {useShellWidth} from '../../../shell/viewport';

type Props = {context: ScreenContext; target: AudiobookTarget; onMore?: (target: ContentEntry, anchor: HTMLElement) => void};
type Lease = Readonly<{token: number; context: ScreenContext; key: string; binding: AudiobookBindings}>;
/** One captured page operation: the exact binding and page it was claimed for. */
type Operation = Readonly<{binding: AudiobookBindings; page: ContentProjection; kind: 'watchlist' | 'finished'}>;

/** One committed binding lease per original context/library/view/entity, retired synchronously by
 * the layout effect that made it. Only a lease that still matches the live token and the inputs it
 * was made for is published, so a stale body can never render under new ones. */

/** The audiobook page's facts on the shared facts line: a series' or author's book count, the
 * length, a book's chapter count. */
function heroFacts(count: number | undefined, length: string | undefined, chapters: number | undefined) {
  return [...(count !== undefined ? [{id: 'count', text: t('next.kit.books', {count}), role: 'value' as const}] : []),
    ...(length ? [{id: 'duration', text: length, role: 'context' as const}] : []),
    ...(chapters !== undefined ? [{id: 'chapters', text: t('title.chapterCount', {count: chapters}), role: 'context' as const}] : [])];
}

export function AudiobookEntry({context, target, onMore}: Props) {
  const {libraryId, view, entityId} = target, key = JSON.stringify([libraryId, view, entityId]);
  const [lease, setLease] = useState<Lease>();
  const live = useRef<Lease | undefined>(undefined), serial = useRef(0);
  useLayoutEffect(() => {
    // The book page is a document in the viewer's cache: Back draws it at once, and the hub reads it again in place only if it moved.
    const binding = createAudiobookBindings(context, {libraryId, view, entityId});
    const next: Lease = Object.freeze({token: ++serial.current, context, key, binding});
    live.current = next;
    setLease(next);
    return () => { binding.dispose(); if (live.current === next) live.current = undefined; setLease(current => current === next ? undefined : current); };
  }, [context, libraryId, view, entityId, key]);
  const current = lease && live.current?.token === lease.token && lease.context === context && lease.key === key ? lease : undefined;
  return current ? <AudiobookBound key={current.token} binding={current.binding} onMore={onMore}/> : <DetailLoading/>;
}

function AudiobookBound({binding, onMore}: {binding: AudiobookBindings; onMore?: Props['onMore']}) {
  const [failure, setFailure] = useState<unknown>();
  useEffect(() => {let active = true;setFailure(undefined);void binding.load().catch(error => {if (active) setFailure(error);});return () => {active = false;};}, [binding]);
  return failure ? <ScreenProblem context={binding.context} error={failure} retry={() => {setFailure(undefined);void binding.load().catch(setFailure);}}/>
    : <AudiobookBody binding={binding} onMore={onMore}/>;
}

const CHAPTER_PREVIEW = 11;
/** A length in the title page's words ("9 h 54 min", "1 min"), never the compact "1m". */
const duration = (value: number | undefined) => value === undefined ? undefined : durationText(value, t);
/** The book's own language code, named in the reader's language; an unknown code stays itself. */
function languageName(code: string): string {
  try { return new Intl.DisplayNames(serviceI18n().locale, {type: 'language'}).of(code) ?? code; } catch { return code; }
}
/** The drawn "Read by …" line, with the current locale's own list punctuation; each name is a real
 * destination when this library's checked Books capabilities offer the narrator filter. */
/** `bare`: the names alone, under a Narrator label; otherwise "Read by …". */
function Narrators({names, linked, onOpen, locale, bare = false}: {names: readonly string[]; linked: boolean; onOpen(name: string): void; locale: string | undefined; bare?: boolean}) {
  const [before, after] = bare ? ['', ''] : t('next.kit.readBy', {name: '\uFFF0'}).split('\uFFF0');
  const parts = new Intl.ListFormat(locale, {type: 'conjunction', style: 'long'}).formatToParts(names.map((_, index) => `\u0000${index}`));
  let next = 0;
  return <>{before}{parts.map((part, position) => part.type === 'literal'
    ? <Fragment key={`literal:${position}`}>{part.value}</Fragment>
    : (() => { const name = names[next++]; return <Fragment key={`name:${position}`}>{linked && name !== undefined
      ? <button type="button" className={styles.narratorLink} onClick={() => { if (name !== undefined) onOpen(name); }}>{name}</button> : name}</Fragment>; })())}{after}</>;
}
function AudiobookBody({binding, onMore}: {binding: AudiobookBindings; onMore?: Props['onMore']}) {
  const phone = useShellWidth() < 1024;
  const state = useSyncExternalStore(binding.service.subscribe, binding.service.getSnapshot, binding.service.getSnapshot);
  const [expanded, setExpanded] = useState(false), [chaptersExpanded, setChaptersExpanded] = useState(false), [finished, setFinished] = useState<boolean>(), [watchlisted, setWatchlisted] = useState<boolean>(), [filters, setFilters] = useState<Readonly<{narrator: boolean; series: boolean}>>({narrator: false, series: false}), [moreBooks, setMoreBooks] = useState<Readonly<{token: object; page: ContentProjection; value: RelationPage}>>();
  // One captured page operation at a time, claimed synchronously before any I/O.
  const operation = useRef<Operation | undefined>(undefined), [busy, setBusy] = useState<Operation['kind'] | undefined>(undefined);
  const owns = (op: Operation) => {
    if (operation.current !== op) return false;
    try { op.binding.current(op.page); } catch { return false; }
    return op.binding.context.isCurrent();
  };
  const claim = (pageAtRender: ContentProjection, kind: Operation['kind']): Operation | undefined => {
    if (operation.current !== undefined) return undefined;
    try { binding.current(pageAtRender); } catch { return undefined; }
    if (!binding.context.isCurrent()) return undefined;
    const op: Operation = Object.freeze({binding, page: pageAtRender, kind});
    operation.current = op; setBusy(kind);
    return op;
  };
  /** Only this exact operation's own completion may clear its busy state, and only while its page
   * and binding are still the committed ones. A successor's operation is never touched. */
  const settle = (op: Operation) => {
    if (operation.current !== op) return;
    operation.current = undefined;
    try { op.binding.current(op.page); } catch { return; }
    if (!op.binding.context.isCurrent()) return;
    setBusy(undefined);
  };
  const refuse = (op: Operation) => (error: unknown) => { if (owns(op)) binding.context.effects.reportError(error); };
  const page = state.projection, entity = page?.entity;
  // The page's own read lifetime and operation are retired in layout, never after paint.
  const reads = useRef<{token: object} | undefined>(undefined);
  useLayoutEffect(() => {
    const token = {}, pageAtRender = page, live = () => {
      if (!pageAtRender || reads.current?.token !== token) return false;
      try { binding.current(pageAtRender); } catch { return false; }
      return binding.context.isCurrent();
    };
    reads.current = {token};
    operation.current = undefined; setBusy(undefined);
    setExpanded(false); setChaptersExpanded(false); setFinished(undefined); setWatchlisted(undefined); setFilters({narrator: false, series: false}); setMoreBooks(undefined);
    if (pageAtRender?.scope.view === 'book') {
      void binding.finished(pageAtRender).then(value => {if (live()) setFinished(value.watched);}, error => {if (live()) binding.context.effects.reportError(error);});
      // A server that does not offer the container flag withholds the button; a real failure reports.
      void binding.watchlisted(pageAtRender).then(value => {if (live()) setWatchlisted(value);}, error => {
        if (!live()) return;
        if (error instanceof ApiError && [403, 404, 405, 501].includes(error.status)) return;
        binding.context.effects.reportError(error);
      });
      // The drawn More audiobooks row is the server's own related books, never a guessed list.
      void binding.moreBooks(pageAtRender).then(value => {if (live() && reads.current?.token === token) setMoreBooks({token, page: pageAtRender, value});}, error => {
        if (!live()) return;
        if (error instanceof ApiError && [403, 404, 405, 501].includes(error.status)) return;
        binding.context.effects.reportError(error);
      });
      // Narrator and series links appear only when this library's Books pivot publishes each filter.
      void binding.filterSupport(pageAtRender).then(value => {if (live()) setFilters(value);}, error => {
        if (!live()) return;
        if (error instanceof ApiError && [403, 404, 405, 501].includes(error.status)) return;
        binding.context.effects.reportError(error);
      });
    }
    return () => { if (reads.current?.token === token) reads.current = undefined; };
  }, [binding, page]);
  if (!page || !entity) return state.phase === 'error' || state.phase === 'refresh-required'
    ? <ScreenProblem context={binding.context} error={state.error} retry={() => {void binding.load().catch(binding.context.effects.reportError);}}/> : <DetailLoading/>;
  try {binding.current(page);} catch (error) {return state.phase === 'loading' ? <DetailLoading/> : <ScreenProblem context={binding.context} error={state.error ?? error} retry={() => {void binding.load().catch(binding.context.effects.reportError);}}/>;}
  const run = (command: () => unknown) => runScreenCommand(binding.context, command);
  const book = page.scope.view === 'book', author = page.scope.view === 'author';
  const chapters = state.appended.find(section => section.id === 'chapters'), files = state.appended.find(section => section.id === 'files');
  const rows = book ? chapters?.entries.length ? chapters : files : state.appended.find(section => section.id === 'books');
  const chapterCount = audiobookChapterCount(chapters, entity.count);
  const context = state.appended.find(section => section.id === 'context')?.entries ?? [];
  const series = context.find(entry => entry.kind === 'book_series'), authorEntry = context.find(entry => entry.kind === 'author');
  // The filter can only use a series name this book publishes exactly; otherwise its series page stands.
  const seriesTarget = series && entity.series?.find(value => value.name === series.title);
  const progress = entity.bookProgress, resume = page.listening?.resume;
  const ratio = progress?.positionMs !== undefined && progress.durationMs ? progress.positionMs / progress.durationMs : undefined;
  const remaining = duration(progress?.remainingMs === undefined ? undefined : progress.remainingMs / 1000);
  const primaryBook = !book ? rows?.entries.find(entry => entry.bookProgress && entry.available !== false && !entry.missing) : undefined;
  const canPlay = book ? page.listening?.actions.includes('play') && !page.listening.resumeUnavailable && (!!resume || !!files?.entries.some(entry => entry.playback)) : !!primaryBook;
  // FX-P2 (J-23's rule): a book whose files can't be reached draws a disabled Play with the reason,
  // never no Play at all; Start over is disabled with it.
  const unplayable = !!book && !canPlay && (entity.available === false || !!entity.missing || !!page.listening?.resumeUnavailable
    || !!files && !files.nextCursor && files.entries.length > 0 && !files.entries.some(entry => entry.playback && entry.available !== false && !entry.missing));
  const label = book ? resume ? progress?.chapter ? t('title.resumeChapter', {chapter: t('next.kit.chapter', {number: progress.chapter.number})}) : t('entry.resume') : t('entry.play') : t('next.browse.resume', {title: primaryBook?.title ?? ''});
  const cover = entity.posterUrl ?? rows?.entries[0]?.posterUrl;
  const open = (entry: ContentEntry) => run(() => binding.open(page, entry));
  // The book, author or series on the title frame (desktop-listen book; phone-app 14): the cover (a circle for an author), the
  // series as the kicker, the author and narrators leading the facts, Resume chapter with the played line and Start over,
  // Finished, My List and More; the chapters or books are the panel, About this book the side.
  const title = entity.displayTitle ?? entity.title;
  const coverNode = author ? <PersonCircle width="100%" height="100%" initialsSize={phone ? 44 : 80} alt={entity.title}
      covers={cover ? [binding.context.artwork.foreground(cover, 464, 1)].filter(source => !!source) as ArtworkSource[] : []} initials={nameInitials(entity.title)}/>
    : cover ? <Artwork source={binding.context.artwork.foreground(cover, 464, 1)} alt="" />
    : <GeneratedCover title={entity.title} author={entity.author} glyph={Array.from(entity.title.trim())[0] ?? ''} tint={generatedCoverTint(entity.title)}/>;
  const openSeries = () => run(() => series && (filters.series && seriesTarget ? binding.openSeries(page, series, seriesTarget.name) : open(series)));
  const kicker = book ? series ? {label: series.title, ...(seriesTarget?.position ? {detail: t('title.bookInSeries', {number: seriesTarget.position})} : {}), onPress: openSeries} : undefined
    : {label: t(author ? 'filter.field.author' : 'library.tab.series')};
  const byline = entity.author || book && entity.narrators?.length ? <>{entity.author && <CoverHeroLink onOpen={authorEntry ? () => open(authorEntry) : undefined}>{entity.author}</CoverHeroLink>}
    {book && !!entity.narrators?.length && <span><Narrators names={entity.narrators} linked={filters.narrator} locale={serviceI18n().locale}
      onOpen={name => run(() => binding.openNarrator(page, name))}/></span>}</> : undefined;
  const primary: TitlePrimary | undefined = canPlay ? {icon: 'i-play', label, onPress: () => run(() => binding.primary(page)),
      ...(book && ratio !== undefined ? {progress: Math.min(1, Math.max(0, ratio)), ...(remaining ? {remaining: t('title.resumeRemaining', {remaining})} : {})} : {}),
      ...(book && page.listening?.actions.includes('play') ? {startOver: {label: t('action.startOver'), onPress: () => run(() => binding.primary(page, true))}} : {})}
    : unplayable ? {icon: 'i-play', label: t('entry.play'), disabled: true, onPress: () => {}, note: t('title.fileUnavailable')} : undefined;
  const actions: TitleAction[] = [
    ...(book && watchlisted !== undefined ? [{icon: watchlisted ? 'i-check' as const : 'i-plus' as const, label: t('next.controls.myList'), labelled: true, pinned: true, active: watchlisted, disabled: busy !== undefined,
      onPress: () => { const op = claim(page, 'watchlist'); if (!op) return; void binding.setWatchlisted(page, !watchlisted).then(value => {if (owns(op)) setWatchlisted(value);}, refuse(op)).finally(() => settle(op)); }}] : []),
    ...(book && finished !== undefined ? [{icon: 'i-check' as const, label: t(finished ? 'title.markNotFinished' : 'title.markFinished'), active: finished, disabled: busy !== undefined,
      onPress: () => { const op = claim(page, 'finished'); if (!op) return; void binding.markFinished(page).then(value => {if (owns(op)) setFinished(value.watched);}, refuse(op)).finally(() => settle(op)); }}] : []),
    ...(!book && !author && primaryBook ? [{icon: 'i-retry' as const, label: t('action.startOver'), onPress: () => open(primaryBook)}] : []),
    onMore ? {icon: 'i-more', label: t('card.more', {title: entity.title}), onPress: () => {}, wrap: (button: ReactElement) => cloneElement(button as ReactElement<{onClick?: (event: MouseEvent<HTMLButtonElement>) => void}>, {onClick: (event: MouseEvent<HTMLButtonElement>) => { const anchor = event.currentTarget; run(() => {binding.current(page); onMore(entity, anchor);}); }})}
      : {icon: 'i-more', label: t('card.more', {title: entity.title}), onPress: () => {}, wrap: (button: ReactElement) => <AudiobookMore binding={binding} page={page} entry={entity} trigger={button}/>},
  ];
  const about: Readonly<{label: string; values: readonly AboutValue[]}>[] = book ? [
    ...(entity.author ? [{label: t('filter.field.author'), values: [{text: entity.author, ...(authorEntry ? {onPress: () => open(authorEntry)} : {})}]}] : []),
    ...(entity.narrators?.length ? [{label: t('filter.field.narrator'), values: entity.narrators.map(name => ({text: name, ...(filters.narrator ? {onPress: () => run(() => binding.openNarrator(page, name))} : {})}))}] : []),
    ...(series ? [{label: t('library.tab.series'), values: [{text: seriesTarget?.position ? `${series.title} ${t('title.bookInSeries', {number: seriesTarget.position})}` : series.title, onPress: openSeries}]}] : []),
    ...(entity.publishedYear !== undefined ? [{label: t('title.published'), values: [{text: serviceI18n().number(entity.publishedYear, {useGrouping: false})}]}] : []),
    ...(entity.publisher ? [{label: t('title.publisher'), values: [{text: entity.publisher}]}] : []),
    ...(entity.language ? [{label: t('next.selection.language'), values: [{text: languageName(entity.language)}]}] : []),
    ...(entity.addedAt ? [{label: t('title.added'), values: [{text: serviceI18n().date(entity.addedAt, 'medium')}]}] : []),
  ] : [
    ...(entity.narrators?.length ? [{label: t('filter.field.narrator'), values: [{text: serviceI18n().list(entity.narrators)}]}] : []),
    ...(entity.duration !== undefined ? [{label: t('next.kit.length'), values: [{text: duration(entity.duration) ?? ''}]}] : []),
  ];
  return <><TitleFrame back={binding.context.effects.back ? {label: t('action.back'), onPress: () => run(() => {binding.current(page);binding.context.effects.back?.();})} : undefined}
    cover={{shape: author ? 'circle' : 'square', node: coverNode}} kicker={kicker} title={title}
    metadata={<TitleFactsLine lead={byline} parts={heroFacts(!book ? entity.count : undefined, duration(entity.duration), book ? chapterCount : undefined)} />}
    primary={primary} actions={actions}
    synopsis={book ? entity.overview : undefined}
    readMore={book && entity.overview ? {label: t(expanded ? 'web.person.showLess' : 'web.person.readMore'), expanded, onPress: () => setExpanded(value => !value)} : undefined}
    aside={about.length ? <div><PanelHeading title={t(book ? 'title.aboutThisBook' : 'next.controls.details')} /><AboutTable rows={about} /></div> : undefined}>
    {state.error && <ScreenProblem context={binding.context} error={state.error} retry={() => {void binding.load().catch(binding.context.effects.reportError);}}/>}
    <PanelBlock label={t(book ? 'next.browse.chapters' : 'library.tab.books')}><div data-audiobook-panel data-audiobook-view={page.scope.view} data-layout={phone ? 'phone' : 'desktop'}>
    {rows && <section className={styles.rows}><div className={styles.sectionHead}><h2>{t(book ? 'next.browse.chapters' : 'library.tab.books')}</h2>{(phone || !book) && <span>{rows.totalCount}</span>}</div>
      {(book && !chaptersExpanded ? rows.entries.slice(0, CHAPTER_PREVIEW) : rows.entries).map((entry, index) => book ? <Chapter key={entry.id} entry={entry} index={index} page={page} binding={binding}/>
        : <div className={styles.bookRow} key={entry.id}><button onClick={() => open(entry)}><span className={styles.thumb}><WholeArtwork source={binding.context.artwork.foreground(entry.posterUrl, 132, 1)} alt=""
          fallback={<GeneratedCover title={entry.title} author={entry.author} glyph={Array.from(entry.title.trim())[0] ?? ''} tint={generatedCoverTint(entry.title)}/>}/></span><span><b>{entry.displayTitle ?? entry.title}</b>{entry.narrators?.length ? <small>{t('next.kit.readBy', {name: serviceI18n().list(entry.narrators)})}</small> : null}{entry.bookProgress?.remainingMs !== undefined && <small>{t('title.resumeRemaining', {remaining: duration(entry.bookProgress.remainingMs / 1000)!})}</small>}</span></button>{onMore && <Button variant="ghost" icon="i-more" iconOnly aria-label={t('card.more', {title: entry.title})} onClick={event => run(() => {binding.current(page);onMore(entry, event.currentTarget);})}/>}</div>)}
      {book && !chaptersExpanded && (rows.entries.length > CHAPTER_PREVIEW || !!rows.nextCursor) && <Chip variant="toolbar" trailingIcon="i-down" trailingSize={16} className={styles.showAll}
        onClick={() => run(() => {
          // The same admitted page and its exact section must still be the drawn ones.
          binding.current(page);
          const snapshot = binding.service.getSnapshot(), latest = snapshot.appended.find(value => value.id === rows.id) ?? snapshot.sections.find(value => value.id === rows.id);
          if (latest !== rows) throw new DOMException('Chapters changed', 'AbortError');
          setChaptersExpanded(true);
        })}>{rows.entries.length > CHAPTER_PREVIEW && rows.entries.length >= rows.totalCount ? t('title.showAllChapters', {count: rows.totalCount}) : t('title.showMoreChapters')}</Chip>}
      {rows.nextCursor && (book ? chaptersExpanded : true) && <Button size="small" onClick={() => run(() => {binding.current(page);return binding.service.next(rows.id);})}>{t('title.nextPage')}</Button>}
    </section>}
    </div></PanelBlock>
  </TitleFrame>
    {book && moreBooks && moreBooks.page === page && moreBooks.token === reads.current?.token && <MoreAudiobooksRow binding={binding} page={page} first={moreBooks.value} guard={() => {
      // A retained card belongs to this exact page and read receipt; it may not open after either was replaced.
      if (reads.current?.token !== moreBooks.token) throw new DOMException('Book page changed', 'AbortError');
      binding.current(moreBooks.page);
    }} />}
  </>;
}
/** The ordinary TitleRow pager supplies visibility demand and the existing square-card shelf.
 * An empty filtered page still mounts its pending slot, so visible demand can reach later books. */
function MoreAudiobooksRow({binding, page, first, guard}: {binding: AudiobookBindings; page: ContentProjection; first: RelationPage; guard: () => void}) {
  const row = useRelationRow(binding.context, first, first.entries, () => binding.moreBooksPager(page, first));
  if (!row.entries.length && !row.pagination) return null;
  return <TitleRow heading={t('title.moreAudiobooks')} subtitle={t('title.otherBooksOnServer')} entries={row.entries}
    binding={{context: binding.context}} guard={guard} pagination={row.pagination}/>;
}
function Chapter({entry, index, page, binding}: {entry: ContentEntry; index: number; page: ContentProjection; binding: AudiobookBindings}) {
  const resume = page.listening?.resume;
  const current = !!resume && entry.playback?.itemId === resume.itemId && resume.positionSeconds >= (entry.playback.startSeconds ?? 0) && entry.duration !== undefined && resume.positionSeconds < (entry.playback.startSeconds ?? 0) + entry.duration;
  const fraction = current && resume && entry.duration ? (resume.positionSeconds - (entry.playback?.startSeconds ?? 0)) / entry.duration : undefined;
  // desktop-listen #book: chapters before the resume point read as done (a check, dimmed), the current one is lit.
  const done = !current && !!resume && entry.playback?.itemId === resume.itemId && entry.duration !== undefined
    && (entry.playback.startSeconds ?? 0) + entry.duration <= resume.positionSeconds;
  return <div className={styles.chapterRow}><button className={styles.chapter} data-current={current || undefined} data-done={done || undefined} disabled={!entry.playback || entry.available === false || entry.missing} onClick={() => runScreenCommand(binding.context, () => binding.play(page, entry))}>
    <span className={styles.number}>{done ? <Icon name="i-check" size={18}/> : index + 1}</span><span className={styles.chapterTitle}>{entry.title}</span><span className={styles.duration}>{entry.duration === undefined ? undefined : formatClock(entry.duration)}</span>
    {fraction !== undefined && <span className={styles.chapterProgress}><i style={{width: `${Math.min(1, Math.max(0, fraction)) * 100}%`}}/></span>}
  </button>{entry.kind === 'audiobook_file' && <AudiobookMore binding={binding} page={page} entry={entry}/>}</div>;
}
