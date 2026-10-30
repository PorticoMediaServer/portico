import {defaultI18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import {Button} from '../../../kit/controls';
import {Banner} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {watchClock} from '../model';
import type {ListeningHost} from './use-listening-owner';
import styles from './audiobook-listening.module.css';
const t = defaultI18n.t;
export function ListeningChapters({host, snapshot, reportError}: {host: ListeningHost; snapshot: WebPlaybackRuntimeSnapshot; reportError(error: unknown): void}) {
  const {owner, state} = host, chapters = state.chapters;
  const run = (task: () => Promise<void>) => {void task().catch(error => {if (owner.canReport(state,true)) reportError(error);});};
  if (!chapters || chapters.data?.status === 'unavailable') return <StateView title={t('player.opt.chaptersUnavailable')}/>;
  if (chapters.phase === 'idle' || chapters.phase === 'loading') return <p role="status">{t('player.opt.loadingChapters')}</p>;
  if (chapters.phase === 'error' || chapters.phase === 'refresh-required') return <Banner action={{label: t('next.states.tryAgain'), onAction: () => run(() => owner.chapterPage(state, chapters, 'retry',true))}}>{t('player.opt.chaptersUnavailable')}</Banner>;
  if (!chapters.data?.chapters.length) return <StateView title={t('player.opt.noChapters')}/>;
  const position = snapshot.presentation?.positionSeconds ?? 0;
  return <section aria-label={t('player.opt.chapters')}>
    {chapters.data.chapters.map(chapter => <div key={chapter.id} className={styles.chapterRow} data-current={position >= chapter.startSeconds && position < chapter.endSeconds || undefined}>
      <button type="button" disabled={!snapshot.presentation?.controls.includes('seek')} onClick={() => run(() => owner.chapter(state, chapters, chapter,true))}>
        <span>{chapter.index}</span><b>{chapter.title}</b><small>{watchClock(chapter.endSeconds - chapter.startSeconds)}</small>
      </button>
    </div>)}
    <footer className={styles.pager}>
      {chapters.pagination.canPrevious && <Button size="small" onClick={() => run(() => owner.chapterPage(state, chapters, 'previous',true))}>{t('web.pager.previous')}</Button>}
      {chapters.data.nextCursor && <Button size="small" onClick={() => run(() => owner.chapterPage(state, chapters, 'next',true))}>{t('next.live.more')}</Button>}
    </footer>
  </section>;
}
