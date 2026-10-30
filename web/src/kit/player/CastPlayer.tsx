import {defaultI18n} from '@i18n';
import {AmbientArt, Artwork} from '../artwork';
import {Button, Slider} from '../controls';
import {Icon} from '../Icon';
import {Dialog} from '../overlays';
import {Facts, Control} from '../dock/parts';
import {clockText} from '../dock/model';
import type {CastPlayerProps} from './types';
import styles from './cast-player.module.css';

/** The phone-player 7b remote. No receiver volume, playback info or queue surface. */
export function CastPlayer({controller, snapshot, visuals, phone, returnFocusRef, t = defaultI18n.t}: CastPlayerProps) {
  const remote = snapshot.remote;
  if (!remote || snapshot.disposed) return null;
  const bound = remote.itemId && visuals.itemId === remote.itemId ? visuals : undefined;
  const open = snapshot.panel === 'remote' && remote.phase === 'playing';
  const position = remote.positionKnown ? clockText(remote.positionSeconds) ?? '' : '', remaining = clockText(Math.max(0, remote.durationSeconds - remote.positionSeconds)) ?? '';
  const back = visuals.skipBackSeconds, forward = visuals.skipForwardSeconds;
  return <Dialog open={open} onOpenChange={() => controller.closePanel()} title={t('next.dock.castControls')} showCloseButton={false} responsive={false}
    returnFocusRef={returnFocusRef} className={styles.remoteDialog} bodyClassName={styles.remoteBody}>
    <div className={styles.remote} data-phone={phone || undefined}>
      <AmbientArt source={bound?.ambient} palette={bound?.palette} treatment="cover" className={styles.ambient} /><div className={styles.shade} />
      <div className={styles.remoteContent}><header className={styles.remoteHead}>
        <Control icon="i-down" label={t('next.dock.close')} onClick={() => controller.closePanel()} />
        <span><small>{t('next.cast.playingOn')}</small><b>{remote.deviceName}</b></span>
        {bound?.onMore && <Control icon="i-more" label={t('next.controls.more')} onClick={bound.onMore} />}
      </header>
      <Artwork source={bound?.artwork} alt={bound?.identity?.title ?? remote.title ?? ''} className={styles.remoteArt} />
      <div className={styles.remoteIdentity}><div><b>{bound?.identity?.title ?? remote.title}</b>{bound?.identity?.subtitle && <span><Facts parts={bound.identity.subtitle} /></span>}</div>
        {bound?.onMyList && <Control icon="i-plus" label={t('entry.addToWatchlist')} onClick={bound.onMyList} />}</div>
      <div className={styles.remoteScrub}><Slider variant="player" value={remote.positionSeconds} min={0} max={remote.durationSeconds || 1} step={1} disabled={!remote.durationSeconds}
        aria-label={t('next.dock.remotePosition')} aria-valuetext={t('next.dock.positionOf', {position, duration: clockText(remote.durationSeconds) ?? ''})} onValueChange={seconds => controller.seek(remote, seconds)} />
        <div className={styles.remoteTimes}><span>{position}</span><span>{remote.durationSeconds ? '-' + remaining : ''}</span></div></div>
      <div className={styles.remoteTransport}>
        <Control icon="i-rew" iconSize={38} skip={back} disabled={!remote.positionKnown || !back} label={t('next.dock.back', {seconds: back})} onClick={() => controller.seek(remote, Math.max(0, remote.positionSeconds - back))} />
        <Control primary disabled={!remote.positionKnown} icon={remote.paused ? 'i-play' : 'i-pause'} iconSize={34} label={t(remote.paused ? 'next.dock.play' : 'next.dock.pause')} onClick={() => controller.toggle(remote)} />
        <Control icon="i-fwd" iconSize={38} skip={forward} disabled={!remote.positionKnown || !forward} label={t('next.dock.forward', {seconds: forward})} onClick={() => controller.seek(remote, remote.positionSeconds + forward)} />
      </div>
      <div className={styles.remoteActions}>
        {!!snapshot.cast.textTracks.length && <button type="button" onClick={() => controller.openTracks(remote)}><Icon name="d-cc" size={16} />{t('next.cast.subtitles')}</button>}
        {!!snapshot.cast.audioTracks.length && <button type="button" onClick={() => controller.openTracks(remote)}><Icon name="i-audio" size={16} />{t('next.cast.audio')}</button>}
        <button type="button" onClick={() => controller.seek(remote, 0)}><Icon name="i-restart" size={16} />{t('next.controls.startOver')}</button>
        <button type="button" onClick={() => controller.stop(remote)}><Icon name="i-stop" size={15} />{t('next.dock.stop')}</button>
      </div><p className={styles.volumeNote}>{t('next.cast.volumeNote')}</p></div>
    </div>
  </Dialog>;
}

export function CastTracks({controller, snapshot, returnFocusRef, t = defaultI18n.t}: CastPlayerProps) {
  const remote = snapshot.remote;
  if (!remote) return null;
  return <Dialog open={snapshot.panel === 'tracks' && remote.phase === 'playing'} onOpenChange={() => controller.closePanel()} title={t('next.dock.subtitlesAudio')} returnFocusRef={returnFocusRef}>
    {!!snapshot.cast.textTracks.length && <section className={styles.trackGroup}><h3>{t('next.cast.subtitles')}</h3>
      <button type="button" onClick={() => controller.subtitles(remote, null)}>{t('next.controls.off')}</button>
      {snapshot.cast.textTracks.map(track => <button type="button" key={track.id} onClick={() => controller.subtitles(remote, track.id)}>{track.name || track.language}</button>)}
    </section>}
    {!!snapshot.cast.audioTracks.length && <section className={styles.trackGroup}><h3>{t('next.cast.audio')}</h3>
      {snapshot.cast.audioTracks.map(track => <button type="button" key={track.id} onClick={() => controller.audio(remote, track.id)}>{track.name || track.language}</button>)}
    </section>}
  </Dialog>;
}

export function CastTakeover({controller, snapshot, returnFocusRef, t = defaultI18n.t}: CastPlayerProps) {
  const takeover = snapshot.cast.takeover;
  return <Dialog open={!!takeover} onOpenChange={() => controller.answerTakeover(false,takeover)} title={t('cast.takeover.title')} showCloseButton={false} returnFocusRef={returnFocusRef}
    description={takeover ? t('cast.takeover.body', {requester: takeover.requester}) : undefined} footer={<>
      <Button variant="glass" onClick={() => controller.answerTakeover(false,takeover)}>{t('cast.takeover.deny')}</Button>
      <Button variant="primary" onClick={() => controller.answerTakeover(true,takeover)}>{t('action.allow')}</Button>
    </>}>{null}</Dialog>;
}
