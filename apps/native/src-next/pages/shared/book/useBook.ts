import {useState, useSyncExternalStore} from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {TrackRowData} from '../../../kit/browse';
import type {StateRowProps} from '../../../kit/frames';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {AudiobookScreenController} from '../../../screens/destinations/audiobooks/model';
import {askNativeDownload} from '../../../screens/downloads/request';
import {nativeDownloadRequest} from '../../../screens/watch/download-request';
import {bookPlace} from './book-place';

const t = defaultI18n.t;
// Lengths read as on every title page ("8 h 13 min", collections 41a).
const duration = (seconds: number | undefined) => seconds === undefined ? undefined : durationText(seconds, t);

export type BookPageProps = Readonly<{
  controller: AudiobookScreenController; entry: ContentEntry; runtime: NativeShellRuntime;
  actions: Pick<NativeEntryActions, 'menu'>;
  /** Opens an author or series destination the book's own projection names. */
  openEntry?: (entry: ContentEntry) => void; onBack?: () => void;
}>;

/** What an audiobook's page shows on a phone (collections 41a), from the audiobook controller the old book screen read:
 * data only. */
export type BookView = Readonly<{
  title: string; author?: string; cover?: ArtworkSource; glow?: AmbientArtSource;
  /** The book's series and its place in it ("Harry Potter, book 1"), a link to the series when the server names one. */
  kicker?: Readonly<{label: string; onPress?: () => void}>;
  /** The author under the title, a link to their page when the server names one. */
  byline?: Readonly<{text: string; onPress?: () => void}>;
  /** Who reads it, how long it is, and how many chapters (only a real chapter list names a count). */
  facts: readonly FactPart[];
  /** Resume chapter N | Resume | Play, the book-wide progress and the time left. */
  primary: Readonly<{label: string; progress?: number; remaining?: string; onPress: () => void}>;
  finished: Readonly<{active: boolean; onPress: () => void}>;
  download?: () => void;
  /** The book's menu, with Start over in it. */
  more: () => void;
  description?: Readonly<{text: string; readMore: Readonly<{label: string; expanded: boolean; onPress: () => void}>}>;
  /** The read failed, or a further page of chapters is loading or failed. */
  state?: StateRowProps; continuation?: StateRowProps;
  /** The chapters (or the book's parts when it has no chapters): the finished ones checked, the one you are in marked
   * with its progress. */
  chapters?: Readonly<{title: string; count: string; rows: readonly TrackRowData[]; onPlay: (id: string) => void}>;
  /** The scroller nears the end: the next page of chapters. */
  onNearEnd: () => void;
}>;

