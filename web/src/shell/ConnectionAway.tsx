import {useCallback, useSyncExternalStore} from 'react';
import {Link} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import {StateSentence} from '../kit/states';
import styles from './shell.module.css';

const t = defaultI18n.t;

/** True only when the verified connection itself reports the server away. The
 * shell stays mounted, so the page underneath is never covered or emptied. */
export function useServerAway(api: HttpLocalApi | undefined): boolean {
  const subscribe = useCallback((changed: () => void) => api ? api.subscribeRoute(changed) : () => {}, [api]);
  const snapshot = useCallback(() => api?.getRouteSnapshot()?.phase === 'offline', [api]);
  return useSyncExternalStore(subscribe, snapshot, () => false);
}

/** states.html 4b / desktop-app.html#16: one quiet line under the top bar while
 * the server stops answering. It goes by itself when the connection is back.
 * Only the desktop host offers Downloads; a browser has none, so its line is not
 * interactive (states.html frame 4). Full-screen surfaces hide it (shell.module.css). */
export function ConnectionAway({serverName, downloads = true}: {serverName: string; downloads?: boolean}) {
  return <div className={styles.away} role="status" data-connection-away data-interactive={downloads || undefined}>
    <span className={styles.awayDot} aria-hidden="true" />
    <span><StateSentence messageId="next.states.awayLine" values={{server: serverName}} /></span>
    {downloads && <Link className={styles.awayLink} to="/saved" search={{view: 'downloads'}}>{t('next.shell.downloads')}</Link>}
  </div>;
}
