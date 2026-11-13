import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import type {SettingsStorageSource} from './storage-source';
import styles from './server.module.css';

const t = defaultI18n.t;
const label = () => t('next.settingsSpec.server.storage.groups.0.rows.3.control.label');

/**
 * Empty cache (#3208): the dialog first says how much it frees now, then empties without a restart; the usage is read
 * again after. Copies a download request still names, recordings and originals are never touched (the server's rule).
 */
export function EmptyCache({source}: Readonly<{source: SettingsStorageSource}>) {
  const [open, setOpen] = useState(false), [bytes, setBytes] = useState<number>(), [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false), [freed, setFreed] = useState<number>();
  const begin = () => {
    setOpen(true); setBytes(undefined); setFailed(false); setFreed(undefined);
    source.preview().then(setBytes, () => setFailed(true));
  };
  const empty = async () => {
    setBusy(true); setFailed(false);
    try {setFreed(await source.emptyCache()); setOpen(false);} catch {setFailed(true);} finally {setBusy(false);}
  };
  return <>
    <Button size="compact" variant="glass" onClick={begin}>{label()}</Button>
    {freed !== undefined && <span role="status" className={styles.storageNote}>{t('server.space.cacheFreed', {bytes: defaultI18n.bytes(freed)})}</span>}
    <Dialog footerDismisses open={open} title={label()} onOpenChange={value => {if (!value && !busy) setOpen(false);}}
      footer={<><Button size="compact" variant="glass" disabled={busy} onClick={() => setOpen(false)}>{t('action.cancel')}</Button>
        <Button size="compact" variant="primary" loading={busy} disabled={busy || bytes === undefined || bytes === 0} onClick={() => {void empty();}}>{label()}</Button></>}>
      <p>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.confirm')}</p>
      {bytes === undefined && !failed && <p role="status">{t('next.settings.loading')}</p>}
      {bytes !== undefined && <p>{bytes > 0 ? t('server.space.cacheFrees', {bytes: defaultI18n.bytes(bytes)}) : t('server.space.cacheEmpty')}</p>}
      {failed && <p role="alert" className={styles.backupFailure}>{t('next.states.pageFailedBody')}</p>}
    </Dialog>
  </>;
}
