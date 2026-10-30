import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import type {SettingsUpdatesSource} from './updates-source';
import styles from './server.module.css';

const t = defaultI18n.t;

/** General › Updates, rows "Portico Server" and "Latest version" (owner-server):
 * the build this server runs and the pressed check, both from one report. The
 * read never contacts the feed; only Check now does, on the owner's press. */
export function UpdatesCurrent({source}: {source: SettingsUpdatesSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const report = state.report;
  if (!report) return <span className={styles.updatesValue}>{state.error === 'read' ? t('error.unavailable') : t('next.settings.loading')}</span>;
  return <span className={styles.updatesValue}>{report.current.version || '—'}</span>;
}

export function UpdatesCheckNow({source, label, disabled}: {source: SettingsUpdatesSource; label: string; disabled?: boolean}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const report = state.report;
  const latest = report?.status === 'update-available' ? report.latest?.version : undefined;
  const line = !report ? (state.error === 'read' ? t('error.unavailable') : '') : report.message || latest || '';
  const checked = report?.checkedAt ? t('web.servers.checked', {when: defaultI18n.dateTime(report.checkedAt)}) : '';
  const failed = state.error === 'check' ? t('error.unavailable') : '';
  return <div className={styles.updatesAction}>
    {line ? <span className={styles.updatesValue}>{line}</span> : null}
    {checked ? <span className={styles.updatesValue}>{checked}</span> : null}
    {failed ? <span className={styles.updatesValue} role="alert">{failed}</span> : null}
    <Button size="small" variant="glass" loading={state.busy} disabled={disabled || state.busy} onClick={() => {void source.check();}}>{label}</Button>
  </div>;
}
