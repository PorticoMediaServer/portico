import {defaultI18n} from '@i18n';
import {TrackChoices} from '../media';
import {AmbientArt, Artwork} from '../artwork';
import {Button, Slider} from '../controls';
import {Icon} from '../Icon';
import {Dialog} from '../overlays';
import {Facts, Control} from '../dock/parts';
import {clockText} from '../dock/model';
import type {CastPlayerProps} from './types';
import styles from './cast-player.module.css';

/** The phone-player 7b remote. No receiver volume, playback info or queue surface. */
export function CastPlayer({controller, snapshot, visuals, phone, returnFocusRef, reportError, t = defaultI18n.t}: CastPlayerProps) {
  const remote = snapshot.remote;
  if (!remote || snapshot.disposed) return null;
  const bound = remote.itemId && visuals.itemId === remote.itemId ? visuals : undefined;
  const open = snapshot.panel === 'remote' && remote.phase === 'playing';
  const position = remote.positionKnown ? clockText(remote.positionSeconds) ?? '' : '', remaining = clockText(Math.max(0, remote.durationSeconds - remote.positionSeconds)) ?? '';
  const back = visuals.skipBackSeconds, forward = visuals.skipForwardSeconds;
  // A Portico device's own tracks, next title and Play here (rule 27); a Cast device's receiver tracks.
  const portico = remote.kind === 'portico';
  const subtitles = portico ? !!remote.tracks?.subtitles.length : !!snapshot.cast.textTracks.length, audio = portico ? !!remote.tracks?.audio.length : !!snapshot.cast.audioTracks.length;
  return <Dialog open={open} onOpenChange={() => controller.closePanel()} title={t('next.dock.castControls')} showCloseButton={false} responsive={false}
    returnFocusRef={returnFocusRef} className={phone ? `${styles.remoteDialog} ${styles.remotePhone}` : styles.remoteDialog} bodyClassName={styles.remoteBody}>
    <div className={styles.remote} data-phone={phone || undefined}>
      <AmbientArt source={bound?.ambient} palette={bound?.palette} treatment="cover" className={styles.ambient} /><div className={styles.shade} />
      <div className={styles.remoteContent}><header className={styles.remoteHead}>
        <Control icon="i-down" label={t('next.dock.close')} onClick={() => controller.closePanel()} />
        <span><small>{t('next.cast.playingOn')}</small><b>{remote.deviceName}</b></span>
        {bound?.onMore && <Control icon="i-more" label={t('next.controls.more')} onClick={bound.onMore} />}
      </header>
      <span className={styles.remoteArt}><Artwork source={bound?.artwork} alt={bound?.identity?.title ?? remote.title ?? ''} /></span>
      <div className={styles.remoteIdentity}><div><b>{bound?.identity?.title ?? remote.title}</b>{bound?.identity?.subtitle && <span><Facts parts={bound.identity.subtitle} /></span>}</div>
        {bound?.onMyList && <Control icon="i-plus" label={t('entry.addToWatchlist')} onClick={bound.onMyList} />}</div>
      <div className={styles.remoteScrub}><Slider variant="player" value={remote.positionSeconds} min={0} max={remote.durationSeconds || 1} step={1} disabled={!remote.durationSeconds}
        aria-label={t('next.dock.remotePosition')} aria-valuetext={t('next.dock.positionOf', {position, duration: clockText(remote.durationSeconds) ?? ''})} onValueChange={seconds => controller.seek(remote, seconds)} />
        <div className={styles.remoteTimes}><span>{position}</span><span>{remote.durationSeconds ? '-' + remaining : ''}</span></div></div>
      <div className={styles.remoteTransport}>
        <Control icon="i-rew" iconSize={38} skip={back} disabled={!remote.positionKnown || !back} label={t('next.dock.back', {seconds: back})} onClick={() => controller.seek(remote, Math.max(0, remote.positionSeconds - back))} />
        <Control primary disabled={!remote.positionKnown} icon={remote.paused ? 'i-play' : 'i-pause'} iconSize={34} label={t(remote.paused ? 'next.dock.play' : 'next.dock.pause')} onClick={() => controller.toggle(remote)} />
        <Control icon="i-fwd" iconSize={38} skip={forward} disabled={!remote.positionKnown || !forward} label={t('next.dock.forward', {seconds: forward})} onClick={() => controller.seek(remote, remote.positionSeconds + forward)} />
        {portico && remote.next && <Control icon="i-next" iconSize={30} label={t('next.dock.next')} onClick={() => controller.next(remote)} />}
      </div>
      <div className={styles.remoteActions}>
        {subtitles && <button type="button" onClick={() => controller.openTracks(remote)}><Icon name="d-cc" size={16} />{t('next.cast.subtitles')}</button>}
        {audio && <button type="button" onClick={() => controller.openTracks(remote)}><Icon name="i-audio" size={16} />{t('next.cast.audio')}</button>}
        {portico && remote.itemId && remote.positionKnown && <button type="button" onClick={() => { void controller.playHere(remote).catch(reportError); }}><Icon name="i-play" size={16} />{t('next.dock.playHere')}</button>}
        <button type="button" onClick={() => controller.seek(remote, 0)}><Icon name="i-restart" size={16} />{t('next.controls.startOver')}</button>
        <button type="button" onClick={() => controller.stop(remote)}><Icon name="i-stop" size={15} />{t('next.dock.stop')}</button>
      </div><p className={styles.volumeNote}>{t('next.cast.volumeNote')}</p></div>
    </div>
  </Dialog>;
}

export function CastTracks({controller, snapshot, returnFocusRef, reportError, t = defaultI18n.t}: CastPlayerProps) {
  const remote = snapshot.remote;
  if (!remote) return null;
  // The player's own track rows (kit TrackChoices), for a Cast device or one of the account's Portico devices, with the
  // ones in use checked where they are known.
  const portico = remote.kind === 'portico' ? remote.tracks : undefined;
  const row = (id: string, name: string, selected: boolean) => ({id, primary: name, fullName: name, selected});
  const text = portico ? portico.subtitles.map(track => row(track.id, track.name, portico.subtitleId === track.id)) : snapshot.cast.textTracks.map(track => row(String(track.id), track.name || track.language, false));
  const audio = portico ? portico.audio.map(track => row(track.id, track.name, portico.audioId === track.id)) : snapshot.cast.audioTracks.map(track => row(String(track.id), track.name || track.language, false));
  const id = (value: string) => portico ? value : Number(value);
  return <Dialog open={snapshot.panel === 'tracks' && remote.phase === 'playing'} onOpenChange={() => controller.closePanel()} title={t('next.dock.subtitlesAudio')} returnFocusRef={returnFocusRef}>
    {!!text.length && <TrackChoices title={t('next.cast.subtitles')} reportError={reportError} t={t}
      choices={[row('off', t('next.controls.off'), portico?.subtitleId === null), ...text]} onSelect={value => controller.subtitles(remote, value === 'off' ? null : id(value))} />}
    {!!audio.length && <TrackChoices title={t('next.cast.audio')} reportError={reportError} t={t} choices={audio} onSelect={value => controller.audio(remote, id(value))} />}
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
