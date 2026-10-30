import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileOtherDeviceSignOut} from './sign-out-others';
const t = defaultI18n.t;
type Attempt = {command: ProfileOtherDeviceSignOut; claimed: boolean};

export function ProfileOtherSignOut({command, isCurrent, onFailure, disabled}: {
  command: ProfileOtherDeviceSignOut; isCurrent: () => boolean; onFailure: () => void; disabled?: boolean;
}) {
  const [confirmation, setConfirmation] = useState<Attempt>(), [busy, setBusy] = useState(false);
  const active = useRef<Attempt | undefined>(undefined), live = useRef(true), trigger = useRef<HTMLButtonElement>(null);
  const latest = useRef({command, isCurrent, disabled});latest.current = {command, isCurrent, disabled};
  useEffect(() => {live.current = true;return () => {live.current = false;active.current = undefined;};}, [command]);
  const currentAttempt = (attempt: Attempt) => live.current && active.current === attempt && latest.current.command === attempt.command && latest.current.isCurrent();
  const close = (attempt: Attempt) => {
    if (!currentAttempt(attempt)) return;
    active.current = undefined;setConfirmation(undefined);setBusy(false);
  };
  const run = async (attempt: Attempt) => {
    if (!currentAttempt(attempt) || attempt.claimed || latest.current.disabled) return;
    // Claim this exact confirmation before notifying any state subscriber.
    attempt.claimed = true;setBusy(true);
    try {
      if (!currentAttempt(attempt) || latest.current.disabled) return;
      await attempt.command.signOut();
      if (currentAttempt(attempt)) {active.current = undefined;setConfirmation(undefined);setBusy(false);}
    } catch {
      if (currentAttempt(attempt)) {active.current = undefined;setConfirmation(undefined);setBusy(false);onFailure();}
    }
  };
  if (!isCurrent()) return null;
  return <>
    <Button ref={trigger} size="small" variant="glass" disabled={disabled || busy} onClick={() => {
      if (!live.current || latest.current.disabled || !latest.current.isCurrent() || active.current?.claimed) return;
      const attempt: Attempt = {command: latest.current.command, claimed: false};active.current = attempt;setConfirmation(attempt);setBusy(false);
    }}>{t('next.settingsSpec.account.profile.groups.8.rows.0.control.label')}</Button>
    {confirmation && <Dialog children={null} open={active.current === confirmation && confirmation.command === command} size="sm" returnFocusRef={trigger} title={t('next.settingsSpec.account.profile.groups.8.rows.0.label')}
      description={t('next.settingsSpec.account.profile.groups.8.rows.0.control.confirm')}
      dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy} onOpenChange={value => {if (!value) close(confirmation);}}
      footer={<><Button size="small" disabled={busy} onClick={() => close(confirmation)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={busy} onClick={() => {void run(confirmation);}}>{t('next.settingsSpec.account.profile.groups.8.rows.0.control.label')}</Button></>} />}
  </>;
}
