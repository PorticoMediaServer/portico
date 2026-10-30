import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {DownloadRemoval} from './DownloadRemoval';
import type {DownloadRemovalSource} from './download-removal-source';
import type {DownloadsUsageSource} from './downloads-usage-source';
import styles from '../server/server.module.css';

const t = defaultI18n.t;

/** Authorized names and actual retained bytes; removal uses the explicit owner claims/offline-permissions boundary. */
export function DownloadsUsage({source, label, removal}: Readonly<{source: DownloadsUsageSource; label: string; removal: DownloadRemovalSource}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  useEffect(() => {void source.load();}, [source]);
  const action = useSyncExternalStore(removal.subscribe, removal.getSnapshot, removal.getSnapshot);
  const remaining = action.pending ?? action.continuation;
  const busy = state.phase === 'loading';
  return <div className={styles.storage} role="group" aria-label={label} aria-busy={busy}>
    {state.phase === 'idle' || busy ? <p role="status">{t('next.settings.loading')}</p> : null}
    {state.phase === 'error' ? <p className={styles.storageFailure} role="alert"><span>{t('next.saved.loadFailed')}</span>
      <button type="button" onClick={() => {void source.load('retry');}}>{t('next.shell.tryAgain')}</button></p> : null}
    {state.page && <ul className={styles.storageList}>{state.page.rows.map(row => <li key={row.profileKey}>
      <div className={styles.storageText}><b>{row.profileName || row.accountName || '—'}</b>
        {row.accountName && row.accountName !== row.profileName ? <small>{row.accountName}</small> : null}</div>
      <span className={styles.storageSize}>{defaultI18n.bytes(row.storedBytes)}</span>
      {row.count > 0 && <DownloadRemoval source={removal} row={row} disabled={busy || state.phase !== 'ready'}/>}
    </li>)}</ul>}
    {remaining && !state.page?.rows.some(row => row.profileKey === remaining.profileKey) && <div><b>{remaining.profileName || remaining.accountName || '—'}</b><DownloadRemoval source={removal} row={remaining} disabled={busy || !action.pending && state.phase !== 'ready'}/></div>}
    {action.done && <p role="status">{t('server.downloadUsage.removed')}</p>}
    {state.phase === 'ready' && !state.page?.rows.length ? <p>{t('web.downloads.emptyTitle')}</p> : null}
    <div>
      {state.previous && <Button size="small" disabled={busy} onClick={() => {void source.load('previous');}}>{t('next.reader.previous')}</Button>}
      {state.page?.next && <Button size="small" disabled={busy} onClick={() => {void source.load('next');}}>{t('next.reader.next')}</Button>}
    </div>
  </div>;
}
