import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import {Icon} from '../../../kit/Icon';
import type {SettingsPageSnapshot} from '../Settings';
import type {SettingsBackupSource} from '../backup-source';
import {StorageBackupFolder, StorageBackupNow, StorageBackups, StorageRestoreFile, type BackupSurfaceProps} from './Backups';
import {SettingsCacheSource} from './cache-source';
import {EmptyCache} from './EmptyCache';
import {StorageDataFolder, StorageUsage} from './StorageUsage';
import {BackupProtection} from '../server-storage/BackupProtection';
import {DownloadsUsage} from '../server-storage/DownloadsUsage';
import {DownloadRemovalSource} from '../server-storage/download-removal-source';
import {DownloadsUsageSource} from '../server-storage/downloads-usage-source';
import {SettingsStorageSource, type StorageTransport} from './storage-source';
import {sectionLinkRows, sectionTarget, storageRowState, type SettingsTarget, type StorageRowFacts} from './rows';
import styles from './server.module.css';

const t = defaultI18n.t;

export type {SettingsTarget} from './rows';

export type SettingsServerSurfaces = Readonly<{
  eligible: (row: SettingsRow) => boolean | undefined;
  special: (row: SettingsRow, state: SettingPresentation) => ReactNode | undefined;
  rowState: (row: SettingsRow) => Pick<SettingPresentation, 'present'> | undefined;
  pageSnapshot: (base: SettingsPageSnapshot) => SettingsPageSnapshot;
  subscribe: (listener: () => void) => () => void;
  dispose: () => void;
}>;

export type SettingsServerOptions = Readonly<{
  transport: StorageTransport; serverId: string; isCurrent: () => boolean; openTarget: (target: SettingsTarget) => void;
  /** The controller's own owner-scoped backup reader; it is already loaded for this section. */
  backups: SettingsBackupSource; subscribe: (listener: () => void) => () => void; scopeKey: string; reportError: (error: unknown) => void;
}>;

/** Owner-only Storage facts and controls. One shared sweep feeds both drawn rows. */
export function createServerSettingsSurfaces(options: SettingsServerOptions): SettingsServerSurfaces {
  const cache = new SettingsCacheSource(options.transport, options.isCurrent, options.reportError);
  const downloads = new DownloadsUsageSource(options.transport, options.isCurrent, options.reportError);
  const downloadRemoval = new DownloadRemovalSource(options.transport, downloads, options.isCurrent, options.reportError);
  const storage = new SettingsStorageSource(options.transport, options.serverId, options.isCurrent, options.reportError);
  const backups: BackupSurfaceProps = {source: options.backups, subscribe: options.subscribe, isCurrent: options.isCurrent, scopeKey: options.scopeKey, reportError: options.reportError};
  const facts = (): StorageRowFacts => {
    const snapshot = options.backups.getSnapshot();
    return {live: options.isCurrent(), backups: !!snapshot.settings || !!snapshot.document, dataDirectoryOnNetwork: storage.getSnapshot().report?.stateDirectoryOnNetwork, backupProtectionKnown: typeof snapshot.settings?.passphraseProtected === 'boolean'};
  };
  let cachedPage: {base: SettingsPageSnapshot; facts: ReturnType<typeof storage.getSnapshot>; value: SettingsPageSnapshot} | undefined;
  const pageSnapshot = (base: SettingsPageSnapshot): SettingsPageSnapshot => {
    const facts = storage.getSnapshot();
    if (!cachedPage || cachedPage.base !== base || cachedPage.facts !== facts) cachedPage = {base, facts, value: Object.freeze({...base})};
    return cachedPage.value;
  };
  const linkTarget = (row: SettingsRow) => sectionLinkRows.has(row.id) ? sectionTarget(row.control.to) : undefined;
  return {
    // These rows are drawn here, so this page decides when they exist at all.
    eligible: row => storageRowState(row, facts())?.present,
    special: (row, state) => {
      const target = linkTarget(row), text = row.control.label && t(row.control.label);
      if (target && text) return <button type="button" className={styles.sectionLink} onClick={() => {if (options.isCurrent()) options.openTarget(target);}}>{text}<Icon name="i-right" size={16} /></button>;
      const label = state.label ?? row.label;
      return row.id === 'storage.networkNotice' ? <span className={styles.storageWarning} role="status">{t('next.settings.storage.sharedDrive')}</span>
        : row.id === 'storage.emptyCache' ? <EmptyCache source={cache}/>
        : row.id === 'storage.dataFolder' ? <StorageDataFolder source={storage} label={label} />
        : row.id === 'storage.usage' ? <StorageUsage source={storage} label={label} />
        : row.id === 'storage.downloads' ? <DownloadsUsage source={downloads} removal={downloadRemoval} label={label} />
        : row.id === 'storage.backupKeysNote' ? <span className={styles.storageNote}>{t('next.settings.backup.keysNotice')}</span>
        : row.id === 'storage.backupPassphrase' ? <BackupProtection {...backups} label={label} />
        : row.id === 'storage.backupFolder' ? <StorageBackupFolder {...backups} api={options.transport} />
        : row.id === 'storage.backupNow' ? <StorageBackupNow {...backups} />
        : row.id === 'storage.backups' ? <StorageBackups {...backups} />
        : row.id === 'storage.restoreFile' ? <StorageRestoreFile {...backups} api={options.transport} />
        : undefined;
    },
    rowState: row => storageRowState(row, facts()),
    pageSnapshot,
    subscribe: storage.subscribe,
    dispose: () => {downloadRemoval.dispose();cache.dispose();downloads.dispose();storage.dispose();},
  };
}
