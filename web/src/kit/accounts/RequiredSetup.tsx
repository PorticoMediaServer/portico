import {useEffect, useId, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import type {TwoFactorEnrolment} from '@core/identity-client.ts';
import {Button, TextField} from '../controls';
import {Dialog} from '../overlays/Dialog';
import {AuthCodeBoxes, AuthError} from './AuthStage';
import {ServicesLabel, ServicesStrength} from './ServicesAuth';
import {ServicesAuthenticator, ServicesRecoveryCodes} from './ServicesWorkspace';
import styles from './required-setup.module.css';

const t = defaultI18n.t;

/** account22 panel 4. Password proof belongs to the live sign-in caller;
 * this drawn panel has a key/QR and code, never an extra password field. */
export function RequiredTwoStepDialog({enrolment, busy, error, onConfirm, onCancel}: {
  enrolment: TwoFactorEnrolment; busy?: boolean; error?: unknown;
  onConfirm: (code: string) => void; onCancel: () => void;
}) {
  const [code, setCode] = useState(''), [localError, setLocalError] = useState<unknown>();
  const life = useRef(true);
  useEffect(() => {life.current = true; return () => {life.current = false;};}, []);
  // Clipboard errors are visible only in the still-mounted original dialog.
  const copy = async () => {try {await navigator.clipboard.writeText(enrolment.secret);} catch (failure) {if (life.current) setLocalError(failure);}};
  return <Dialog open title={t('web.hostedSecurity.addToAuthenticator')} description={t('web.hostedSecurity.enterKey')}
    showCloseButton={false} className={styles.dialog} onOpenChange={open => {if (!open) {life.current = false; setCode(''); onCancel();}}}
    footer={<><Button disabled={busy} onClick={() => {life.current = false; setCode(''); onCancel();}}>{t('action.cancel')}</Button>
      <Button variant="primary" disabled={code.length !== 6} loading={busy} onClick={() => {if (code.length === 6 && !busy) onConfirm(code);}}>{t('web.hostedSecurity.confirm')}</Button></>}>
    <AuthError error={error ?? localError} />
    <ServicesAuthenticator secret={enrolment.secret} uri={enrolment.uri} onCopy={() => void copy()} onError={setLocalError} />
    <div className={styles.code}><AuthCodeBoxes length={6} grouped value={code} onChange={setCode} onComplete={setCode} label={t('web.twoStep.code')} autoFocus disabled={busy} /></div>
  </Dialog>;
}

/** account22 panel 5. The caller clears codes before leaving this panel. */
export function RequiredRecoveryCodesDialog({codes, onDone}: {codes: readonly string[]; onDone: () => void}) {
  const [error, setError] = useState<unknown>();
  const life = useRef(true);
  useEffect(() => {life.current = true; return () => {life.current = false;};}, []);
  const copy = () => {void navigator.clipboard.writeText(codes.join('\n')).catch(failure => {if (life.current) setError(failure);});};
  const download = () => {
    let url: string | undefined;
    try {
      url = URL.createObjectURL(new Blob([codes.join('\n')], {type: 'text/plain;charset=utf-8'}));
      const link = document.createElement('a'); link.href = url; link.download = 'portico-recovery-codes.txt'; link.click();
    } catch (failure) {setError(failure);} finally {if (url) URL.revokeObjectURL(url);}
  };
  return <Dialog open title={t('web.hostedSecurity.newCodesTitle')} description={t('services.security.codesBody')}
    showCloseButton={false} className={styles.dialog} onOpenChange={open => {if (!open) onDone();}}
    footer={<Button variant="primary" onClick={onDone}>{t('action.done')}</Button>}>
    <AuthError error={error} /><ServicesRecoveryCodes codes={codes} onCopy={copy} onDownload={download} />
  </Dialog>;
}

/** account22 panel 1. It is a password change, not enrollment proof. */
export function RequiredPasswordDialog({busy, error, onChange, onCancel}: {
  busy?: boolean; error?: unknown; onChange: (current: string, next: string) => void; onCancel: () => void;
}) {
  const [current, setCurrent] = useState(''), [next, setNext] = useState('');
  const form = useId(), input = useRef<HTMLInputElement>(null), ready = Boolean(current) && passwordStrength(next).acceptable;
  const clear = () => {setCurrent(''); setNext('');};
  return <Dialog open title={t('account.changePasswordTitle')} description={t('web.hostedSecurity.othersSignedOut')}
    initialFocusRef={input} showCloseButton={false} className={styles.dialog} onOpenChange={open => {if (!open) {clear(); onCancel();}}}
    footer={<><Button disabled={busy} onClick={() => {clear(); onCancel();}}>{t('action.cancel')}</Button>
      <Button variant="primary" type="submit" form={form} disabled={!ready} loading={busy}>{t('account.changePasswordTitle')}</Button></>}>
    <AuthError error={error} />
    <form id={form} className={styles.form} onSubmit={event => {event.preventDefault(); if (ready && !busy) {const proof = current, password = next; clear(); onChange(proof, password);}}}>
      <TextField ref={input} type="password" label={t('account.currentPassword')} autoComplete="current-password" value={current} onChange={event => setCurrent(event.currentTarget.value)} disabled={busy} required />
      <TextField type="password" label={<ServicesLabel detail={t('services.password.minimum')}>{t('auth.newPassword')}</ServicesLabel>} autoComplete="new-password" value={next} onChange={event => setNext(event.currentTarget.value)} disabled={busy} required />
      <ServicesStrength password={next} id={`${form}-strength`} />
    </form>
  </Dialog>;
}
