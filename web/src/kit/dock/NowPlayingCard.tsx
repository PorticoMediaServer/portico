import {useEffect, useId, useLayoutEffect, useRef, type KeyboardEvent, type RefObject} from 'react';
import {Icon} from '../Icon';
import {AmbientArt, Artwork} from '../artwork';
import {Facts, Control, playbackAction, playbackIcon, playbackLabel, useDockActions} from './parts';
import {clockText, positionOf, progressOf} from './model';
import {dockText} from './text';
import {LevelMeter} from './LevelMeter';
import type {DockRendererProps} from './types';
import styles from './dock.module.css';

export type NowPlayingCardProps = DockRendererProps & Readonly<{expanded: boolean; onExpandedChange: (expanded: boolean) => void; returnFocusRef?: RefObject<HTMLElement | null>}>;
/** Web adapter for the TV component; native renderer and device proof live in the native lane. */
export function NowPlayingCard(props: NowPlayingCardProps) {
  const {view, binding, expanded, onExpandedChange, returnFocusRef, t = dockText, className, style} = props, send = useDockActions({view, binding});
  const root = useRef<HTMLDivElement | null>(null), opener = useRef<HTMLButtonElement | null>(null), play = useRef<HTMLButtonElement | null>(null), restore = useRef<HTMLElement | null>(null), id = useId();
  useLayoutEffect(() => {
    const remember = (event: FocusEvent) => { const element = event.target; if (element instanceof HTMLElement && !root.current?.contains(element) && element !== opener.current) restore.current = element; };
    document.addEventListener('focusin', remember); return () => document.removeEventListener('focusin', remember);
  }, []);
  useLayoutEffect(() => {
    if (expanded) play.current?.focus({preventScroll: true});
  }, [expanded]);
  useEffect(() => {
    if (!expanded) return;
    // The owner's ref may be on a parent, attached after this child's layout effects.
    const element = returnFocusRef?.current ?? restore.current;
    return () => { if (element?.isConnected) element.focus({preventScroll: true}); };
  }, [expanded, returnFocusRef]);
  if (view.remote || (view.mode !== 'music' && view.mode !== 'audiobook')) return null;
  const fold = () => { onExpandedChange(false); };
  const keydown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (['ArrowDown', 'Escape', 'BrowserBack', 'GoBack', 'Back'].includes(event.key)) { event.preventDefault(); event.stopPropagation(); fold(); return; }
    if (!['ArrowLeft', 'ArrowRight', 'Tab'].includes(event.key)) return;
    const elements = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')), current = elements.indexOf(document.activeElement as HTMLButtonElement);
    event.preventDefault(); elements[(current + (event.key === 'ArrowLeft' || event.shiftKey ? -1 : 1) + elements.length) % elements.length]?.focus();
  };
  if (!expanded) return <button type="button" ref={opener} className={[styles.tvCard, className].filter(Boolean).join(' ')} data-paused={!view.playback.playing || undefined} style={style} aria-label={t('next.dock.openNowPlaying')} aria-haspopup="dialog" aria-expanded={false} onFocus={() => onExpandedChange(true)} onClick={() => onExpandedChange(true)}>
    <Artwork source={view.presentation.artwork} alt="" className={styles.tvCover} /><span className={styles.tvText}><b>{view.presentation.identity.title}</b><small><Facts parts={view.presentation.identity.subtitle} /></small></span>
    <span className={styles.tvStatus}>{view.playback.playing ? <LevelMeter variant="tv-large" /> : <Icon name="i-pause" size={20} />}</span><span className={styles.tvLine}><i style={{transform: `scaleX(${progressOf(view)})`}} /></span>
  </button>;
  return <><div className={styles.tvScrim} /><div ref={root} role="dialog" aria-modal="true" aria-labelledby={id} className={[styles.tvFlyout, className].filter(Boolean).join(' ')} style={style} onKeyDown={keydown}>
    <AmbientArt source={view.presentation.ambient} palette={view.presentation.palette} treatment="cover" className={styles.tvGlow} /><span className={styles.tvShade} />
    <div className={styles.tvHead}><Artwork source={view.presentation.artwork} alt="" className={styles.tvLargeCover} /><div className={styles.tvHeadText}>
      <p className={styles.tvPlaying}>{view.playback.playing ? <LevelMeter variant="tv-large" /> : <Icon name="i-pause" size={20} />}{t(view.playback.playing ? 'next.dock.playing' : 'next.dock.paused')}</p>
      <h4 id={id}>{view.presentation.identity.title}</h4><p className={styles.tvArtist}><Facts parts={view.presentation.identity.subtitle} /></p>{!!view.presentation.album?.length && <p className={styles.tvAlbum}><Facts parts={view.presentation.album} /></p>}
    </div></div>
    <div className={styles.tvProgress}><i style={{transform: `scaleX(${progressOf(view)})`}} /></div><div className={styles.tvTimes}><span>{clockText(positionOf(view))}</span><span>{clockText(view.presentation.durationSeconds)}</span></div>
    <div className={styles.tvTransport}><Control icon="i-prev" label={view.presentation.previousLabel ?? t(view.mode === 'audiobook' ? 'next.dock.previousChapter' : 'next.dock.previous')} disabled={!view.presentation.controls.includes('previous')} onClick={() => send({kind: 'previous'})} />
      <Control buttonRef={play} primary icon={playbackIcon(view)} label={playbackLabel(view, t)} disabled={!view.presentation.controls.includes(playbackAction(view))} onClick={() => send({kind: playbackAction(view)})} />
      <Control icon="i-next" label={view.presentation.nextLabel ?? t(view.mode === 'audiobook' ? 'next.dock.nextChapter' : 'next.dock.next')} disabled={!view.presentation.controls.includes('next')} onClick={() => send({kind: 'next'})} />
      <Control icon="i-stop" label={t('next.dock.stop')} onClick={() => send({kind: 'stop'})} /><button type="button" className={styles.tvNow} onClick={() => send({kind: 'expand'})}><Icon name="i-zoom" size={28} />{t('next.dock.nowPlaying')}</button></div>
    <p className={styles.tvHint}><kbd>{t('next.dock.backKey')}</kbd>{t('next.dock.fold')}</p>
  </div></>;
}
