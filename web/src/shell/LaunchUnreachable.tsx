import {useCallback, useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../kit/states';
import type {LaunchUnreachable} from './bootstrap';
import {retryProgress, retrySeconds} from './connection-retry';
import {useShellWidth} from './viewport';
import {useOfflineDownloads} from './offline-downloads';
import wordmark from './wordmark.svg';
import bootStyles from './boot.module.css';
import styles from './states.module.css';

const t = defaultI18n.t;

/** states.html#3. The server the saved sign-in points at is not answering. The
 * page waits on the bootstrap's own retries, so the countdown is the real one
 * and Try again now asks immediately. Identity, access and version failures
 * never reach this page; they keep the ordinary error path. */
export function LaunchUnreachableScreen({facts, onRetry, onSwitchServer, now: fixedNow}: {
  facts: LaunchUnreachable;
  onRetry: () => void;
  onSwitchServer: () => void;
  /** Test seam only; the app always reads the real clock. */
  now?: number;
}) {
  const width = useShellWidth(), [tick, setTick] = useState(() => Date.now());
  const offlinePort = useOfflineDownloads(), [offline, setOffline] = useState(() => offlinePort?.snapshot()), [playing, setPlaying] = useState(() => offlinePort?.playback?.());
  // One stable callback: an inline ref would release and retire the host on every render.
  const attachSurface = offlinePort?.attachSurface;
  const surfaceRef = useCallback((element: HTMLVideoElement | null) => { attachSurface?.(element); }, [attachSurface]);
  useEffect(() => {
    if (!offlinePort) { setOffline(undefined); setPlaying(undefined); return; }
    const sync = () => { setOffline(offlinePort.snapshot()); setPlaying(offlinePort.playback?.()); };
    sync();
    return offlinePort.subscribe(sync);
  }, [offlinePort]);
  const now = fixedNow ?? tick, waiting = !facts.retrying;
  const computerOffline = facts.category === 'offline' || globalThis.navigator?.onLine === false;
  useEffect(() => {
    if (!waiting || fixedNow !== undefined) return;
    const timer = setInterval(() => setTick(Date.now()), 250);
    return () => clearInterval(timer);
  }, [waiting, fixedNow]);
  return <>
    {offline && offline.items.length > 0 && <section className={styles.offlineDownloads} data-offline-downloads aria-label={t('next.shell.downloads')}>
      <h2>{t('next.shell.downloads')}</h2>
      <ul>{offline.items.map(row => <li key={row.id} data-playing={playing?.phase === 'playing' && playing.itemId === row.itemId || undefined}><b>{row.title}</b>{row.artist ? <span>{row.artist}</span> : null}
        {offlinePort?.play ? <button type="button" onClick={() => { void offlinePort.play?.(row.id).catch(() => {}); }}>{t('next.controls.play')}</button> : null}</li>)}</ul>
      <button type="button" onClick={() => { void offlinePort?.openFolder().catch(() => {}); }}>{t('next.saved.folder')}</button>
    </section>}
    {offlinePort?.attachSurface && <div className={styles.offlinePlayer} data-offline-playback data-offline-playing={playing?.phase === 'playing' || undefined} data-media-surface data-native-video-active="true">
      <video ref={surfaceRef} playsInline preload="none" aria-label={t('next.dock.nowPlaying')} />
    </div>}
    <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: width < 600 ? 'top' : 'side'}} tone="warning" icon="i-srvoff"
      chrome={<img className={bootStyles.stateBrand} src={wordmark} alt={t('next.platform.appName')}/>}
      contentStyle={{top: width >= 1024 ? 500 : 250}}
      title={computerOffline ? t('next.boot.offline') : facts.serverName ? t('next.states.cantReach', {server: facts.serverName}) : t('next.states.cantReachServer')}
      retry={facts.retrying ? {progress: null, label: t('next.states.retrying')} : {progress: retryProgress(facts.startedAt, facts.nextRetryAt, now), label: t('next.states.retryIn', {seconds: retrySeconds(facts.nextRetryAt, now)})}}
      primary={{id: 'retry', label: t('next.states.tryNow'), icon: 'i-retry', onPress: onRetry, loading: facts.retrying}}
      secondary={width >= 600 ? {id: 'switch', label: t('next.states.switchServer'), icon: 'i-server', onPress: onSwitchServer} : undefined}
      links={width < 600 ? [{id: 'switch', label: t('next.states.switchServer'), onPress: onSwitchServer}] : undefined} />
  </>;
}
