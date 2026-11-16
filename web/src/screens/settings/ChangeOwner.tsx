import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {PreviousOwner} from '@core/portico-servers.ts';
import {Button} from '../../kit/controls/Button';
import {RichSelect} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {SecureRefusalDialog} from './SecureRefusalDialog';
import {ownerSwitchContextAllowed} from '../../platform/secure-pending';
import type {SettingsOwnerSwitch} from './owner-switch';

const t = defaultI18n.t;
const LABEL = 'next.settingsSpec.server.signin.groups.0.rows.4.control.label';
const CONFIRM = 'next.settingsSpec.server.signin.groups.0.rows.4.control.confirm';

/** Change owner on a Portico Accounts server (definition area 2 rules 31 to 34). The owner chooses what they become
 * (an administrator unless they choose member or to leave); the new owner approves with their Portico Account in a new
 * tab; this page waits for that approval, hands the server over and reloads, since the owner's own powers end. */
export function ChangeOwner({source, isCurrent, onError}: {
  source: SettingsOwnerSwitch; isCurrent: () => boolean; onError: (error: unknown) => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot);
  const [open, setOpen] = useState(false), [secure, setSecure] = useState(false), [unavailable, setUnavailable] = useState(false);
  const [previousOwner, setPreviousOwner] = useState<PreviousOwner>('admin');
  const pending = useRef(false), alive = useRef(true);
  const opener = useRef<HTMLButtonElement>(null), cancelButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const current = () => alive.current && isCurrent();
  const run = async (work: () => Promise<void>) => {
    if (!current() || pending.current) return;
    pending.current = true;
    try {await work();} catch (error) {if (current()) onError(error);} finally {pending.current = false;}
  };
  const start = () => {void run(async () => {
    if (!ownerSwitchContextAllowed()) {setSecure(true);return;}
    setPreviousOwner('admin');
    const ready = await source.startChangeOwner();
    if (!current()) return;
    setUnavailable(!ready);setOpen(true);
  });};
  const close = () => {void run(async () => {
    if (state.confirmed) return;
    await source.cancel();
    if (current()) {setOpen(false);setUnavailable(false);}
  });};
  const proceed = () => {
    const approvalUrl = source.ownerApprovalUrl();
    if (!approvalUrl || state.confirmed || pending.current) return;
    // Opened from the click itself so the browser allows it; this page keeps the choice and waits.
    window.open(approvalUrl, '_blank', 'noopener');
    const chosen = previousOwner;
    void run(async () => {
      await source.confirmChangeOwner(chosen);
      if (current()) window.location.reload();
    });
  };
  const busy = state.busy || pending.current, ready = !!source.ownerApprovalUrl();
  const label = t(LABEL), verb = label.replace(/…$/, '');
  return <>
    <Button ref={opener} variant="glass" size="small" loading={busy && !open} disabled={!isCurrent() || state.busy || !!state.preview || state.confirmed} onClick={start}>{label}</Button>
    <SecureRefusalDialog open={secure} onOpenChange={value => {if (!value) setSecure(false);}} />
    <Dialog footerDismisses open={open} onOpenChange={value => {if (!value) close();}} title={verb} description={t(CONFIRM)}
      initialFocusRef={cancelButton} returnFocusRef={opener} dismissOnBack={!state.confirmed} dismissOnOutsidePress={!busy} showCloseButton={!state.confirmed}
      footer={<>
        <Button ref={cancelButton} size="small" disabled={state.confirmed} onClick={close}>{t('action.cancel')}</Button>
        <Button variant="primary" size="small" loading={state.confirmed} disabled={!ready || state.confirmed} onClick={proceed}>{t('next.settings.ownerSwitch.continueToApprove')}</Button></>}>
      {(unavailable || !ready) && <p role="alert">{t('next.settings.ownerSwitch.approvalUnavailable')}</p>}
      {state.confirmed && <p role="status">{t('next.settings.ownerSwitch.approvingOwner')}</p>}
      <RichSelect label={t('accounts.settings.changeOwnerLocalYou')} value={previousOwner} disabled={state.confirmed}
        description={previousOwner === 'removed' ? t('accounts.settings.changeOwnerLocalLeaveHelp') : undefined}
        options={[{value: 'admin', label: t('accounts.settings.changeOwnerLocalStayAdmin'), isDefault: true}, {value: 'member', label: t('accounts.settings.changeOwnerLocalStayMember')},
          {value: 'removed', label: t('accounts.settings.changeOwnerLocalLeave'), description: t('accounts.settings.changeOwnerLocalLeaveHelp')}]}
        onValueChange={value => setPreviousOwner(value === 'member' || value === 'removed' ? value : 'admin')} />
    </Dialog>
  </>;
}
