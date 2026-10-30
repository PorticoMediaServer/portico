import {Fragment, useId, type RefObject} from 'react';
import type {FactPart} from '@core/presentation/kit-model/index.ts';
import {Icon, type IconName} from '../Icon';
import {Artwork} from '../artwork';
import {Slider} from '../controls/Slider';
import {Button} from '../controls/Button';
import {clockText, commandFor, dispatchDockAction, positionOf, progressOf, remoteReady, remoteTerminal, seekBounds, seekTarget} from './model';
import {dockText, remoteText} from './text';
import type {DockIntent, DockMessageId, DockRendererProps, DockTranslate, DockView} from './types';
import styles from './dock.module.css';

export function Facts({parts}: {parts: readonly FactPart[]}) {
  return <>{parts.map((part, index) => <Fragment key={part.id}>{index > 0 && <span className={styles.gap} aria-hidden="true" />}<span data-fact-role={part.role}>{part.text}</span></Fragment>)}</>;
}
export function Control({icon, label, onClick, disabled, primary, active, skip, iconSize, className, buttonRef}: {icon: IconName; label: string; onClick: () => void; disabled?: boolean; primary?: boolean; active?: boolean; skip?: number; iconSize?: number; className?: string; buttonRef?: RefObject<HTMLButtonElement | null>}) {
  return <button type="button" ref={buttonRef} className={[styles.control, primary && styles.primary, skip !== undefined && styles.skip, className].filter(Boolean).join(' ')} aria-label={label} title={label} disabled={disabled} aria-pressed={active} data-on={active || undefined} onClick={onClick}>
    <Icon name={icon} size={iconSize ?? (skip !== undefined ? 26 : primary ? 22 : 20)} />{skip !== undefined && <span aria-hidden="true">{skip}</span>}
  </button>;
}
export function useDockActions({view, binding}: DockRendererProps) {
  return (action: DockIntent) => dispatchDockAction(binding, commandFor(view, action));
}
export const playbackLabel = (view: DockView, t: DockTranslate) => t(view.presentation.finished ? 'next.dock.replay' : view.playback.playing ? 'next.dock.pause' : 'next.dock.play');
export const playbackIcon = (view: DockView): IconName => view.presentation.finished ? 'i-back' : view.playback.playing ? 'i-pause' : 'i-play';
export const playbackAction = (view: DockView) => view.presentation.finished ? 'replay' as const : view.playback.playing ? 'pause' as const : 'play' as const;
export function ArtSlot({view, artSlotRef, videoSurface = false, artworkSurface = false, phone = false}: Pick<DockRendererProps, 'view' | 'artSlotRef' | 'videoSurface' | 'artworkSurface'> & {phone?: boolean}) {
  // A starting video has no media surface yet: it shows the pressed card's art under the ring.
  const video = ['video', 'live', 'cast', 'play-on'].includes(view.mode) && !view.starting;
  if (video && !view.remote && !videoSurface) throw new TypeError('Local video dock requires its persistent media surface.');
  return <span ref={artSlotRef} data-dock-art-slot data-external-artwork={artworkSurface && !view.remote && view.mode === 'music' || undefined} className={[styles.artSlot, video && styles.videoArt].filter(Boolean).join(' ')}>
    {!(videoSurface && !view.remote && video || artworkSurface && !view.remote && view.mode === 'music') && <Artwork source={view.presentation.artwork} alt="" className={styles.cover} brightness={view.remote && (!phone && !remoteTerminal(view.remote) || phone && remoteTerminal(view.remote)) ? phone ? .55 : .62 : undefined} saturation={view.remote && (!phone && !remoteTerminal(view.remote) || phone && remoteTerminal(view.remote)) ? phone ? .4 : .8 : undefined} />}
    {view.starting ? <span className={styles.castMark} data-pending><span className={styles.ring} /></span>
      : view.remote ? (phone || !remoteTerminal(view.remote)) && <span className={styles.castMark} data-pending={!remoteReady(view.remote) && !remoteTerminal(view.remote) || undefined}>{remoteReady(view.remote) || remoteTerminal(view.remote) ? <Icon name={phone ? 'i-tvdev' : 'i-cast'} size={phone ? 18 : 24} /> : <span className={styles.ring} />}</span>
      : video && <span className={styles.expandMark}><Icon name="i-zoom" size={phone ? 12 : 22} /></span>}
  </span>;
}
export function Identity({view, binding, t = dockText, artSlotRef, identityRef, videoSurface, artworkSurface, phone = false}: DockRendererProps & {phone?: boolean}) {
  const send = useDockActions({view, binding}), terminal = remoteTerminal(view.remote), pending = view.remote && !remoteReady(view.remote) || !!view.starting;
  const accessibleId = useId(), actionId = `${accessibleId}-action`, titleId = `${accessibleId}-title`, subtitleId = `${accessibleId}-subtitle`;
  const subtitle: readonly FactPart[] = view.presentation.finished ? [...view.presentation.identity.subtitle.filter(part => part.id === 'episode-label'), {id: 'finished', role: 'status', text: t('next.dock.finished')}] : phone && view.mode === 'music' && !view.playback.playing ? [...view.presentation.identity.subtitle, {id: 'paused', role: 'status', text: t('next.dock.paused')}] : view.presentation.identity.subtitle;
  const content = <><ArtSlot view={view} artSlotRef={artSlotRef} videoSurface={videoSurface} artworkSurface={artworkSurface} phone={phone} /><span className={styles.identityText} data-dock-expansion-copy><b id={titleId}>{view.presentation.identity.title}</b>
    <small id={subtitleId} data-status={view.remote?.phase}>{phone && view.remote?.phase === 'playing' ? <>{view.presentation.deviceName}<span className={styles.gap} aria-hidden="true" />{clockText(positionOf(view))}</> : phone && view.remote?.phase === 'stopped' ? t('next.dock.stoppedAt', {position: clockText(positionOf(view)) ?? ''}) : view.starting ? (view.starting.waiting === 'channel_source' ? t('next.dock.channelSourceWaiting') : t('next.dock.starting')) : remoteText(view, t) ?? <Facts parts={subtitle} />}</small></span></>;
  return <>{terminal || pending ? <div className={styles.identity}>{content}</div> : <button ref={identityRef} type="button" className={styles.identity} aria-labelledby={`${actionId} ${titleId}`} aria-describedby={subtitleId} onClick={() => send({kind: 'expand'})}>
    <span id={actionId} className={styles.accessible}>{t(view.remote ? 'next.dock.castControls' : view.mode === 'music' || view.mode === 'audiobook' ? 'next.dock.openNowPlaying' : 'next.dock.expand')}</span>{content}<span className={styles.expandTip} role="tooltip">{t('next.dock.expand')}</span></button>}
    <span className={styles.accessible} role="status" aria-atomic="true">{remoteText(view, t)}</span>
  </>;
}
export function Transport({view, binding, t = dockText, phone = false}: DockRendererProps & {phone?: boolean}) {
  const send = useDockActions({view, binding}), model = view.presentation, can = (key: typeof model.controls[number]) => model.controls.includes(key) && remoteReady(view.remote);
  const skip = (delta: number) => { const seconds = seekTarget(view, positionOf(view) + delta); if (seconds !== null) send({kind: 'seek', seconds}); };
  const book = view.mode === 'audiobook', video = view.mode === 'video' || view.mode === 'live';
  const previous = model.previousLabel ?? t(book ? 'next.dock.previousChapter' : video && model.identity.subtitle.some(p => p.id === 'episode-label') ? 'next.dock.previousEpisode' : 'next.dock.previous');
  const next = model.nextLabel ?? t(book ? 'next.dock.nextChapter' : video && model.identity.subtitle.some(p => p.id === 'episode-label') ? 'next.dock.nextEpisode' : 'next.dock.next');
  const playing = playbackAction(view), main = <Control primary={!phone} icon={playbackIcon(view)} iconSize={phone ? 24 : 22} label={playbackLabel(view, t)} disabled={!can(playing)} onClick={() => send({kind: playing})} />;
  if (phone) return <div className={styles.transport} data-dock-expansion-copy>
    {video || view.remote ? <Control icon="i-rew" iconSize={27} label={t('next.dock.back', {seconds: model.skipBackSeconds})} skip={model.skipBackSeconds} disabled={!can('seek')} onClick={() => skip(-model.skipBackSeconds)} /> : <Control icon="i-prev" iconSize={22} label={previous} disabled={!can('previous')} onClick={() => send({kind: 'previous'})} />}
    {main}{view.remote ? <Control icon="i-stop" label={t('next.dock.stop')} onClick={() => send({kind: 'stop'})} /> : <><Control icon="i-next" iconSize={22} label={next} disabled={!can('next')} onClick={() => send({kind: 'next'})} /><Control icon="i-x" iconSize={18} label={t('next.dock.stopClose')} className={styles.close} onClick={() => send({kind: 'stop'})} /></>}
  </div>;
  return <div className={styles.transport} data-dock-expansion-copy>
    {view.mode === 'music' && <Control icon="i-shuffle" label={t('next.dock.shuffle')} active={model.shuffle ?? false} disabled={!can('shuffle')} onClick={() => send({kind: 'shuffle', shuffle: !model.shuffle})} />}
    {!view.remote && <Control icon="i-prev" label={previous} disabled={!can('previous')} onClick={() => send({kind: 'previous'})} />}
    {view.mode !== 'music' && <Control icon="i-back" label={t('next.dock.back', {seconds: model.skipBackSeconds})} skip={model.skipBackSeconds} disabled={!can('seek')} onClick={() => skip(-model.skipBackSeconds)} />}
    {main}
    {view.mode !== 'music' && <Control icon="i-fwd" label={t('next.dock.forward', {seconds: model.skipForwardSeconds})} skip={model.skipForwardSeconds} disabled={!can('seek') || model.finished} onClick={() => skip(model.skipForwardSeconds)} />}
    {!view.remote && <Control icon="i-next" label={next} disabled={!can('next')} onClick={() => send({kind: 'next'})} />}
    {view.mode === 'music' && <Control icon="i-repeat" label={t(model.repeat === 'one' ? 'next.dock.repeatOne' : model.repeat === 'all' ? 'next.dock.repeatAll' : 'next.dock.repeatOff')} active={!!model.repeat && model.repeat !== 'off'} disabled={!can('repeat')} onClick={() => send({kind: 'repeat', repeat: model.repeat === 'off' || !model.repeat ? 'all' : model.repeat === 'all' ? 'one' : 'off'})} />}
  </div>;
}
export function Progress({view, binding, t = dockText, line = false}: DockRendererProps & {line?: boolean}) {
  const send = useDockActions({view, binding}), bounds = seekBounds(view), position = positionOf(view), duration = view.presentation.durationSeconds;
  const current = clockText(position), end = clockText(duration), progress = progressOf(view);
  if (line) return <span className={styles.progressLine} data-dock-expansion-copy aria-hidden="true"><i style={{transform: `scaleX(${progress})`}} /></span>;
  return <div className={styles.scrub} data-dock-expansion-copy data-pending={!remoteReady(view.remote) || undefined}>
    <span>{current}</span><span className={styles.track}><i style={{transform: `scaleX(${progress})`}} />
      {bounds && <input type="range" min={bounds.start} max={bounds.end} step={1} value={Math.min(bounds.end, Math.max(bounds.start, position))} disabled={!view.presentation.controls.includes('seek') || !remoteReady(view.remote)} aria-label={t(view.remote ? 'next.dock.remotePosition' : 'next.dock.position')} aria-valuetext={current && end ? t('next.dock.positionOf', {position: current, duration: end}) : current} onChange={event => { const seconds = seekTarget(view, event.currentTarget.valueAsNumber); if (seconds !== null) send({kind: 'seek', seconds}); }} />}
    </span><span>{end}</span>
  </div>;
}
export function TerminalActions({view, binding, t = dockText, phone = false}: DockRendererProps & {phone?: boolean}) {
  const send = useDockActions({view, binding});
  return <div className={styles.terminalActions}>{view.remote?.phase === 'stopped' && <Button size="dock" variant="primary" icon={phone ? undefined : 'i-play'} aria-label={t('next.dock.continueHere', {position: clockText(positionOf(view)) ?? ''})} onClick={() => send({kind: 'continue-here'})}>{t(phone ? 'next.dock.continue' : 'next.dock.continueHere', {position: clockText(positionOf(view)) ?? ''})}</Button>}
    {phone ? <Control icon="i-x" iconSize={18} label={t('next.dock.dismiss')} className={styles.close} onClick={() => send({kind: 'dismiss'})} /> : <Button size="dock" variant="glass" onClick={() => send({kind: 'dismiss'})}>{t('next.dock.dismiss')}</Button>}
  </div>;
}
/** The starting occupant's only action: it cancels the accepted start itself. */
export function StartingStop({view, t = dockText, phone = false}: Pick<DockRendererProps, 'view' | 't'> & {phone?: boolean}) {
  const starting = view.starting;
  if (!starting) return null;
  return phone ? <Control icon="i-x" label={t('next.dock.stop')} className={styles.close} onClick={starting.stop} />
    : <div className={styles.endControls}><Button size="dock" variant="glass" onClick={starting.stop}>{t('next.dock.stop')}</Button></div>;
}
export function EndControls({view, binding, t = dockText}: DockRendererProps) {
  const send = useDockActions({view, binding}), model = view.presentation;
  const tool = (kind: 'subtitles-audio' | 'options' | 'play-on' | 'lyrics' | 'queue' | 'chapters', icon: IconName, key: DockMessageId) => model.controls.includes(kind) ? <Control icon={icon} label={t(key)} onClick={() => send({kind})} /> : null;
  if (view.remote) return <div className={styles.endControls} data-dock-expansion-copy>{remoteReady(view.remote) && tool('subtitles-audio', 'd-cc', 'next.dock.subtitlesAudio')}
    <Button size="dock" variant="glass" onClick={() => send({kind: 'stop'})}>{t('next.dock.stop')}</Button>{remoteReady(view.remote) && <Control icon="i-up" label={t('next.dock.castControls')} onClick={() => send({kind: 'expand'})} />}</div>;
  return <div className={styles.endControls} data-dock-expansion-copy>
    {view.mode === 'music' && <>{tool('lyrics', 'i-lyrics', 'next.dock.lyrics')}{tool('queue', 'i-queue', 'next.dock.queue')}</>}
    {view.mode === 'audiobook' && <>{model.controls.includes('speed') && model.rate !== undefined && <button className={styles.pill} type="button" aria-label={t('next.dock.speed', {rate: model.rate})} onClick={() => send({kind: 'speed'})}>{t('next.dock.rate', {rate: model.rate})}</button>}
      {model.controls.includes('sleep') && <button className={styles.pill} type="button" aria-label={t('next.dock.sleep')} onClick={() => send({kind: 'sleep'})}><Icon name="i-moon" size={16} />{model.sleepLabel ?? t('next.dock.sleep')}</button>}{tool('chapters', 'i-chapters', 'next.dock.chapters')}</>}
    {(view.mode === 'video' || view.mode === 'live') && <>{tool('subtitles-audio', 'd-cc', 'next.dock.subtitlesAudio')}{tool('options', 'i-sliders', 'next.dock.options')}</>}
    {tool('play-on', 'i-cast', 'next.dock.playOnDevice')}
    {model.controls.includes('volume') && model.volume !== undefined && <div className={styles.volume}><Control icon="i-vol" label={t(model.muted ? 'next.dock.unmute' : 'next.dock.mute')} onClick={() => send({kind: 'mute'})} /><Slider variant="dock" min={0} max={1} step={.01} value={model.muted ? 0 : model.volume} aria-label={t('next.dock.volume')} onValueChange={volume => send({kind: 'volume', volume})} /></div>}
    <Control icon="i-up" label={t('next.dock.expand')} onClick={() => send({kind: 'expand'})} /><Control icon="i-x" label={t('next.dock.stopClose')} onClick={() => send({kind: 'stop'})} />
  </div>;
}
