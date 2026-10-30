import type {CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {Artwork} from '../artwork';
import {Icon} from '../Icon';
import {Button} from '../controls';
import {Control} from '../dock/parts';
import {clockText} from '../dock/model';
import type {CastPlayerProps} from './types';
import dock from '../dock/dock.module.css';

/** Cast has the dock before local video/music. All commands carry the exact remote revision. */
export function CastDock({controller, snapshot, visuals, phone, reportError, t = defaultI18n.t}: CastPlayerProps) {
  const remote = snapshot.remote;
  if (!remote || snapshot.disposed) return null;
  const bound = remote.itemId && visuals.itemId === remote.itemId ? visuals : undefined;
  const playing = remote.phase === 'playing', terminal = ['stopped', 'replaced', 'error'].includes(remote.phase);
  const back = visuals.skipBackSeconds, forward = visuals.skipForwardSeconds;
  const canContinue = remote.confirmed && remote.positionKnown && remote.phase === 'stopped' && !!remote.itemId;
  const position = remote.positionKnown ? clockText(remote.positionSeconds) ?? '' : '', duration = remote.positionKnown ? clockText(remote.durationSeconds) ?? '' : '';
  const status = remote.phase === 'playing' ? t('next.dock.onDevice', {device: remote.deviceName ?? ''})
    : remote.phase === 'waiting' ? t('cast.waitingForAllow') : remote.phase === 'stopped' ? t('next.dock.stopped')
    : remote.phase === 'replaced' ? t('next.dock.replaced') : remote.phase === 'error' ? snapshot.cast.error ?? t('next.dock.remoteError')
    : remote.deviceName ? t('next.dock.connecting', {device: remote.deviceName}) : t('next.dock.chooseDevice');
  const art = <span className={[dock.artSlot, dock.videoArt].join(' ')}><Artwork source={bound?.artwork} alt="" className={dock.cover} brightness={!terminal ? .62 : undefined} />
    {(!terminal || phone) && <span className={dock.castMark} data-pending={!playing && !terminal || undefined}>{playing || terminal ? <Icon name={phone ? 'i-tvdev' : 'i-cast'} size={phone ? 18 : 24} /> : <span className={dock.ring} />}</span>}</span>;
  const identity = <>{art}<span className={dock.identityText}><b>{bound?.identity?.title ?? remote.title}</b><small data-status={remote.phase} aria-live="polite">{phone && playing ? <>{remote.deviceName}<span className={dock.gap} />{position}</> : phone && remote.phase === 'stopped' && remote.positionKnown ? t('next.dock.stoppedAt', {position}) : status}</small></span></>;
  const skip = (delta: number) => controller.seek(remote, Math.max(0, remote.positionSeconds + delta));
  const main = <Control primary={!phone} icon={remote.paused ? 'i-play' : 'i-pause'} label={t(remote.paused ? 'next.dock.play' : 'next.dock.pause')} disabled={!playing || !remote.positionKnown} onClick={() => controller.toggle(remote)} />;
  const terminalActions = <div className={dock.terminalActions}>
    {canContinue && <Button variant="primary" size="dock" icon={phone ? undefined : 'i-play'} onClick={() => { void controller.continueHere(remote).catch(reportError); }} aria-label={t('next.dock.continueHere', {position})}>{t(phone ? 'next.dock.continue' : 'next.dock.continueHere', {position})}</Button>}
    {phone ? <Control icon="i-x" label={t('next.dock.dismiss')} onClick={() => controller.dismiss(remote)} /> : <Button variant="glass" size="dock" onClick={() => controller.dismiss(remote)}>{t('next.dock.dismiss')}</Button>}
  </div>;
  return <section className={phone ? dock.phoneBar : dock.bar} data-mode="cast" aria-label={t('cast.region')} style={{'--dock-glow': bound?.palette?.first ?? 'transparent'} as CSSProperties}>
    <div data-dock-identity className={phone ? dock.phoneIdentity : undefined}>
      {playing ? <button type="button" className={dock.identity} aria-label={t('next.dock.castControls')} onClick={() => controller.openRemote(remote)}>{identity}</button> : <div className={dock.identity}>{identity}</div>}
    </div>
    {terminal ? terminalActions : phone && !playing ? <Control icon="i-stop" label={t('next.dock.stopConnecting')} onClick={() => controller.stop(remote)} /> : <div className={phone ? dock.transport : dock.middle}>
      <div className={dock.transport}><Control icon={phone ? 'i-rew' : 'i-back'} skip={back} label={t('next.dock.back', {seconds: back})} disabled={!playing || !remote.positionKnown || !back} onClick={() => skip(-back)} />{main}
        {phone ? <Control icon="i-stop" label={t(playing ? 'next.dock.stop' : 'next.dock.stopConnecting')} onClick={() => controller.stop(remote)} /> : <Control icon="i-fwd" skip={forward} label={t('next.dock.forward', {seconds: forward})} disabled={!playing || !remote.positionKnown || !forward} onClick={() => skip(forward)} />}</div>
      {!phone && <div className={dock.scrub} style={!playing ? {opacity: .35} : undefined}><span>{position}</span><span className={dock.track}>
        <i style={{transform: `scaleX(${remote.durationSeconds > 0 ? Math.min(1, remote.positionSeconds / remote.durationSeconds) : 0})`}} />
        <input type="range" min={0} max={Math.max(1, remote.durationSeconds)} step={1} value={Math.min(remote.positionSeconds, remote.durationSeconds || 0)} disabled={!playing || !remote.durationSeconds}
          aria-label={t('next.dock.remotePosition')} aria-valuetext={t('next.dock.positionOf', {position, duration})} onChange={event => controller.seek(remote, Number(event.currentTarget.value))} />
      </span><span>{duration}</span></div>}
    </div>}
    {!terminal && !phone && <div className={dock.endControls}>
      {playing && !!(snapshot.cast.audioTracks.length || snapshot.cast.textTracks.length) && <Control icon="d-cc" label={t('next.dock.subtitlesAudio')} onClick={() => controller.openTracks(remote)} />}
      <Button variant="glass" size="dock" onClick={() => controller.stop(remote)}>{t('next.dock.stop')}</Button>
      {playing && <Control icon="i-up" label={t('next.dock.castControls')} onClick={() => controller.openRemote(remote)} />}
    </div>}
    {phone && !terminal && <span className={dock.progressLine} aria-hidden="true"><i style={{transform: `scaleX(${remote.durationSeconds > 0 ? Math.min(1, remote.positionSeconds / remote.durationSeconds) : 0})`}} /></span>}
  </section>;
}
