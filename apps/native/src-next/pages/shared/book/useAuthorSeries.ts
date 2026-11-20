import {useSyncExternalStore} from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AboutValue, StateRowProps, TitleListItem} from '../../../kit/frames';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {AudiobookScreenController} from '../../../screens/destinations/audiobooks/model';
import {audiobookAuthorInitials} from '../../../screens/destinations/audiobooks/presentation';

const t = defaultI18n.t;
// Lengths read as on every title page ("27 h 3 min", collections 41b).
const duration = (seconds: number | undefined) => seconds === undefined ? undefined : durationText(seconds, t);

export type AuthorSeriesPageProps = Readonly<{
  controller: AudiobookScreenController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Pick<NativeEntryActions, 'menu'>;
  openEntry: (entry: ContentEntry) => void; onBack?: () => void;
}>;

/** What an author's or a series' page shows on a phone (collections 41b, 41c), from the audiobook controller the old
 * author / series screen read: data only. */
export type AuthorSeriesView = Readonly<{
  series: boolean; title: string; kicker: string;
  /** A series: its first book's cover (else its own). An author: the circle of their books' covers with their initials. */
  cover?: ArtworkSource; covers: readonly ArtworkSource[]; initials: string; glow?: AmbientArtSource;
  /** A series' author, a link to their page when the projection names one. */
  byline?: Readonly<{text: string; onPress?: () => void}>;
  facts: readonly FactPart[];
  /** Resume the book this profile is in, else open the first book. */
  primary: Readonly<{label: string; disabled: boolean; onPress: () => void}>;
  /** A series: Start the series over (opens its first book). */
  startOver?: () => void;
  more: () => void;
  state?: StateRowProps; continuation?: StateRowProps;
  books: Readonly<{title: string; count: string; rows: readonly (TitleListItem & Readonly<{id: string}>)[]}>;
  /** An author: who read their books and how long they all are. */
  details?: readonly Readonly<{label: string; values: readonly AboutValue[]}>[];
  onNearEnd: () => void;
}>;

export function useAuthorSeries({controller, entry, runtime, actions, openEntry}: AuthorSeriesPageProps): AuthorSeriesView {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const entity = snapshot.entity ?? entry, series = entry.kind === 'book_series', title = entity.displayTitle ?? entity.title;
  const failed = snapshot.phase === 'error' || snapshot.phase === 'refresh-required';
  const books = snapshot.sections.find(section => section.id === 'books')?.entries ?? [];
  // A series' author opens the author's page only when the projection named a real author identity.
  const authorEntry = (snapshot.sections.find(section => section.id === 'context')?.entries ?? []).find(value => value.kind === 'author');
  // Resume continues the book this profile is actually in: the listening journey names it, and a book's own progress names
  // its part. Neither is guessed from order (the old page's rule).
  const listeningId = snapshot.listening?.resume?.itemId;
  const resume = (listeningId === undefined ? undefined : books.find(book => book.bookProgress?.itemId === listeningId)) ?? books.find(book => book.bookProgress?.itemId);
  const author = series ? books[0]?.author ?? books[0]?.subtitle : undefined;
  const narrators = [...new Set([...(entity.narrators ?? []), ...books.flatMap(book => book.narrators ?? [])])];
  const coverPath = series ? books[0]?.posterUrl ?? entity.posterUrl : entity.posterUrl;
  const facts: FactPart[] = [];
  if (entity.count !== undefined) facts.push({id: 'books', text: t('next.kit.books', {count: entity.count}), role: 'identity'});
  if (entity.duration !== undefined) facts.push({id: 'length', text: duration(entity.duration)!, role: 'context'});
  const presented = (error: unknown, refresh?: boolean) => presentError(refresh ? {code: 'refresh_required'} : error, 'detail', {operation: 'load'});
  const readFailed = failed ? presented(snapshot.error, snapshot.phase === 'refresh-required') : undefined;
  const more = snapshot.continuation, moreFailed = more && more.phase !== 'loading' ? presented(more.error, more.phase === 'refresh-required') : undefined;
  return {
    // 41b "Author", 41c "Series" over the name.
    series, title, kicker: series ? t('next.kit.seriesKind') : t('title.kind', {kind: entry.kind}),
    cover: series ? runtime.artwork(coverPath) : undefined,
    covers: series ? [] : books.flatMap(book => {const art = runtime.artwork(book.posterUrl); return art ? [art] : [];}),
    initials: audiobookAuthorInitials(title), glow: runtime.ambient(coverPath ?? books[0]?.posterUrl),
    byline: author || authorEntry ? {text: author ?? authorEntry!.title, onPress: authorEntry ? () => openEntry(authorEntry) : undefined} : undefined,
    facts,
    // A series resumes "chapter N" of the book you are in when its progress names the chapter (41c), as that book's own page
    // does; an author names the book (41b "Resume The Martian").
    primary: {label: !resume ? t('entry.play') : series && resume.bookProgress?.chapter?.number ? t('title.resumeChapterNumber', {number: resume.bookProgress.chapter.number})
      : t('next.browse.resume', {title: resume.title}), disabled: failed || !books.length,
      onPress: () => {if (resume) run(controller.resumeBook(resume)); else if (books[0]) openEntry(books[0]);}},
    startOver: series && books[0] ? () => openEntry(books[0]!) : undefined,
    more: () => actions.menu(entity),
    state: readFailed ? {kind: 'error', icon: 'i-warn', title: readFailed.title, action: {label: readFailed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.load())}} : undefined,
    continuation: more?.phase === 'loading' ? {kind: 'loading'}
      : moreFailed ? {kind: 'error', icon: 'i-warn', title: moreFailed.title, action: {label: moreFailed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.recover())}} : undefined,
    // The rows keep the server's order; only a server-sent series position is labelled, nothing is numbered by index.
    books: {title: t(series ? 'next.kit.booksInSeries' : 'next.kit.booksHeading'), count: defaultI18n.number(entity.count ?? books.length),
      rows: books.map(book => {
        const left = book.bookProgress?.remainingMs === undefined ? undefined : duration(book.bookProgress.remainingMs / 1000);
        const position = series ? book.series?.[0]?.position : undefined;
        // A finished book reads "Finished" (books, as elsewhere), not a film's "Watched".
        return {id: book.id, square: true, title: book.displayTitle ?? book.title, artwork: runtime.artwork(book.posterUrl), watched: book.watched === true, doneLabel: t('title.finished'),
          kicker: position ? t('next.reader.bookNumber', {number: position}) : undefined,
          facts: book.narrators?.length ? [t('next.kit.readBy', {name: defaultI18n.list(book.narrators)})] : [],
          note: left ? t('next.kit.remaining', {time: left}) : undefined,
          // A phone's row opens the book page; its ⋯ opens the book's menu.
          onNavigate: () => openEntry(book), onMore: anchor => actions.menu(book, anchor), testID: `audiobook-book-${book.id}`};
      })},
    details: !series && (narrators.length || entity.duration !== undefined) ? [
      ...(narrators.length ? [{label: t('next.kit.narrators'), values: [{text: defaultI18n.list(narrators)}]}] : []),
      ...(entity.duration !== undefined ? [{label: t('next.kit.length'), values: [{text: duration(entity.duration)!}]}] : [])] : undefined,
    onNearEnd: () => run(controller.next('books')),
  };
}
