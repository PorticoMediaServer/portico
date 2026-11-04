import {defaultI18n} from '@i18n';
import type {BookChapter, BookChaptersView} from '@core/book-chapters.ts';
export {playingBookChapter} from '@core/book-chapters.ts';
import {Icon} from '../../../kit/Icon';
import {watchClock} from '../model';
import styles from './audiobook-listening.module.css';
const t = defaultI18n.t;

/** The whole book's chapters across its parts (MU-2). A row in another part starts that part
 * at the chapter; the playing row is marked. Numbers count through the whole book.
 * desktop-listen, audiobook player: a count and "Chapter N of M" head the list, chapters already
 * passed carry a check, and the playing one shows how far in it is and the time left in it. */
export function BookChapterList({book, playing, disabled, onPick, positionSeconds}: Readonly<{
  book: BookChaptersView; playing: boolean; disabled: boolean; onPick: (chapter: BookChapter) => void; positionSeconds?: number;
}>) {
  const current = book.chapters[book.current], inPart = current && current.itemId === book.itemId ? current : undefined;
  const span = inPart ? inPart.endSeconds - inPart.startSeconds : 0;
  const into = inPart && positionSeconds !== undefined && Number.isFinite(positionSeconds) && span > 0 ? Math.max(0, Math.min(span, positionSeconds - inPart.startSeconds)) : undefined;
  return <>
    <div className={styles.chapterHead}><span>{t('next.kit.chapters', {count: book.chapters.length})}</span>
      {current && <span>{t('next.kit.chapterOf', {number: current.number, total: book.chapters.length})}</span>}</div>
    <div role="list" aria-label={t('player.opt.chapters')}>{book.chapters.map((chapter, index) => {
      const playingRow = index === book.current, passed = index < book.current;
      const left = playingRow && into !== undefined ? t('next.kit.remaining', {time: watchClock(span - into)}) : undefined;
      return <div role="listitem" key={chapter.id} className={styles.chapterRow} data-current={playingRow || undefined} data-passed={passed || undefined}>
        <button type="button" disabled={disabled} onClick={() => onPick(chapter)} aria-current={playingRow ? 'true' : undefined}>
          <span>{playingRow ? <Icon name={playing ? 'i-play' : 'i-pause'} size={16}/> : passed ? <Icon name="i-check" size={15}/> : chapter.number}</span>
          <b>{chapter.title}</b><small>{left ?? watchClock(chapter.endSeconds - chapter.startSeconds)}</small>
          {left && <span className={styles.chapterProgress} aria-hidden="true"><i style={{width: `${(into! / span) * 100}%`}}/></span>}
        </button>
      </div>;
    })}</div>
  </>;
}
