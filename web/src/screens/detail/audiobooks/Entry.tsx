import {audiobookChapterCount} from './chapter-count';
import {Fragment, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {serviceI18n} from '@core/presentation/service-text.ts';
import {formatClock} from '@core/presentation/content.ts';
import {ApiError} from '@core/index.ts';
import type {ContentEntry, ContentProjection} from '@core/library-content.ts';
import type {RelationPage} from '@core/relation-rows.ts';
import {Menu, MenuItem} from '../../../kit/overlays';
import {Artwork, AmbientArt} from '../../../kit/artwork';
import {Button, Chip} from '../../../kit/controls';
import {dependsOn, titlesWithPersonalState} from '@core/freshness-hub.ts';
import {useFreshDocument} from '../../../platform/freshness';
import {createAudiobookBindings, type AudiobookBindings, type AudiobookTarget} from './bindings';
import {runScreenCommand, type ScreenContext} from '../context';
import {DetailLoading} from '../Loading';
import {ScreenProblem} from '../ScreenProblem';
import {t} from '../text';
import styles from './audiobooks.module.css';
import {AudiobookMore} from './More';
import {TitleRow} from '../Detail';
import {useRelationRow} from '../relation-row';
import {useShellWidth} from '../../../shell/viewport';

type Props = {context: ScreenContext; target: AudiobookTarget; onMore?: (target: ContentEntry, anchor: HTMLElement) => void};
type Lease = Readonly<{token: number; context: ScreenContext; key: string; binding: AudiobookBindings}>;
/** One captured page operation: the exact binding and page it was claimed for. */
type Operation = Readonly<{binding: AudiobookBindings; page: ContentProjection; kind: 'watchlist' | 'finished'}>;

/** One committed binding lease per original context/library/view/entity, retired synchronously by
 * the layout effect that made it. Only a lease that still matches the live token and the inputs it
 * was made for is published, so a stale body can never render under new ones. */
export function AudiobookEntry({context, target, onMore}: Props) {
  const {libraryId, view, entityId} = target, key = JSON.stringify([libraryId, view, entityId]);
  const [lease, setLease] = useState<Lease>();
  const live = useRef<Lease | undefined>(undefined), serial = useRef(0);
  useLayoutEffect(() => {
    const binding = createAudiobookBindings(context, {libraryId, view, entityId}), next: Lease = Object.freeze({token: ++serial.current, context, key, binding});
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
const duration = (value: number | undefined) => value === undefined ? undefined : serviceI18n().duration(value);
/** The book's own language code, named in the reader's language; an unknown code stays itself. */
function languageName(code: string): string {
  try { return new Intl.DisplayNames(serviceI18n().locale, {type: 'language'}).of(code) ?? code; } catch { return code; }
}
/** The drawn "Read by …" line, with the current locale's own list punctuation; each name is a real
 * destination when this library's checked Books capabilities offer the narrator filter. */
function Narrators({names, linked, onOpen, locale}: {names: readonly string[]; linked: boolean; onOpen(name: string): void; locale: string | undefined}) {
  const [before, after] = t('next.kit.readBy', {name: '\uFFF0'}).split('\uFFF0');
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
  const [more, setMore] = useState<Readonly<{entry: ContentEntry; page: ContentProjection; anchor: HTMLElement}>>();
  const page = state.projection, entity = page?.entity;
  useFreshDocument(() => [dependsOn.libraries, ...titlesWithPersonalState(page?.scope.libraryId), dependsOn.event('stream.resync')], refresh => binding.service.revalidate(refresh), () => binding.service.getSnapshot().phase === 'ready');
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
    setExpanded(false); setChaptersExpanded(false); setFinished(undefined); setWatchlisted(undefined); setFilters({narrator: false, series: false}); setMoreBooks(undefined); setMore(undefined);
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
  const label = book ? resume ? progress?.chapter ? t('title.resumeChapter', {chapter: t('next.kit.chapter', {number: progress.chapter.index})}) : t('entry.resume') : t('entry.play') : t('next.browse.resume', {title: primaryBook?.title ?? ''});
  const cover = entity.posterUrl ?? rows?.entries[0]?.posterUrl;
  const leafMore = (entry: ContentEntry, anchor: HTMLElement) => {
    binding.current(page);
    if (entry.kind !== 'audiobook_file' || entry.available === false || entry.missing || !state.appended.some(section => section.entries.includes(entry))) return;
    setMore({entry, page, anchor});
  };
  const localOwner = binding.context.identity?.viewer.authority === 'local' && binding.context.identity.viewer.role === 'owner';
  const effects = binding.context.effects;
  const hasLeafActions = !!(effects.addToPlaylist || effects.addToCollection || localOwner && effects.deleteTitle);
  const invoke = (effect: typeof effects.addToPlaylist) => run(() => {
    binding.current(page);
    if (!more || more.page !== page || !effect || more.entry.available === false || more.entry.missing || !binding.service.getSnapshot().appended.some(section => section.entries.includes(more.entry))) return;
    return effect({libraryId: page.scope.libraryId, itemId: more.entry.id, kind: more.entry.kind, title: more.entry.title}, more.anchor);
  });
  const open = (entry: ContentEntry) => run(() => binding.open(page, entry));
  return <article className={styles.page} data-audiobook-view={page.scope.view} data-layout={phone ? 'phone' : 'desktop'}>
    <AmbientArt className={styles.ambient} treatment="phone" source={binding.context.artwork.ambient(cover)}/>
    <nav className={styles.nav}>
      {binding.context.effects.back && <Button variant="ghost" icon="i-left" iconOnly aria-label={t('action.back')} onClick={() => run(() => {binding.current(page);binding.context.effects.back?.();})}/>}
      {onMore && <Button variant="ghost" icon="i-more" iconOnly aria-label={t('card.more', {title: entity.title})} onClick={event => run(() => {binding.current(page);onMore(entity, event.currentTarget);})}/>}
      {/* Phones keep the drawn bar's More; the desktop page draws it in the action row. */}
      {!onMore && phone && <AudiobookMore binding={binding} page={page} entry={entity}/>}
    </nav>
    <header className={styles.hero}>
      <div className={author ? styles.disc : styles.cover}><Artwork source={binding.context.artwork.foreground(cover, 464, 1)} alt=""/>{author && <b>{entity.title.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => word[0]).join('')}</b>}</div>
      {book && series ? <button className={styles.eyebrow} onClick={() => run(() => filters.series && seriesTarget ? binding.openSeries(page, series, seriesTarget.name) : open(series))}>{series.title}</button> : !book && <p className={styles.eyebrow}>{t(author ? 'filter.field.author' : 'library.tab.series')}</p>}
      <h1>{entity.displayTitle ?? entity.title}</h1>
      {entity.author && (authorEntry ? <button className={styles.author} onClick={() => open(authorEntry)}>{entity.author}</button> : <div className={styles.author}>{entity.author}</div>)}
      <div className={styles.meta}>
        {book && !!entity.narrators?.length && <span><Narrators names={entity.narrators} linked={filters.narrator} locale={serviceI18n().locale}
          onOpen={name => run(() => binding.openNarrator(page, name))}/></span>}
        {!book && entity.count !== undefined && <strong>{t('next.kit.books', {count: entity.count})}</strong>}
        {entity.duration !== undefined && <span>{duration(entity.duration)}</span>}
        {book && chapterCount !== undefined && <span>{t('title.chapterCount', {count: chapterCount})}</span>}
      </div>
    </header>
    <div className={styles.actions}>
      {canPlay && <Button className={styles.primary} variant="primary" icon="i-play" onClick={() => run(() => binding.primary(page))}>{label}</Button>}
      {book && page.listening?.actions.includes('play') && <Button variant="glass" onClick={() => run(() => binding.primary(page, true))}>{t('action.startOver')}</Button>}
      {book && !phone && watchlisted !== undefined && <Button className={styles.round} icon={watchlisted ? 'i-check' : 'i-plus'} iconOnly aria-pressed={watchlisted} disabled={busy !== undefined}
        aria-label={t('next.controls.myList')} onClick={() => {
          const op = claim(page, 'watchlist'); if (!op) return;
          void binding.setWatchlisted(page, !watchlisted).then(value => {if (owns(op)) setWatchlisted(value);}, refuse(op)).finally(() => settle(op));
        }}/>}
      {book && finished !== undefined && <Button className={styles.round} icon="i-check" iconOnly disabled={busy !== undefined} aria-pressed={finished} aria-label={t(finished ? 'title.markNotFinished' : 'title.markFinished')} onClick={() => {
        const op = claim(page, 'finished'); if (!op) return;
        void binding.markFinished(page).then(value => {if (owns(op)) setFinished(value.watched);}, refuse(op)).finally(() => settle(op));
      }}/>}
      {!book && !author && primaryBook && <Button className={styles.round} icon="i-retry" iconOnly aria-label={t('action.startOver')} onClick={() => open(primaryBook)}/>}
      {!onMore && !phone && <AudiobookMore binding={binding} page={page} entry={entity}/>}
    </div>
    {book && remaining && <div className={styles.progress}>{ratio !== undefined && <span><i style={{width: `${Math.min(1, Math.max(0, ratio)) * 100}%`}}/></span>}{t('title.resumeRemaining', {remaining})}</div>}
    {book && <aside className={styles.aside}>
      {entity.overview && <><p className={styles.about} data-expanded={expanded || undefined}>{entity.overview}</p>{!expanded && <button className={styles.readMore} onClick={() => setExpanded(true)}>{t('web.person.readMore')}</button>}</>}
      {/* desktop-listen's About this book: only the facts this book publishes, and only at desktop. */}
      {!phone && <dl className={styles.bookFacts}>
        {entity.author && <><dt>{t('filter.field.author')}</dt><dd>{authorEntry ? <button type="button" className={styles.link} onClick={() => open(authorEntry)}>{entity.author}</button> : entity.author}</dd></>}
        {!!entity.narrators?.length && <><dt>{t('filter.field.narrator')}</dt><dd><Narrators names={entity.narrators} linked={filters.narrator} locale={serviceI18n().locale}
          onOpen={name => run(() => binding.openNarrator(page, name))}/></dd></>}
        {series && <><dt>{t('library.tab.series')}</dt><dd><button type="button" className={styles.link}
          onClick={() => run(() => filters.series && seriesTarget ? binding.openSeries(page, series, seriesTarget.name) : open(series))}>{series.title}</button>{seriesTarget?.position && t('title.bookInSeries', {number: seriesTarget.position})}</dd></>}
        {entity.publishedYear !== undefined && <><dt>{t('title.published')}</dt><dd>{serviceI18n().number(entity.publishedYear, {useGrouping: false})}</dd></>}
        {entity.publisher && <><dt>{t('title.publisher')}</dt><dd>{entity.publisher}</dd></>}
        {entity.language && <><dt>{t('next.selection.language')}</dt><dd>{languageName(entity.language)}</dd></>}
        {entity.addedAt && <><dt>{t('title.added')}</dt><dd>{serviceI18n().date(entity.addedAt, 'medium')}</dd></>}
      </dl>}
    </aside>}
    {state.error && <ScreenProblem context={binding.context} error={state.error} retry={() => {void binding.load().catch(binding.context.effects.reportError);}}/>}
    {rows && <section><div className={styles.sectionHead}><h2>{t(book ? 'next.browse.chapters' : 'library.tab.books')}</h2><span>{rows.totalCount}</span></div>
      {(book && !chaptersExpanded ? rows.entries.slice(0, CHAPTER_PREVIEW) : rows.entries).map((entry, index) => book ? <div key={entry.id}><Chapter entry={entry} index={index} page={page} binding={binding}/>{entry.kind === 'audiobook_file' && hasLeafActions && <Button variant="ghost" icon="i-more" iconOnly aria-label={t('card.more', {title: entry.title})} onClick={event => run(() => leafMore(entry, event.currentTarget))}/>}</div>
        : <div className={styles.bookRow} key={entry.id}><button onClick={() => open(entry)}><span className={styles.thumb}><Artwork source={binding.context.artwork.foreground(entry.posterUrl, 132, 1)} alt=""/></span><span><b>{entry.displayTitle ?? entry.title}</b>{entry.narrators?.length ? <small>{t('next.kit.readBy', {name: serviceI18n().list(entry.narrators)})}</small> : null}{entry.bookProgress?.remainingMs !== undefined && <small>{t('title.resumeRemaining', {remaining: duration(entry.bookProgress.remainingMs / 1000)!})}</small>}</span></button>{onMore && <Button variant="ghost" icon="i-more" iconOnly aria-label={t('card.more', {title: entry.title})} onClick={event => run(() => {binding.current(page);onMore(entry, event.currentTarget);})}/>}</div>)}
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
    {book && moreBooks && moreBooks.page === page && moreBooks.token === reads.current?.token && <MoreAudiobooksRow binding={binding} page={page} first={moreBooks.value} guard={() => {
      // A retained card belongs to this exact page and read receipt; it may not open after either was replaced.
      if (reads.current?.token !== moreBooks.token) throw new DOMException('Book page changed', 'AbortError');
      binding.current(moreBooks.page);
    }} />}
    {more?.page === page && <Menu open onOpenChange={value => {if (!value) setMore(undefined);}} title={more.entry.title} anchorRef={{current: more.anchor}}>
      {effects.addToPlaylist && <MenuItem onSelect={() => invoke(effects.addToPlaylist)}>{t('entry.addToPlaylist')}</MenuItem>}
      {effects.addToCollection && <MenuItem onSelect={() => invoke(effects.addToCollection)}>{t('entry.addToCollection')}</MenuItem>}
      {localOwner && effects.deleteTitle && <MenuItem onSelect={() => invoke(effects.deleteTitle)}>{t('entry.delete')}</MenuItem>}
    </Menu>}
    {!book && <section><div className={styles.sectionHead}><h2>{t('next.controls.details')}</h2></div><dl className={styles.details}>{entity.narrators?.length ? <><dt>{t('filter.field.narrator')}</dt><dd>{serviceI18n().list(entity.narrators)}</dd></> : null}{entity.duration !== undefined && <><dt>{t('next.kit.length')}</dt><dd>{duration(entity.duration)}</dd></>}</dl></section>}
  </article>;
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
  return <div className={styles.chapterRow}><button className={styles.chapter} data-current={current || undefined} disabled={!entry.playback || entry.available === false || entry.missing} onClick={() => runScreenCommand(binding.context, () => binding.play(page, entry))}>
    <span className={styles.number}>{index + 1}</span><span className={styles.chapterTitle}>{entry.title}</span><span className={styles.duration}>{entry.duration === undefined ? undefined : formatClock(entry.duration)}</span>
    {fraction !== undefined && <span className={styles.chapterProgress}><i style={{width: `${Math.min(1, Math.max(0, fraction)) * 100}%`}}/></span>}
  </button>{entry.kind === 'audiobook_file' && <AudiobookMore binding={binding} page={page} entry={entry}/>}</div>;
}
