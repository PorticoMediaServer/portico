import {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import type {DownloadRemovalSource} from './download-removal-source';
import type {DownloadsUsageRow} from './downloads-usage-source';
import styles from '../server/server.module.css';

const t = defaultI18n.t;
export function DownloadRemoval({source, row, disabled}: Readonly<{source: DownloadRemovalSource; row: DownloadsUsageRow; disabled: boolean}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const view = useMemo<{active: boolean; dialog?: object; flight?: object}>(() => { /* Preserve the original ownership/invalidation keys. */ void source; void row; return ({active: true}); }, [source, row]);
  const [dialog, setDialog] = useState<object>();
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.dialog = undefined;};}, [view]);
  const locked = state.pending ?? state.continuation;
  const target = state.pending?.profileKey === row.profileKey ? state.pending : row;
  const name = target.profileName || target.accountName || '—';
  const held = view.active && dialog !== undefined && view.dialog === dialog;
  const close = () => {if (source.getSnapshot().busy) return;view.dialog = undefined;setDialog(undefined);};
  const open = () => {if (!view.active || disabled || state.busy || locked && locked.profileKey !== row.profileKey) return;const next = {};view.dialog = next;setDialog(next);};
  const submit = async () => {
    const captured = dialog;
    if (!captured || !held || view.dialog !== captured || view.flight || source.getSnapshot().busy) return;
    const flight = {};view.flight = flight;
    const current = () => view.active && view.dialog === captured && view.flight === flight;
    try {
      const pending = source.getSnapshot().pending;
      const done = pending ? pending === target && await source.retry(current) : await source.remove(target, current);
      if (done && current()) {view.dialog = undefined;setDialog(undefined);}
    } finally {if (view.flight === flight) view.flight = undefined;}
  };
  return <>
    <Button size="small" variant="glass" disabled={disabled || state.busy || !!locked && locked.profileKey !== row.profileKey} onClick={open}>{t('server.downloadUsage.remove')}</Button>
    <Dialog open={held} title={t('server.downloadUsage.removeTitle', {name})} onOpenChange={value => {if (!value) close();}} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
      footer={<><Button size="small" variant="glass" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button><Button size="small" variant="glass" className={styles.cleanupDanger} disabled={state.busy} onClick={() => {void submit();}}>{t(state.pending ? 'action.tryAgain' : 'action.remove')}</Button></>}>
      <p>{t('server.downloadUsage.removeBody')}</p>
      {state.error && <p role="alert" className={styles.backupFailure}>{t('error.unconfirmedChange')}</p>}
    </Dialog>
  </>;
}
