import type {ContentEntry} from '../library-content.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;

/**
 * One Continue your books card (tablet-read.html frame 11, Q24): the Read lane's resume card.
 * `where` is where you are and `left` how much is left, both from real facts only: a book's
 * saved page, an audiobook's chapter and time left. A reading time is never
 * estimated, an EPUB's place is its progress line (its saved section is a package path, not a
 * chapter name), and a stale saved position (the file changed since) shows progress only.
 */
export type ContinueBookFacts = Readonly<{
  reading: boolean;
  shape: 'book' | 'magazine' | 'newspaper' | 'audiobook';
  where?: string;
  left?: string;
  progress?: number;
}>;

export function continueBookFacts(entry: ContentEntry, t: Translate): ContinueBookFacts {
  if (entry.kind === 'ebook' || entry.kind === 'comic') {
    const shape = entry.publicationType === 'magazine' ? 'magazine' : entry.publicationType === 'newspaper' ? 'newspaper' : 'book';
    const saved = entry.readingProgress;
    if (!saved) return {reading: true, shape};
    if (saved.stale) return {reading: true, shape, progress: saved.progression};
    if (saved.pageIndex !== undefined) {
      const page = saved.pageIndex + 1, count = entry.pageCount;
      const known = count !== undefined && page <= count;
      return {reading: true, shape, progress: saved.progression,
        where: known ? t('card.pageOf', {page, count}) : t('card.page', {page}),
        ...(known && count - page > 0 ? {left: t('card.pagesLeft', {count: count - page})} : {})};
    }
    return {reading: true, shape, progress: saved.progression};
  }
  const book = entry.bookProgress;
  const remaining = book?.remainingMs !== undefined ? Math.round(book.remainingMs / 60000) : undefined;
  return {reading: false, shape: 'audiobook',
    ...(book?.positionMs !== undefined && book.durationMs ? {progress: Math.min(1, book.positionMs / book.durationMs)} : {}),
    ...(book?.chapter ? {where: book.chapter.title} : {}),
    ...(remaining !== undefined && remaining > 0 ? {left: remaining >= 60 ? t('card.hoursLeft', {hours: Math.floor(remaining / 60), minutes: remaining % 60}) : t('card.minutesLeft', {count: remaining})} : {})};
}
