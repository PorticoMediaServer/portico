import {defaultI18n} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import {KEEPING_PRESETS} from '@core/server-admin/server-storage.ts';
import {Select} from '../../../kit/controls';
import type {SettingsStorageSource} from './storage-source';
import {useStorage} from './StorageUsage';
import styles from './server.module.css';

const t = defaultI18n.t;

/** Keep generated files (#3208): Balanced, Shorter or Longer; a server value that is none of them reads as Custom. */
export function KeepingSelect({source, row, label}: Readonly<{source: SettingsStorageSource; row: SettingsRow; label: string}>) {
  const state = useStorage(source), keeping = state.keeping;
  if (!keeping) return null;
  const options = (row.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), disabled: option.value === 'custom'}));
  return <>
    <Select aria-label={label} value={keeping.preset} disabled={state.saving} options={options} onValueChange={next => {
      if ((KEEPING_PRESETS as readonly string[]).includes(next)) void source.saveKeeping({preset: next as (typeof KEEPING_PRESETS)[number]});
    }} />
    {state.saveFailed && <span role="alert" className={styles.backupFailure}>{t('next.states.pageFailedBody')}</span>}
  </>;
}
