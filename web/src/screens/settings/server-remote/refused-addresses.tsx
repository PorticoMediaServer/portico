import {useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {RefusedAddressesSource} from '@core/server-admin/refused-addresses.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './server-remote.module.css';

export {RefusedAddressesSource} from '@core/server-admin/refused-addresses.ts';
const t = defaultI18n.t;
const K = 'next.settingsSpec.server.remote.groups.2.rows.0.children.0.children.0';

/** One line per refused name: why, how often, and its one fix. "Use Standard"
 * changes the whole server, so it asks first; "Add certificate…" goes to Use
 * your own address. */
export function RefusedAddressesControl({source, onStandard, onCertificate, disabled}: Readonly<{
  source: RefusedAddressesSource; onStandard: () => void; onCertificate: () => void; disabled?: boolean;
}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirming, setConfirming] = useState(false);
  if (!state.names.length) return <p className={styles.refusedEmpty} role="status">{state.failed ? t('next.remoteStatus.failed') : t(`${K}.control.empty`)}</p>;
  return <>
    <ul className={styles.refused} aria-label={t(`${K}.label`)}>
      {state.names.map(item => <li key={item.name} className={styles.refusedItem}>
        <span className={styles.refusedIcon} aria-hidden="true"><Icon name={item.reason === 'proxy' ? 'i-server' : 'i-globe'} size={20}/></span>
        <span className={styles.refusedText}><strong>{item.name}</strong>
          <small>{t(`${K}.reason.${item.reason}`)} {t(`${K}.asked`, {count: item.count})}</small></span>
        {item.reason === 'name'
          ? <Button size="small" variant="glass" disabled={disabled || !!state.allowing} loading={state.allowing === item.name} onClick={() => {void source.allow(item.name);}}>{t(`${K}.control.itemAction.label`)}</Button>
          : item.reason === 'certificate'
            ? <Button size="small" variant="glass" disabled={disabled} onClick={onCertificate}>{t(`${K}.control.certificateAction.label`)}</Button>
            : <Button size="small" variant="glass" disabled={disabled} onClick={() => setConfirming(true)}>{t(`${K}.control.standardAction.label`)}</Button>}
      </li>)}
    </ul>
    {state.failed && <p className={styles.note} role="alert">{t('next.remoteStatus.failed')}</p>}
    {confirming && <Dialog open={confirming} title={t(`${K}.control.standardAction.title`)} onOpenChange={setConfirming}
      footer={<><Button onClick={() => setConfirming(false)}>{t('action.cancel')}</Button>
        <Button variant="primary" onClick={() => {setConfirming(false);onStandard();}}>{t(`${K}.control.standardAction.label`)}</Button></>}>
      <p>{t(`${K}.control.standardAction.confirm`)}</p>
    </Dialog>}
  </>;
}
