import {useRef, type CSSProperties} from 'react';
import {EndControls, Identity, Progress, StartingStop, TerminalActions, Transport} from './parts';
import {remoteTerminal} from './model';
import {dockText} from './text';
import {useDockHandover} from './use-motion';
import {useDockEntry} from './dock-entry';
import type {DockRendererProps} from './types';
import styles from './dock.module.css';

export function DockBar(props: DockRendererProps) {
  const {view, t = dockText, className, style} = props, ref = useRef<HTMLElement | null>(null);
  const identity = `${view.playback.fence.sessionId}:${view.playback.fence.generation}:${view.mode}`;
  useDockEntry(ref, identity);
  useDockHandover(ref, identity);
  return <section ref={ref} className={[styles.bar, className].filter(Boolean).join(' ')} style={{'--dock-glow': view.presentation.palette?.first ?? 'transparent', ...style} as CSSProperties} data-mode={view.mode} aria-label={t('next.dock.nowPlaying')}>
    <div data-dock-identity><Identity {...props} /></div>
    {view.starting ? <><div className={styles.middle} /><StartingStop view={view} t={t} /></>
      : <>{remoteTerminal(view.remote) ? <TerminalActions {...props} /> : <div className={styles.middle}><Transport {...props} /><Progress {...props} /></div>}
        {!remoteTerminal(view.remote) && <EndControls {...props} />}</>}
  </section>;
}
