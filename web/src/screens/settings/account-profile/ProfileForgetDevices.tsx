import {useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {ManagedProfile} from '@core/profile-management.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileSettingsSource} from './source';
const t = defaultI18n.t;

export function ProfileForgetDevices({source, isCurrent, onFailure}: {
  source: ProfileSettingsSource; isCurrent: () => boolean; onFailure: () => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [target, setTarget] = useState<ManagedProfile>();
  const trigger = useRef<HTMLButtonElement>(null), pending = useRef(false);
  const selected = source.selected();
  if (!isCurrent() || !state.document?.canManage || !selected?.pinRequired) return null;
  const submit = async () => {
    if (!target || pending.current || state.busy || !isCurrent()) return;
    pending.current = true;
    try {
      await source.forgetDevices(target);
      if (isCurrent() && source.getSnapshot().document) setTarget(undefined);
    } catch {
      if (isCurrent() && !source.getSnapshot().error) onFailure();
    } finally {pending.current = false;}
  };
  return <>
    <Button ref={trigger} size="small" variant="glass" disabled={state.busy} onClick={() => {if (isCurrent()) setTarget(selected);}}>{t('profile.forgetDevices')}</Button>
    {target && <Dialog children={null} open size="sm" returnFocusRef={trigger} title={t('profile.forgetDevices')}
      description={t('next.settingsSpec.account.profile.groups.2.rows.0.children.1.control.confirm')}
      dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy} onOpenChange={open => {if (!open && !pending.current) setTarget(undefined);}}
      footer={<><Button size="small" disabled={state.busy} onClick={() => {if (!pending.current) setTarget(undefined);}}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={state.busy} onClick={() => {void submit();}}>{t('profile.forgetDevices')}</Button></>} />}
  </>;
}
