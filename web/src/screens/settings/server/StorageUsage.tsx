import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {SettingsStorageSource} from './storage-source';
import styles from './server.module.css';

const t = defaultI18n.t;
type RowProps = Readonly<{source: SettingsStorageSource; label: string}>;

function useStorage(source: SettingsStorageSource) {
  useEffect(() => {void source.load();}, [source]);
  return useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
}
function Failure({source}: {source: SettingsStorageSource}) {
  return <p className={styles.storageFailure} role="alert"><span>{t('next.settings.folderFailed')}</span><button type="button" onClick={() => {void source.retry();}}>{t('next.shell.tryAgain')}</button></p>;
}

/** The drawn data-folder folders and the conversion folder outside them, from
 * the server's own layout measurement. No per-row sweep is drawn. */
const folderRows = [
  ['Database', 'database'],
  ['Metadata', 'metadata'],
  ['Cache', 'cache'],
  ['Backups', 'backups'],
  ['Logs', 'logs'],
  ['Keys', 'keys'],
  ['Temp', 'temp'],
] as const;

/** The one data folder, as the server reports it. */
export function StorageDataFolder({source, label}: RowProps) {
  const state = useStorage(source);
  if (state.phase === 'error') return <Failure source={source} />;
  if (state.phase !== 'ready' || !state.report) return <p role="status">{t('next.settings.loading')}</p>;
  return <code className={styles.storagePath} aria-label={label}>{state.report.stateDirectory}</code>;
}

/** What's using space, by the server's own data-folder layout. */
export function StorageUsage({source, label}: RowProps) {
  const state = useStorage(source);
  if (state.phase === 'error') return <Failure source={source} />;
  if (state.phase !== 'ready' || !state.report || !state.usage) return <p role="status">{t('next.settings.loading')}</p>;
  const usage = state.usage;
  // The four facts each row draws, so the conversion folder's measured row can
  // join the data folder's without pretending to be one of its categories.
  const rows: {id: string; name: string; help: string; bytes: number}[] = folderRows.flatMap(([name, key]) => {
    const folder = usage.folders.find(candidate => candidate.name === name);
    return folder ? [{id: name, name: t(`server.space.${key}` as MessageId), help: t(`server.space.${key}Help` as MessageId), bytes: folder.bytes}] : [];
  });
  // The conversion folder is its own measured row, never one of the data
  // folder's categories, and its help names only the path the server reported.
  rows.push({id: 'conversions', name: t('server.space.conversions'), help: usage.transcodeDirectory ? t('server.space.conversionsHelp', {path: usage.transcodeDirectory}) : '', bytes: usage.transcodeBytes});
  const total = rows.reduce((sum, row) => sum + row.bytes, 0);
  return <div className={styles.storage} role="group" aria-label={label}>
    <div className={styles.storageTotal}><b>{t('web.maintenance.totalLabel')}</b><span>{defaultI18n.bytes(total)}</span></div>
    <ul className={styles.storageList}>{rows.map(row => {
      const share = total > 0 ? Math.max(0, Math.min(1, row.bytes / total)) : 0;
      return <li key={row.id}><div className={styles.storageText}><b>{row.name}</b><small>{row.help}</small></div><span className={styles.storageSize}>{defaultI18n.bytes(row.bytes)}</span>
        <span className={styles.storageBar} aria-hidden="true"><i style={{width: `${(share * 100).toFixed(1)}%`}} /></span></li>;
    })}</ul>
  </div>;
}
