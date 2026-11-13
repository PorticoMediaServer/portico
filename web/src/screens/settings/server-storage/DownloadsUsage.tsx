import {useCallback, useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ServerRequester} from '@core/query/server-api.ts';
import {clearPreparedCopies, readDownloadsByPerson, type DownloadsUsage as Usage} from '@core/server-admin/downloads-usage-source.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import styles from '../server/server.module.css';

const t = defaultI18n.t;

/** Each person's prepared downloads on this server, the total and the room left under the limit (`GET /v1/admin/downloads`). */
export function DownloadsUsage({api, isCurrent, label}: Readonly<{api: ServerRequester; isCurrent: () => boolean; label: string}>) {
  const [usage, setUsage] = useState<Usage>(), [failed, setFailed] = useState(false);
  // Clear prepared copies, one person at a time (downloads rule 47): the confirm, the transfer refusal in the dialog, the done line.
  const [clearing, setClearing] = useState<Usage['items'][number]>(), [busy, setBusy] = useState(false), [refusal, setRefusal] = useState<'in-use' | 'failed'>(), [cleared, setCleared] = useState(false);
  const load = useCallback((signal: AbortSignal) => {
    setFailed(false);
    readDownloadsByPerson(api, signal).then(value => {if (!signal.aborted && isCurrent()) setUsage(value);}, () => {if (!signal.aborted && isCurrent()) setFailed(true);});
  }, [api, isCurrent]);
  useEffect(() => {const lifetime = new AbortController(); load(lifetime.signal); return () => lifetime.abort();}, [load]);
  const clear = async () => {
    if (!clearing || busy) return;
    setBusy(true); setRefusal(undefined);
    try {
      const outcome = await clearPreparedCopies(api, clearing.userId);
      if (!isCurrent()) return;
      if (outcome === 'in-use') setRefusal('in-use'); else {setClearing(undefined); setCleared(true); load(new AbortController().signal);}
    } catch {if (isCurrent()) setRefusal('failed');}
    finally {if (isCurrent()) setBusy(false);}
  };
  const people = usage?.items.filter(person => person.titles > 0 || person.copiesBytes > 0) ?? [];
  return <div className={styles.storage} role="group" aria-label={label}>
    {failed && <p className={styles.storageFailure} role="alert"><span>{t('next.saved.loadFailed')}</span><button type="button" onClick={() => load(new AbortController().signal)}>{t('next.shell.tryAgain')}</button></p>}
    {!usage && !failed && <p role="status">{t('next.settings.loading')}</p>}
    {usage && <>
      <div className={styles.storageTotal}><b>{t('server.downloadUsage.total')}</b><span>{defaultI18n.bytes(usage.copiesBytes)}</span></div>
      {people.length ? <ul className={styles.storageList}>{people.map(person => <li key={person.userId}>
        <div className={styles.storageText}><b>{person.username}</b><small>{t('server.downloadUsage.titles', {count: person.titles})}</small></div>
        <span className={styles.storageSize}>{defaultI18n.bytes(person.copiesBytes)}</span>
        {person.copiesBytes > 0 && <Button size="small" variant="glass" onClick={() => {setRefusal(undefined); setCleared(false); setClearing(person);}}>{t('server.downloadUsage.clear')}</Button>}
      </li>)}</ul> : <p>{t('server.downloadUsage.none')}</p>}
      {cleared && <p role="status" className={styles.storageNote}>{t('server.downloadUsage.cleared')}</p>}
      {usage.roomBytes !== undefined && <p className={styles.storageNote}>{t('server.downloadUsage.remaining', {size: defaultI18n.bytes(usage.roomBytes)})}</p>}
    </>}
    <Dialog footerDismisses open={!!clearing} onOpenChange={open => {if (!open && !busy) setClearing(undefined);}} size="sm" role="alertdialog"
      title={clearing ? t('server.downloadUsage.clearTitle', {name: clearing.username}) : ''} description={t('server.downloadUsage.removeBody')} footer={<>
        <Button disabled={busy} onClick={() => setClearing(undefined)}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={busy} onClick={() => {void clear();}}>{t('server.downloadUsage.clear')}</Button>
      </>}>
      {refusal === 'in-use' && clearing ? <p role="alert">{t('server.downloadUsage.clearInUse', {name: clearing.username})}</p> : null}
      {refusal === 'failed' ? <p role="alert">{t('next.states.pageFailedBody')}</p> : null}
    </Dialog>
  </div>;
}
