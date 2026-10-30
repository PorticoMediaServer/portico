import type {DockPlayback} from '../../../../packages/player-core/src/index.ts';
import {Artwork} from '../artwork';
import {Button} from '../controls/Button';
import {Facts} from './parts';
import {clockText, dispatchDockAction} from './model';
import {dockText} from './text';
import type {DockBinding, DockPresentation, DockTranslate} from './types';
import styles from './dock.module.css';

export type DockNoticeData = Readonly<{kind: 'paused-audio' | 'stopped-video'; playback: DockPlayback; presentation: DockPresentation; changedAtMs: number}>;
/** Transition notes have real bookmark inputs, a 300ms delay and eight-second action lifetime. */
export function DockNotice({notice, nowMs, binding, phone = false, t = dockText}: {notice: DockNoticeData | null; nowMs: number; binding: DockBinding; phone?: boolean; t?: DockTranslate}) {
  if (!notice || nowMs < notice.changedAtMs + 300 || nowMs >= notice.changedAtMs + 8300) return null;
  const {playback, presentation, kind} = notice, audio = kind === 'paused-audio';
  const artist = presentation.identity.kind === 'music' ? presentation.identity.subtitle.find(part => part.id === 'artist')?.text : undefined;
  const action = {kind: audio ? 'restore-audio' as const : 'restore-video' as const, fence: playback.fence};
  return <div className={styles.notice} data-phone={phone || undefined} role="status">
    {!phone && <Artwork source={presentation.artwork} alt="" className={audio ? styles.noticeSquare : styles.noticeWide} />}
    <span>{phone ? <><b>{presentation.identity.title}</b>{audio ? t('next.dock.musicPausedPhoneBody') : <>{t('next.dock.videoStoppedBody', {position: clockText(playback.positionSeconds) ?? ''})} {t('next.dock.saved')}</>}</> : <><b>{t(audio ? 'next.dock.musicPaused' : 'next.dock.videoStopped', {position: clockText(playback.positionSeconds) ?? '', title: presentation.identity.title})}</b><br />{audio ? artist ? t('next.dock.musicPausedIdentity', {title: presentation.identity.title, artist}) : <>{presentation.identity.title}<span className={styles.gap} /><Facts parts={presentation.identity.subtitle} /></> : t('next.dock.saved')}</>}</span>
    {!(phone && audio) && <Button variant="glass" size="dock" onClick={() => dispatchDockAction(binding, action)}>{t(audio ? 'next.dock.resumeMusic' : 'next.dock.resume')}</Button>}
  </div>;
}