export function useBook({controller, entry, runtime, actions, openEntry}: BookPageProps): BookView {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), [expanded, setExpanded] = useState(false);
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const entity = snapshot.entity ?? entry;
  const chapterSection = snapshot.sections.find(section => section.id === 'chapters'), partSection = snapshot.sections.find(section => section.id === 'files');
  const chapters = chapterSection?.entries ?? [], parts = partSection?.entries ?? [];
  const rows = chapters.length ? chapters : parts, rowsSection = chapters.length ? chapterSection : partSection;
  // The projection's own Author & series entries carry the real navigation ids; a link exists only when the server sent one.
  const context = snapshot.sections.find(section => section.id === 'context')?.entries ?? [];
  const seriesEntry = context.find(value => value.kind === 'book_series'), authorEntry = context.find(value => value.kind === 'author');
  // Where this profile is in the rows: the shared rule every book page takes (book-place.ts).
  const progress = entity.bookProgress, place = bookPlace(rows, progress);
  const currentRow = place?.index ?? -1, currentId = place?.id;
  // The book's bar is book-wide only: the whole-book position against the whole-book length.
  const ratio = progress?.positionMs !== undefined && progress.durationMs ? progress.positionMs / progress.durationMs : undefined;
  const remaining = progress?.remainingMs === undefined ? undefined : duration(progress.remainingMs / 1000);
  // A book's entity count is its files, not chapters: only a real chapter list names a chapter count.
  const chapterCount = chapters.length ? chapterSection?.totalCount : undefined;
  const facts: FactPart[] = [];
  if (entity.narrators?.length) facts.push({id: 'narrators', text: t('next.kit.readBy', {name: defaultI18n.list(entity.narrators)}), role: 'identity'});
  if (entity.duration !== undefined) facts.push({id: 'duration', text: duration(entity.duration)!, role: 'context'});
  if (chapterCount !== undefined) facts.push({id: 'chapters', text: t('title.chapterCount', {count: chapterCount}), role: 'context'});
  const canResume = !!snapshot.listening?.resume && !snapshot.listening?.resumeUnavailable;
  const label = canResume && currentRow >= 0 && rows === chapters ? t('title.resumeChapterNumber', {number: currentRow + 1}) : t(canResume ? 'entry.resume' : 'entry.play');
  const series = entity.series?.[0], download = nativeDownloadRequest(entity);
  const presented = (error: unknown, refresh?: boolean) => presentError(refresh ? {code: 'refresh_required'} : error, 'detail', {operation: 'load'});
  const failed = snapshot.phase === 'error' || snapshot.phase === 'refresh-required' ? presented(snapshot.error, snapshot.phase === 'refresh-required') : undefined;
  const more = snapshot.continuation, moreFailed = more && more.phase !== 'loading' ? presented(more.error, more.phase === 'refresh-required') : undefined;
  return {
    title: entity.displayTitle ?? entity.title, author: entity.author, cover: runtime.artwork(entity.posterUrl), glow: runtime.ambient(entity.posterUrl),
    kicker: series || seriesEntry ? {label: series?.position ? t('title.seriesAndBook', {series: series.name, number: series.position}) : series?.name ?? seriesEntry!.title,
      onPress: seriesEntry && openEntry ? () => openEntry(seriesEntry) : undefined} : undefined,
    byline: entity.author || authorEntry ? {text: entity.author ?? authorEntry!.title, onPress: authorEntry && openEntry ? () => openEntry(authorEntry) : undefined} : undefined,
    facts,
    primary: {label, progress: remaining ? ratio : undefined, remaining: remaining ? t('next.kit.remaining', {time: remaining}) : undefined,
      onPress: () => run(canResume ? controller.resume() : controller.startOver())},
    finished: {active: snapshot.finished, onPress: () => run(controller.markFinished())},
    download: download ? () => askNativeDownload(download) : undefined,
    more: () => actions.menu(entity, undefined, undefined, [{id: 'book-start-over', icon: 'i-restart', label: t('action.startOver'), onSelect: () => run(controller.startOver())}]),
    description: entity.overview ? {text: entity.overview,
      readMore: {label: t(expanded ? 'web.person.showLess' : 'web.person.readMore'), expanded, onPress: () => setExpanded(value => !value)}} : undefined,
    state: failed ? {kind: 'error', icon: 'i-warn', title: failed.title, action: {label: failed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.load())}} : undefined,
    continuation: more?.phase === 'loading' ? {kind: 'loading'}
      : moreFailed ? {kind: 'error', icon: 'i-warn', title: moreFailed.title, action: {label: moreFailed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.recover())}} : undefined,
    chapters: rows.length ? {title: t('next.browse.chapters'), count: defaultI18n.number(rowsSection?.totalCount ?? rows.length),
      rows: rows.map((row, index) => {
        // A finished chapter trades its number for the check; the server sends `watched` only when playback passed its end.
        const current = row.id === currentId, left = current && place?.secondsLeft !== undefined ? duration(place.secondsLeft) : undefined;
        return {id: row.id, number: index + 1, done: row.watched === true && !current, current,
          progress: current ? place?.fraction : undefined,
          duration: row.duration === undefined ? undefined : {id: 'duration', text: duration(row.duration)!, role: 'context'},
          text: {id: row.id, title: row.title, caption: [], detail: left ? [{id: 'remaining', text: t('next.kit.remaining', {time: left}), role: 'context'}] : []}};
      }),
      onPlay: id => {const row = rows.find(value => value.id === id); if (row) run(controller.playChapter(row));}} : undefined,
    onNearEnd: () => {if (rowsSection) run(controller.next(rowsSection.id));},
  };
}
