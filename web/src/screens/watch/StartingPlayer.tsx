import {useLayoutEffect, useRef} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity, type TextEnvironment} from '@core/presentation/kit-model/index.ts';
import type {WebPlaybackStartDisplay, WebPlaybackStarting} from '../../platform/playback/types';
import type {ShellArtwork} from '../../shell/artwork';
import {Artwork} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import styles from './starting-player.module.css';

const t = defaultI18n.t;
/** A video start is drawn as the full player from the press (J-17): the title's picture, its name
 * and a loading ring, with Close to stop it. Music, audiobooks and channels keep their starting dock. */
export function startsInPlayer(display: WebPlaybackStartDisplay | undefined): boolean {
  return !!display && display.subject.kind === 'video';
}

/** The title's picture behind a start or its failure: the backdrop, else the poster, dimmed. */
export function StartPicture({display, artwork}: {display: WebPlaybackStartDisplay; artwork: ShellArtwork}) {
  const source = display.backdropUrl ?? display.posterUrl;
  return <div className={styles.picture} aria-hidden="true">
    {source && <Artwork source={artwork.foreground(source, 1920, 16 / 9, {group: 'playback', priority: 'high'})} alt="" width="100%" height="100%"/>}
    <div className={styles.veil}/>
  </div>;
}

export function StartingPlayer({starting, artwork, environment}: {starting: WebPlaybackStarting; artwork: ShellArtwork; environment: TextEnvironment}) {
  const root = useRef<HTMLDivElement>(null), restore = useRef<HTMLElement | null>(null);
  const identity = playbackIdentity(starting.display.subject, 'full', environment);
  useLayoutEffect(() => {
    restore.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    root.current?.querySelector<HTMLButtonElement>('button')?.focus({preventScroll: true});
    return () => {if (restore.current?.isConnected && !document.querySelector('[data-watch-expanded]')) restore.current.focus({preventScroll: true});};
  }, [starting.key]);
  return <div ref={root} className={styles.player} role="dialog" aria-modal="true" aria-label={identity.title} aria-busy="true"
    onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); starting.stop();}}}>
    <StartPicture display={starting.display} artwork={artwork}/>
    <div className={styles.bar}>
      <Button variant="glass" icon="i-x" iconOnly aria-label={t('next.dock.stop')} onClick={starting.stop}/>
    </div>
    <div className={styles.copy}>
      <h1>{identity.title}</h1>
      {identity.subtitle.length > 0 && <p>{identity.subtitle.map(part => part.text).join(' · ')}</p>}
      <div className={styles.loading} role="status"><span className={styles.ring} aria-hidden="true"/>{t('next.dock.starting')}</div>
    </div>
  </div>;
}
