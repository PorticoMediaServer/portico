import {defaultI18n} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import {KEEPING_PRESETS, PREPARED_DOWNLOAD_DAYS} from '@core/server-admin/server-storage.ts';
import {Select} from '../../../kit/controls';
import type {SettingsStorageSource} from './storage-source';
import {useStorage} from './StorageUsage';
import styles from './server.module.css';

const t = defaultI18n.t;

/**
 * Keep generated files and its one child today, Prepared downloads (#3208): the preset writes the prepared-downloads
 * time (Balanced 30, Shorter 14, Longer 90 days); a time that is none of those reads back as Custom from the server.
 */
export function KeepingSelect({source, row, label}: Readonly<{source: SettingsStorageSource; row: SettingsRow; label: string}>) {
  const state = useStorage(source), keeping = state.keeping;
  if (!keeping) return null;
  const options = (row.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), disabled: row.id === 'storage.keepFiles' && option.value === 'custom'}));
  const value = row.id === 'storage.keepFiles' ? keeping.preset : String(keeping.preparedDownloadsDays);
  return <>
    <Select aria-label={label} value={value} disabled={state.saving} options={options} onValueChange={next => {
      if (row.id === 'storage.keepFiles') {if ((KEEPING_PRESETS as readonly string[]).includes(next)) void source.saveKeeping({preset: next as (typeof KEEPING_PRESETS)[number]});}
      else if ((PREPARED_DOWNLOAD_DAYS as readonly number[]).includes(Number(next))) void source.saveKeeping({preparedDownloadsDays: Number(next)});
    }} />
    {state.saveFailed && <span role="alert" className={styles.backupFailure}>{t('next.states.pageFailedBody')}</span>}
  </>;
}
