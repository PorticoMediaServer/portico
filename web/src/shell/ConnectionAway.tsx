import {useCallback, useEffect, useState, useSyncExternalStore} from 'react';
import {Link} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import {StateSentence} from '../kit/states';
import {fullPlaybackSurfaceUp} from '../screens/watch/expanded-focus';
import styles from './shell.module.css';

const t = defaultI18n.t;

/** True only when the verified connection itself reports the server away. The
 * shell stays mounted, so the page underneath is never covered or emptied. */
export function useServerAway(api: HttpLocalApi | undefined): boolean {
  const subscribe = useCallback((changed: () => void) => api ? api.subscribeRoute(changed) : () => {}, [api]);
  const snapshot = useCallback(() => api?.getRouteSnapshot()?.phase === 'offline', [api]);
  return useSyncExternalStore(subscribe, snapshot, () => false);
}

/** A full-screen surface covers the page: a full player (video, music Now Playing, audiobook listening),
 * judged exactly as the player's focus owner judges it, or a reader. */
export function fullScreenSurfaceUp(doc: Pick<Document, 'querySelector' | 'querySelectorAll'>): boolean {
  return fullPlaybackSurfaceUp(doc as Document) || !!doc.querySelector('[data-reader-root]');
}

/** Whether a full-screen surface covers the page. Watched only
 * while asked (the browser's line is up), and read again when the document changes. */
export function useFullScreenSurface(active: boolean, doc: Document | undefined = typeof document === 'undefined' ? undefined : document): boolean {
  const [covered, setCovered] = useState(false);
  useEffect(() => {
    if (!active || !doc) return;
    let frame = 0;
    const view = doc.defaultView;
    const check = () => {frame = 0; setCovered(fullScreenSurfaceUp(doc));};
    const schedule = () => {if (!frame) frame = view?.requestAnimationFrame(check) ?? 0; if (!view) check();};
    check();
    const observer = new MutationObserver(schedule);
    observer.observe(doc.body, {subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'inert', 'aria-hidden', 'class', 'style']});
    return () => {observer.disconnect(); if (frame) view?.cancelAnimationFrame(frame);};
  }, [active, doc]);
  return active && covered;
}

/** states.html 4b / desktop-app.html#16: one quiet line under the top bar while
 * the server stops answering. It goes by itself when the connection is back.
 * Only the desktop host offers Downloads; a browser has none, so its line is not
 * interactive (states.html frame 4) and hides under full-screen surfaces. */
export function ConnectionAway({serverName, downloads = true}: {serverName: string; downloads?: boolean}) {
  // Only the browser's line hides under full-screen surfaces; the desktop host's line is unchanged.
  const covered = useFullScreenSurface(!downloads);
  return <div className={styles.away} role="status" data-connection-away data-interactive={downloads || undefined} hidden={covered || undefined}>
    <span className={styles.awayDot} aria-hidden="true" />
    <span><StateSentence messageId="next.states.awayLine" values={{server: serverName}} /></span>
    {downloads && <Link className={styles.awayLink} to="/saved" search={{view: 'downloads'}}>{t('next.shell.downloads')}</Link>}
  </div>;
}
