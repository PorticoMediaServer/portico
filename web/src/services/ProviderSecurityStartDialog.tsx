import {useLayoutEffect, useRef, useState} from 'react';
import type {SecurityState, IdentityProvider} from '@core/hosted-security.ts';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {HostedError} from '../kit/accounts/ServicesAuth';
import type {AccountIdentity, ServicesAccount} from './session';
import {servicesI18n} from './i18n';
import type {HostedSecurityTaskKind} from './SecurityPanel';
import {ProviderSecurityPrepareFields} from '../screens/settings/account-profile/ProviderSecurityPrepareFields';
import {captureAccountSecurity, leaveForSecurityProvider, cancelOriginalAccountSecurity, type AccountSecurityServerScope} from '../platform/account-security';
import {browserSecurityJournal, type AccountSecurityPending} from '../platform/account-security-journal';
import styles from './services.module.css';

export function credentialTaskTitle(task: HostedSecurityTaskKind, hasPassword = false) {
  const {t} = servicesI18n();
  return t(task === 'password_set_or_change' ? hasPassword ? 'account.changePasswordTitle' : 'account.setPassword' : task === 'email_change' ? 'web.hostedSecurity.changeEmailTitle' : task === 'mfa_disable' ? 'web.twoStep.turnOffConfirmTitle' : task === 'mfa_enroll' ? 'web.twoStep.turnOnTitle' : 'web.hostedSecurity.newCodesTitle');
}

/** Existing Account Settings owns this dialog. Only the selected provider and
 * original credential task cross the whole-tab navigation in protected storage. */
export function ProviderSecurityStartDialog({account, identity, state, task, enabledProviders, onClose, isCurrent, server}: {
  account: ServicesAccount; identity: AccountIdentity; state: SecurityState; task: HostedSecurityTaskKind;
  enabledProviders: readonly IdentityProvider[]; onClose: () => void; isCurrent: () => boolean; server?: AccountSecurityServerScope;
}) {
  const {t} = servicesI18n(), [email, setEmail] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  type Opening = {live: boolean; busy: boolean; leaving?: boolean; record?: AccountSecurityPending};
  const current = useRef<Opening | undefined>(undefined);
  useLayoutEffect(() => {
    const original: Opening = {live: true, busy: false}; current.current = original;
    return () => {original.live = false; if (!original.leaving && original.record) void cancelOriginalAccountSecurity(original.record).catch(()=>undefined); if (current.current === original) current.current = undefined;};
  }, [account, identity.accountId, identity.familyId, task, isCurrent, server?.serverId, server?.familyId, server?.accountId, server?.profileId]);
  const active = (original: Opening) => original.live && current.current === original && isCurrent();
  const close = async () => {
    const original = current.current;
    if (!original || !active(original) || original.busy) return;
    const originalPage=location.href;
    original.busy = true;original.live = false;original.leaving = true; setBusy(true);setEmail('');
    if (original.record) await cancelOriginalAccountSecurity(original.record).catch(() => undefined);
    if (current.current === original && isCurrent() && location.href===originalPage) onClose();
  };
  const choose = async (providerId: string) => {
    const original = current.current;
    if (!original || !active(original) || original.busy) return;
    const selectedEmail = email.trim(), split = selectedEmail.lastIndexOf('@');
    const target = task === 'email_change' ? selectedEmail.slice(0, split) + '@' + selectedEmail.slice(split + 1).toLowerCase() : '';
    if (task === 'email_change' && (split < 1 || !/^\S+@\S+\.\S+$/.test(target))) return;
    original.busy = true; setBusy(true); setError(undefined);
    const check = () => {if (!active(original)) throw new DOMException('The Account Settings opening ended.', 'AbortError');};
    try {
      const journal = browserSecurityJournal();
      // A retry keeps its first selected provider and target. Editing cannot
      // silently create a second operation after an uncertain preparation.
      const pending = original.record ?? await captureAccountSecurity({account, identity, state, purpose: task, target, providerId, assertCurrent: check, server}, journal);
      if (pending.original.provider.id !== providerId || pending.original.intent.target !== target) throw new Error(t('web.link.invalid'));
      original.record = pending;
      await leaveForSecurityProvider(pending, check, journal, () => {original.leaving = true;});
    } catch (value) {original.leaving = false; if (active(original)) setError(value);}
    finally {original.busy = false; if (active(original)) setBusy(false);}
  };
  const providers = state.providers.filter(v => v.revision && enabledProviders.includes(v.provider) && (!current.current?.record || v.id === current.current.record.original.provider.id));
  const locked = busy || Boolean(current.current?.record);
  return <Dialog open title={credentialTaskTitle(task)} className={styles.overlaySpacing} showCloseButton={false} onOpenChange={open => {if (!open) void close();}}
    footer={<Button size="small" disabled={busy} onClick={() => void close()}>{t('action.cancel')}</Button>}>
    {error ? <HostedError error={error}/> : null}
    <ProviderSecurityPrepareFields task={task} email={email} providers={providers} disabled={busy} emailDisabled={locked} providerDisabled={task === 'email_change' && !/^\S+@\S+\.\S+$/.test(email.trim())}
      onEmailChange={value => {const original = current.current; if (original && active(original) && !locked) setEmail(value);}} onChooseProvider={id => void choose(id)}/>
  </Dialog>;
}
