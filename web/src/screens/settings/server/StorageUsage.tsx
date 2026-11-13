import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {storageUsageLines, type StorageUsageLine} from '@core/server-admin/server-storage.ts';
import type {SettingsStorageSource} from './storage-source';
import styles from './server.module.css';

const t = defaultI18n.t;
type RowProps = Readonly<{source: SettingsStorageSource; label: string}>;

export function useStorage(source: SettingsStorageSource) {
  useEffect(() => {void source.load();}, [source]);
  return useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
}
function Failure({source}: {source: SettingsStorageSource}) {
  return <p className={styles.storageFailure} role="alert"><span>{t('next.settings.folderFailed')}</span><button type="button" onClick={() => {void source.retry();}}>{t('next.shell.tryAgain')}</button></p>;
}

/** The data folder's parts as the server names them (#3300), in the page's words. */
const partNames: Readonly<Record<string, readonly [MessageId, MessageId]>> = {
  database: ['server.space.database', 'server.space.databaseHelp'], artwork: ['server.space.artwork', 'server.space.artworkHelp'],
  conversions: ['server.space.conversions', 'server.space.conversionsInside'], logs: ['server.space.logs', 'server.space.logsHelp'],
  backups: ['server.space.backups', 'server.space.backupsHelp'], reports: ['server.space.reports', 'server.space.reportsHelp'],
  'title-data': ['server.space.titleData', 'server.space.titleDataHelp'], keys: ['server.space.keys', 'server.space.keysHelp'],
  metadata: ['server.space.metadata', 'server.space.metadataHelp'], cache: ['server.space.cache', 'server.space.cacheHelp'],
  temp: ['server.space.temp', 'server.space.tempHelp'], 'other-files': ['server.space.other', 'server.space.otherHelp'],
};
function words(line: StorageUsageLine): {name: string; help: string} {
  if (line.kind !== 'part') return {name: t(line.kind === 'conversions' ? 'server.space.conversions' : 'server.space.recordings'),
    help: [line.folder, line.external ? t('server.space.external') : '', line.problem ? t('server.space.unmeasured', {problem: line.problem}) : ''].filter(Boolean).join(' · ')};
  const known = partNames[line.name];
  return known ? {name: t(known[0]), help: t(known[1])} : {name: line.name, help: ''};
}

/** The one data folder, as the server reports it. */
export function StorageDataFolder({source, label}: RowProps) {
  const state = useStorage(source);
  if (state.phase === 'error') return <Failure source={source} />;
  if (!state.storage) return <p role="status">{t('next.settings.loading')}</p>;
  return <code className={styles.storagePath} aria-label={label}>{state.storage.dataFolder}</code>;
}

/** What's using space: the data folder's parts, then conversion and recording places outside it (rule: never counted twice). */
export function StorageUsage({source, label}: RowProps) {
  const state = useStorage(source);
  if (state.phase === 'error') return <Failure source={source} />;
  if (!state.storage) return <p role="status">{t('next.settings.loading')}</p>;
  const lines = storageUsageLines(state.storage), total = lines.reduce((sum, line) => sum + (line.bytes ?? 0), 0);
  return <div className={styles.storage} role="group" aria-label={label}>
    <div className={styles.storageTotal}><b>{t('web.maintenance.totalLabel')}</b><span>{defaultI18n.bytes(total)}</span></div>
    <ul className={styles.storageList}>{lines.map(line => {
      const {name, help} = words(line), share = total > 0 && line.bytes !== undefined ? Math.max(0, Math.min(1, line.bytes / total)) : 0;
      return <li key={line.id}><div className={styles.storageText}><b>{name}</b>{help && <small>{help}</small>}</div>
        <span className={styles.storageSize}>{line.bytes === undefined ? t('server.space.sizeUnknown') : defaultI18n.bytes(line.bytes)}</span>
        <span className={styles.storageBar} aria-hidden="true"><i style={{width: `${(share * 100).toFixed(1)}%`}} /></span></li>;
    })}</ul>
  </div>;
}
