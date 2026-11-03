import {defaultI18n} from '@i18n';
import type {BookChapter, BookChaptersView} from '@core/book-chapters.ts';
export {playingBookChapter} from '@core/book-chapters.ts';
import {Icon} from '../../../kit/Icon';
import {watchClock} from '../model';
import styles from './audiobook-listening.module.css';
const t = defaultI18n.t;

/** The whole book's chapters across its parts (MU-2). A row in another part starts that part
 * at the chapter; the playing row is marked. Numbers count through the whole book. */
export function BookChapterList({book, playing, disabled, onPick}: Readonly<{
  book: BookChaptersView; playing: boolean; disabled: boolean; onPick: (chapter: BookChapter) => void;
}>) {
  return <div role="list" aria-label={t('player.opt.chapters')}>{book.chapters.map((chapter, index) => {
    const current = index === book.current;
    return <div role="listitem" key={chapter.id} className={styles.chapterRow} data-current={current || undefined}>
      <button type="button" disabled={disabled} onClick={() => onPick(chapter)} aria-current={current ? 'true' : undefined}>
        <span>{current ? <Icon name={playing ? 'i-play' : 'i-pause'} size={16}/> : chapter.number}</span><b>{chapter.title}</b><small>{watchClock(chapter.endSeconds - chapter.startSeconds)}</small>
      </button>
    </div>;
  })}</div>;
}

