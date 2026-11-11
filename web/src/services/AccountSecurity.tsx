import {useEffect, useState, type FormEvent, type ReactNode} from 'react';
import {AuthPasswordField} from '../kit/accounts';
import {ServicesAuthenticator, ServicesBadge, ServicesDangerAction, ServicesGroup, ServicesRecoveryCodes, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {ServicesLabel, ServicesNotice, ServicesQuiet, ServicesStrength} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {accountSession, personCall} from './account-session';
import {servicesCall} from './services-api';
import {problemMessage} from './account-problem';
import {startProvider, type Provider} from './AccountProviders';
import {codeInput} from './AccountSignUp';
import type {Account} from './AccountWorkspace';
import styles from './services.module.css';

type Task =
  | {kind: 'password' | 'email' | 'twoStepOff' | 'newCodes' | 'delete' | 'twoStepOn'}
  | {kind: 'emailCode'; email: string}
  | {kind: 'connect' | 'disconnect'; provider: Provider}
  | {kind: 'scan'; secret: string; uri: string}
  | {kind: 'codes'; codes: readonly string[]};
type Proof = {currentPassword?: string; twoStepCode?: string};

/** Security: everything here that could take an account away asks for the current password typed
 * now and the two-step code when it is on (an account with no password relies on a recent
 * sign-in instead). */
export function AccountSecurity({account, reload}: {account: Account; reload: () => Promise<void>}) {
  const {t} = servicesI18n();
  const providerName = (provider: Provider) => t(provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google');
  const [task, setTask] = useState<Task>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [notice, setNotice] = useState<ReactNode>(() => { const value = new URLSearchParams(location.search).get('connected'); return value === 'google' || value === 'apple' ? t('next.account.security.connectedNotice', {provider: providerName(value)}) : undefined; });
  const [twoStep, setTwoStep] = useState<{on: boolean; recoveryCodesLeft: number}>();
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [newPassword, setNewPassword] = useState(''), [newEmail, setNewEmail] = useState(''), [emailCode, setEmailCode] = useState('');
  const loadTwoStep = () => personCall<{on: boolean; recoveryCodesLeft: number}>('GET', '/v1/account/two-step').then(setTwoStep, () => undefined);
  useEffect(() => { void loadTwoStep(); }, [account.twoStep]);
  // Back from connecting Google or Apple: say so once, and take the note out of the address.
  const [arrivedConnected] = useState(() => { const value = new URLSearchParams(location.search).get('connected'); return value === 'google' || value === 'apple' ? value : undefined; });
  useEffect(() => { if (arrivedConnected) goTo('/account?tab=security', {replace: true}); }, [arrivedConnected]);
  const open = (next: Task) => { setTask(next); setError(undefined); setNotice(undefined); setPassword(''); setCode(''); setNewPassword(''); setNewEmail(''); setEmailCode(''); };
  const close = () => { if (!busy) setTask(undefined); };
  const proof = (): Proof => ({...(account.hasPassword ? {currentPassword: password} : {}), ...(account.twoStep ? {twoStepCode: code.trim()} : {})});
  const proven = (!account.hasPassword || password.length > 0) && (!account.twoStep || code.trim().length >= 6);
  const run = (work: () => Promise<void>) => async (event?: FormEvent) => {
    event?.preventDefault();
    if (busy) return;
    setBusy(true); setError(undefined);
    try { await work(); } catch (caught) { setError(caught); } finally { setBusy(false); }
  };
  const finish = async (message?: ReactNode) => { setTask(undefined); setNotice(message); await reload(); await loadTwoStep(); };

  const proofFields = (withCode = account.twoStep) => <>
    {account.hasPassword
      ? <AuthPasswordField label={t('next.account.proof.password')} name="current-password" autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required />
      : <ServicesQuiet>{t('next.account.proof.noPassword')}</ServicesQuiet>}
    {withCode && <TextField variant="auth" label={t('next.account.proof.code')} description={t('next.account.proof.codeHelp')} name="two-step-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} required />}
  </>;
  const problem = error ? <ServicesNotice tone="error">{t(problemMessage(error, 'account'))}</ServicesNotice> : null;
  const actions = (label: string, ready: boolean, danger = false) => <><Button size="small" disabled={busy} onClick={close}>{t('action.cancel')}</Button>
    {danger ? <ServicesDangerAction loading={busy} disabled={!ready} onClick={() => void submit()}>{label}</ServicesDangerAction> : <Button size="small" variant="primary" type="submit" form="security-task" loading={busy} disabled={!ready}>{label}</Button>}</>;

  const submit = run(async () => {
    if (!task) return;
    switch (task.kind) {
      case 'password':
        await personCall('POST', '/v1/account/password', {...proof(), newPassword});
        return finish(t('next.account.passwordDialog.done'));
      case 'email': {
        const answer = await personCall<{email: string}>('POST', '/v1/account/email', {...proof(), newEmail});
        await reload();
        setTask({kind: 'emailCode', email: answer.email}); setEmailCode('');
        return;
      }
      case 'emailCode': {
        const answer = await servicesCall<{email: string}>('POST', '/v1/accounts/confirm', {email: task.email, code: emailCode});
        return finish(t('next.account.emailDialog.done', {address: answer.email}));
      }
      case 'connect':
        return startProvider(task.provider, '/account?tab=security', proof());
      case 'disconnect':
        await personCall('DELETE', `/v1/account/providers/${task.provider}`, proof());
        return finish();
      case 'twoStepOn': {
        const answer = await personCall<{secret: string; qrAddress: string}>('POST', '/v1/account/two-step', proof());
        setTask({kind: 'scan', secret: answer.secret, uri: answer.qrAddress}); setCode('');
        return;
      }
      case 'scan': {
        const answer = await personCall<{recoveryCodes: string[]}>('POST', '/v1/account/two-step/confirm', {code: code.trim()});
        setTask({kind: 'codes', codes: answer.recoveryCodes});
        await reload();
        return;
      }
      case 'twoStepOff':
        await personCall('DELETE', '/v1/account/two-step', proof());
        return finish();
      case 'newCodes': {
        const answer = await personCall<{recoveryCodes: string[]}>('POST', '/v1/account/two-step/recovery-codes', proof());
        setTask({kind: 'codes', codes: answer.recoveryCodes});
        return;
      }
      case 'delete':
        await personCall('POST', '/v1/account/delete', proof());
        accountSession.forget();
        goTo('/sign-in', {replace: true});
        return;
    }
  });
  const copyCodes = (codes: readonly string[]) => { void navigator.clipboard?.writeText(codes.join('\n')).then(() => setNotice(t('next.account.twoStepDialog.copied')), () => undefined); };
  const downloadCodes = (codes: readonly string[]) => {
    const link = document.createElement('a');
    link.href = URL.createObjectURL(new Blob([codes.join('\n') + '\n'], {type: 'text/plain'}));
    link.download = t('next.account.twoStepDialog.fileName');
    link.click();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  };

  const dialog = (() => {
    if (!task) return null;
    const provider = 'provider' in task ? providerName(task.provider) : '';
    switch (task.kind) {
      case 'password': return {title: t('next.account.passwordDialog.title'), description: t('next.account.passwordDialog.body'), footer: actions(t('next.account.passwordDialog.submit'), proven && [...newPassword].length >= 8), body: <>
        {proofFields()}
        <div><AuthPasswordField label={<ServicesLabel detail={t('next.account.signUp.passwordRule')}>{t('next.account.passwordDialog.new')}</ServicesLabel>} name="new-password" autoComplete="new-password" value={newPassword} onChange={event => setNewPassword(event.currentTarget.value)} disabled={busy} required /><ServicesStrength password={newPassword} id="new-password-strength" /></div>
      </>};
      case 'email': return {title: t('next.account.emailDialog.title'), description: t('next.account.emailDialog.body'), footer: actions(t('next.account.emailDialog.submit'), proven && newEmail.includes('@')), body: <>
        {proofFields()}
        <TextField variant="auth" label={t('next.account.emailDialog.new')} description={t('next.account.emailDialog.newHelp')} name="new-email" type="email" autoComplete="email" value={newEmail} onChange={event => setNewEmail(event.currentTarget.value)} disabled={busy} required />
      </>};
      case 'emailCode': return {title: t('next.account.emailDialog.checkTitle'), description: t('next.account.emailDialog.checkBody', {address: task.email}), footer: actions(t('next.account.emailDialog.confirm'), emailCode.length === 9), body:
        <TextField variant="auth" label={t('next.account.confirm.code')} name="one-time-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} placeholder={t('next.account.codePlaceholder')} value={emailCode} onChange={event => setEmailCode(codeInput(event.currentTarget.value))} disabled={busy} required autoFocus />};
      case 'connect': return {title: t('next.account.connectDialog.title', {provider}), description: t('next.account.connectDialog.body', {provider}), footer: actions(t('next.account.connectDialog.submit', {provider}), proven), body: proofFields()};
      case 'disconnect': return {title: t('next.account.disconnectDialog.title', {provider}), description: t('next.account.disconnectDialog.body', {provider}), footer: actions(t('next.account.security.disconnect'), proven, true), body: proofFields()};
      case 'twoStepOn': return {title: t('next.account.twoStepDialog.startTitle'), description: t('next.account.twoStepDialog.startBody'), footer: actions(t('action.continue'), proven), body: proofFields(false)};
      case 'scan': return {title: t('next.account.twoStepDialog.scanTitle'), description: t('next.account.twoStepDialog.scanBody'), footer: actions(t('next.account.twoStepDialog.confirm'), code.length === 6), body: <>
        <ServicesAuthenticator secret={task.secret} uri={task.uri} onCopy={() => void navigator.clipboard?.writeText(task.secret)} onError={setError} />
        <TextField variant="auth" label={t('next.account.twoStep.code')} name="one-time-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={event => setCode(event.currentTarget.value.replace(/\D/g, ''))} disabled={busy} required />
      </>};
      case 'codes': return {title: t('next.account.twoStepDialog.codesTitle'), description: t('next.account.twoStepDialog.codesBody'), footer: <Button size="small" variant="primary" onClick={() => void finish()}>{t('next.account.twoStepDialog.done')}</Button>, body:
        <ServicesRecoveryCodes codes={task.codes} onCopy={() => copyCodes(task.codes)} onDownload={() => downloadCodes(task.codes)} />};
      case 'twoStepOff': return {title: t('next.account.twoStepDialog.offTitle'), description: t('next.account.twoStepDialog.offBody'), footer: actions(t('next.account.twoStep.turnOff'), proven, true), body: proofFields()};
      case 'newCodes': return {title: t('next.account.twoStepDialog.newCodesTitle'), description: t('next.account.twoStepDialog.newCodesBody'), footer: actions(t('next.account.twoStep.newCodes'), proven), body: proofFields()};
      case 'delete': return {title: t('next.account.delete.title'), description: t('next.account.delete.body'), footer: actions(t('next.account.delete.submit'), proven, true), body: proofFields()};
    }
  })();

  const providerRow = (provider: Provider) => {
    const connected = account.providers.includes(provider);
    return <ServicesWorkspaceRow key={provider} icon={provider === 'apple' ? 'i-apple' : 'i-google'} title={t('next.account.security.provider', {provider: providerName(provider)})}
      help={connected ? t('next.account.security.connected') : t('next.account.security.notConnected', {provider: providerName(provider)})}>
      <Button size="small" onClick={() => open({kind: connected ? 'disconnect' : 'connect', provider})}>{t(connected ? 'next.account.security.disconnect' : 'next.account.security.connect')}</Button>
    </ServicesWorkspaceRow>;
  };
  return <>
    {notice && !task ? <ServicesNotice tone="success">{notice}</ServicesNotice> : null}
    <ServicesGroup title={t('next.account.security.signingIn')}>
      <ServicesWorkspaceRow title={t('next.account.security.password')} help={account.hasPassword ? t('next.account.security.passwordHelp') : t('next.account.security.noPassword')}>
        {account.hasPassword ? <Button size="small" onClick={() => open({kind: 'password'})}>{t('next.account.security.change')}</Button>
          : <Button size="small" onClick={() => goTo('/reset?next=%2Faccount%3Ftab%3Dsecurity')}>{t('next.account.security.setPassword')}</Button>}
      </ServicesWorkspaceRow>
      <ServicesWorkspaceRow title={t('next.account.security.email')} help={account.newEmailWaiting ? t('next.account.security.emailWaiting', {address: account.newEmailWaiting, current: account.email}) : t('next.account.security.emailCurrent', {address: account.email})}>
        {account.newEmailWaiting && <Button size="small" onClick={() => open({kind: 'emailCode', email: account.newEmailWaiting!})}>{t('next.account.security.enterCode')}</Button>}
        <Button size="small" disabled={!account.hasPassword} onClick={() => open({kind: 'email'})}>{t('next.account.security.change')}</Button>
      </ServicesWorkspaceRow>
      {providerRow('apple')}
      {providerRow('google')}
    </ServicesGroup>
    <ServicesGroup title={t('next.account.twoStep.groupTitle')} description={t('next.account.twoStep.groupHelp')}>
      <ServicesWorkspaceRow title={t('next.account.twoStep.groupTitle')} badge={<ServicesBadge tone={account.twoStep ? 'healthy' : 'default'}>{t(account.twoStep ? 'next.account.twoStep.on' : 'next.account.twoStep.off')}</ServicesBadge>}
        help={account.twoStep && twoStep ? t('next.account.twoStep.codesLeft', {count: twoStep.recoveryCodesLeft}) : undefined}>
        <Button size="small" onClick={() => open({kind: account.twoStep ? 'twoStepOff' : 'twoStepOn'})}>{t(account.twoStep ? 'next.account.twoStep.turnOff' : 'next.account.twoStep.turnOn')}</Button>
      </ServicesWorkspaceRow>
      {account.twoStep && <ServicesWorkspaceRow title={t('next.account.twoStep.recoveryRow')} help={t('next.account.twoStep.recoveryRowHelp')}>
        <Button size="small" onClick={() => open({kind: 'newCodes'})}>{t('next.account.twoStep.newCodes')}</Button>
      </ServicesWorkspaceRow>}
    </ServicesGroup>
    <ServicesGroup title={t('next.account.delete.groupTitle')} danger>
      <ServicesWorkspaceRow title={t('next.account.delete.row')} help={t('next.account.delete.rowHelp')}>
        <ServicesDangerAction icon="i-trash" onClick={() => open({kind: 'delete'})}>{t('next.account.delete.action')}</ServicesDangerAction>
      </ServicesWorkspaceRow>
    </ServicesGroup>
    <Dialog open={dialog !== null} onOpenChange={value => { if (!value) close(); }} size="md" title={dialog?.title} description={dialog?.description} footer={dialog?.footer}>
      <form id="security-task" className={styles.form} onSubmit={submit}>{problem}{dialog?.body}</form>
    </Dialog>
  </>;
}
