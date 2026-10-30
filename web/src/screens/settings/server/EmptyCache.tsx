import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import type {SettingsCacheSource} from './cache-source';
import styles from './server.module.css';

const t = defaultI18n.t;
export function EmptyCache({source}: Readonly<{source: SettingsCacheSource}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [dialog, setDialog] = useState<object>();
  const active = useRef<object | undefined>(undefined), owner = useRef(source);
  if (owner.current !== source) {owner.current = source;active.current = undefined;}
  useLayoutEffect(() => {setDialog(undefined);return () => {active.current = undefined;};}, [source]);
  const open = () => {if (owner.current !== source || source.getSnapshot().phase !== 'ready') return;const next = {};active.current = next;setDialog(next);};
  const close = () => {active.current = undefined;setDialog(undefined);};
  const held = dialog !== undefined && active.current === dialog;
  const submit = () => {
    if (!held || active.current !== dialog) return;
    const captured = dialog;
    void source.empty(() => owner.current === source && active.current === captured).finally(() => {if (active.current === captured) close();});
  };
  return <>
    <Button size="compact" variant="glass" disabled={state.phase !== 'ready'} onClick={open}>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')}</Button>
    {state.phase === 'scheduled' && <span role="status" className={styles.storageNote}>{t('web.maintenance.restartingTitle')}</span>}
    {state.phase === 'uncertain' && <span role="alert" className={styles.backupFailure}>{t('error.unconfirmedChange')}</span>}
    <Dialog open={held} title={t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')} onOpenChange={value => {if (!value) close();}}
      footer={<><Button size="compact" variant="glass" onClick={close}>{t('action.cancel')}</Button><Button size="compact" variant="primary" disabled={state.phase !== 'ready'} onClick={submit}>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')}</Button></>}>
      <p>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.confirm')}</p>
      <p>{t('next.settingsSpec.server.general.groups.3.rows.1.control.confirm')}</p>
    </Dialog>
  </>;
}
