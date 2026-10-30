import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {backupProgressFraction, isRestoreRefusalCode} from '@core/administration.ts';
import {Button} from '../../../kit/controls';
import type {BackupSurfaceProps} from './Backups';
import {restoreRecoveryDetails, restoreRecoveryNotice} from '@core/backups.ts';
import {restoreRecoveryMessage, restoreRefusalMessage} from './restore';
import styles from './server.module.css';

const t = defaultI18n.t;
/** Only checked job progress and the server's actual last outcome are presented. */
export function RestoreStatus(props: BackupSurfaceProps) {
  const state = useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);
  const progress = state.document?.restore?.jobId === state.restoreJobId ? state.document?.restore : undefined, result = state.document?.lastRestore;
  const preparing = state.restorePhase === 'preparing';
  const restarting = state.restorePhase === 'restarting';
  const report = preparing || restarting ? (progress?.result ?? state.staged)?.validation.recovery : result?.outcome === 'restored' ? result.recovery : undefined;
  const recovery = restoreRecoveryNotice(report), details = restoreRecoveryDetails(report, t);
  const fraction = preparing && progress && progress.jobId === state.restoreJobId ? backupProgressFraction(progress) : undefined;
  const phase = progress?.phase === 'copy' ? 'web.maintenance.restoreCopy' : progress?.phase === 'integrity' || progress?.phase === 'verify-files' ? 'web.maintenance.restoreIntegrity' : 'server.restore.preparing';
  const reason = result?.reason && isRestoreRefusalCode(result.reason) ? t(restoreRefusalMessage[result.reason]) : undefined;
  const error = state.restoreError === true ? t('error.unconfirmedChange') : state.restoreError ? t(state.restoreEncrypted && state.restoreError === 'restore_integrity_failed' ? 'next.settings.backup.unopened' : restoreRefusalMessage[state.restoreError]) : undefined;
  if (!preparing && !restarting && !result && !error && !recovery) return null;
  return <div className={styles.restoreStatus}>
    {preparing && <div role="status"><b>{t('server.restore.preparing')}</b>{fraction !== undefined && <span className={styles.backupProgress} role="progressbar" aria-label={t('server.restore.preparing')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(fraction * 100)}><i style={{width: `${(fraction * 100).toFixed(1)}%`}} /></span>}<p className={styles.storageNote}>{t(phase)}</p></div>}
    {restarting && <div role="status"><b>{t('web.maintenance.restartingTitle')}</b><p className={styles.storageNote}>{t('web.maintenance.restartingBody')}</p></div>}
    {!preparing && !restarting && result && <p role="status" className={result.outcome === 'rolled_back' ? styles.backupFailure : styles.storageNote}>{t(result.outcome === 'rolled_back' ? 'web.maintenance.restoreRolledBack' : 'web.maintenance.restored')}{result.outcome === 'rolled_back' && reason ? `: ${reason}` : ''}</p>}
    {recovery && <p role="status" className={styles.storageNote}>{t(report?.manifestDamage ? 'web.maintenance.restoreBackupPartial' : restoreRecoveryMessage[recovery])}</p>}
    {details.length > 0 && <ul className={styles.storageNote}>{details.map((detail, index) => <li key={index}>{detail}</li>)}</ul>}
    {error && <p role="alert" className={styles.backupFailure}>{error}</p>}
    {!state.busy && <div><Button size="small" variant="glass" onClick={() => {if (props.isCurrent()) void props.source.load().catch(props.reportError);}}>{t('server.setAside.check')}</Button></div>}
  </div>;
}
