import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {securityBlockLine, securitySummaryLine, type SecuritySummarySource} from '@core/server-admin/security-summary.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import styles from './server-remote.module.css';

export {SecuritySummarySource} from '@core/server-admin/security-summary.ts';
const t = defaultI18n.t;
const K = 'next.settingsSpec.server.remote.groups.2.rows.1';
const words = {t: defaultI18n.t as never, time: defaultI18n.time, dateTime: defaultI18n.dateTime};

/** Security: last 7 days of refused connections, failed sign-ins and blocks, as one quiet line. */
export function SecuritySummaryLine({source}: Readonly<{source: SecuritySummarySource}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  return <span className={styles.securityLine} role="status">{state.summary ? securitySummaryLine(state.summary, words) : state.failed ? t('next.remoteStatus.failed') : ''}</span>;
}

/** Blocked: each network the server refuses right now, with Unblock. */
export function BlockedSourcesControl({source, disabled}: Readonly<{source: SecuritySummarySource; disabled?: boolean}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const blocks = state.summary?.blocks ?? [];
  return <>
    {!blocks.length ? <p className={styles.refusedEmpty} role="status">{state.unblocked ? t(`${K}.children.0.control.itemAction.result`) : t(`${K}.children.0.control.empty`)}</p>
      : <ul className={styles.refused} aria-label={t(`${K}.children.0.label`)}>
        {blocks.map(block => <li key={block.id} className={styles.refusedItem}>
          <span className={styles.refusedIcon} aria-hidden="true"><Icon name="i-shield" size={20}/></span>
          <span className={styles.refusedText}><strong>{block.network}</strong><small>{securityBlockLine(block, words)}</small></span>
          <Button size="small" variant="glass" disabled={disabled || !!state.unblocking} loading={state.unblocking === block.id} onClick={() => {void source.unblock(block.id);}}>{t(`${K}.children.0.control.itemAction.label`)}</Button>
        </li>)}
      </ul>}
    {state.failed && state.summary && <p className={styles.note} role="alert">{t('next.remoteStatus.failed')}</p>}
  </>;
}
