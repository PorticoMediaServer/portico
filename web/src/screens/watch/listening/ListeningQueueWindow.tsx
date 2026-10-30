import {defaultI18n} from '@i18n';
import type {WebQueueEditPage, WebQueueEditSnapshot} from '../../../platform/playback';
import {Button} from '../../../kit/controls';
import {Artwork} from '../../../kit/artwork';
import type {ShellArtwork} from '../../../shell/artwork';
import {watchClock} from '../model';
import {Banner} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import styles from './listening-panel.module.css';
const t = defaultI18n.t;
export type ListeningQueueWindowProps = Readonly<{
  snapshot: WebQueueEditSnapshot | null; retained: WebQueueEditPage | null; anchor?: number; error: boolean; busy: boolean;
  artwork?: ShellArtwork;
  onRetry(): void;
  onEdit(page: WebQueueEditPage, entryId: string, action: 'remove' | -1 | 1 | 'next' | 'previous'): void;
}>;
/** An actual issued canonical page; retained prior rows are paint only after a
 * refusal. No preview DTO, occurrence id or neighbour index becomes a grant. */
export function ListeningQueueWindow({snapshot, retained, anchor, error, busy, artwork, onRetry, onEdit}: ListeningQueueWindowProps) {
  const page = snapshot?.page ?? retained;
  const ready = snapshot?.phase === 'ready' && snapshot.page === page && !snapshot.busy && !busy && !error;
  const upcoming = (index: number) => {const row = page?.rows[index];return !!row && anchor !== undefined && row.position > anchor && row.available && !row.hidden && !row.current;};
  return <div data-listening-queue-window>
    {(error || snapshot?.phase === 'retired' || snapshot?.phase === 'error') && <Banner action={{label: t('next.states.tryAgain'), onAction: onRetry}}>{t('next.states.pageFailedBody')}</Banner>}
    {!page && !error && snapshot?.phase !== 'retired' && <p role="status">{t('next.states.loading')}</p>}
    {page && !page.rows.length && <StateView title={t('player.queueEmpty')}/>}
    {page && <div role="list" aria-busy={snapshot?.busy || busy}>{page.rows.map((row, index) => <div role="listitem" className={styles.queueRow} key={row.entryId} data-current={row.current || undefined}>
      <span className={styles.grip} aria-hidden="true"/>
      <span className={styles.queueCover}>{!row.hidden && row.available && row.artworkUrl && <Artwork source={artwork?.foreground(row.artworkUrl, 80, 1)} alt=""/>}</span>
      <span className={styles.queueCopy}><b>{row.hidden || !row.available ? t('player.queueUnavailable') : row.title}</b>
        {!row.hidden && row.available && row.artist && <small>{row.artist}</small>}{row.current && <small>{t('player.queueNowPlaying')}</small>}</span>
      {!row.hidden && row.available && row.durationMs !== undefined && <span className={styles.duration}>{watchClock(row.durationMs / 1000)}</span>}
      {!row.current && !row.hidden && row.available && <div className={styles.actions}>
        <Button size="small" variant="ghost" icon="i-up" aria-label={t('player.queueMoveUp')} disabled={!ready || !upcoming(index) || !upcoming(index - 1) || page.rows[index - 1]?.position !== row.position - 1} onClick={() => onEdit(page, row.entryId, -1)}/>
        <Button size="small" variant="ghost" icon="i-down" aria-label={t('player.queueMoveDown')} disabled={!ready || !upcoming(index) || !upcoming(index + 1) || page.rows[index + 1]?.position !== row.position + 1} onClick={() => onEdit(page, row.entryId, 1)}/>
        <Button size="small" variant="ghost" icon="i-x" aria-label={t('player.queueRemove')} disabled={!ready || !upcoming(index)} onClick={() => onEdit(page, row.entryId, 'remove')}/>
      </div>}
    </div>)}</div>}
    {page && <footer className={styles.footer}>
      {page.canPrevious && <Button size="small" variant="glass" disabled={!ready} onClick={() => onEdit(page, '', 'previous')}>{t('web.pager.previous')}</Button>}
      {page.canNext && <Button size="small" variant="glass" disabled={!ready} onClick={() => onEdit(page, '', 'next')}>{t('next.live.more')}</Button>}
    </footer>}
  </div>;
}
