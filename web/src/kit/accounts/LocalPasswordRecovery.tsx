import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {AuthError, AuthStage} from './AuthStage';
import {Button} from '../controls/Button';
import {TextField} from '../controls/Fields';
import {Icon} from '../Icon';
import styles from './accounts.module.css';

const t = defaultI18n.t;
export type LocalPasswordRecoveryProps = Readonly<{
  serverName: string; busy?: boolean; error?: unknown; notice?:ReactNode; initialUsername?: string;
  onWriteFile: (username: string) => void;
  onMemberReset: (username: string, code: string, password: string) => void;
  onBack: () => void;
}>;
/** Auth 20. This Local-only view offers two distinct proofs; neither requests
 * an email or persists the one-use code/password outside its live input. */
export function LocalPasswordRecovery({serverName, busy, error, notice, initialUsername = '', onWriteFile, onMemberReset, onBack}: LocalPasswordRecoveryProps) {
  const [owner, setOwner] = useState(initialUsername), [member, setMember] = useState(initialUsername);
  const [code, setCode] = useState(''), [password, setPassword] = useState('');
  const [memberOpen, setMemberOpen] = useState(false);
  const ownerInput = useRef<HTMLInputElement>(null);
  useEffect(() => {if (matchMedia('(min-width: 701px)').matches) ownerInput.current?.focus();}, []);
  return <AuthStage shape="round" phoneShape="top" legal={false} className={styles.localRecoveryStage} contentClassName={styles.localRecoveryContent}>
    <button className={styles.localRecoveryPhoneBack} type="button" onClick={onBack}><Icon name="i-left" size={22} />{t('accounts.signIn.back')}</button>
    <p className={styles.eyebrow}>{serverName}</p>
    <h1>{t('accounts.recovery.title')}</h1>
    <p className={styles.lede}><span className={styles.desktopRecoveryCopy}>{t('accounts.recovery.body')}</span><span className={styles.phoneRecoveryCopy}>{t('accounts.recovery.phoneBody')}</span></p>
    {notice??<AuthError error={error}/>}
    <div className={styles.localRecoveryPanels}>
      <form onSubmit={event => {event.preventDefault(); if (owner && !busy) onWriteFile(owner);}}>
        <h3>{t('accounts.recovery.owner')}</h3>
        <p><span className={styles.desktopRecoveryCopy}>{t('accounts.recovery.ownerBody')}</span><span className={styles.phoneRecoveryCopy}>{t('accounts.recovery.phoneOwnerBody')}</span></p>
        <TextField ref={ownerInput} variant="auth" label={t('accounts.signIn.username')} name="username" autoComplete="username" value={owner} onChange={event => setOwner(event.currentTarget.value)} disabled={busy} required />
        <Button variant="primary" type="submit" loading={busy} disabled={!owner}>{t('accounts.recovery.writeFile')}</Button>
        <p className={styles.localRecoveryQuiet}>{t('accounts.recovery.ownerHelp')}</p>
      </form>
      <div className={styles.localRecoveryMember}>
      <button className={styles.localRecoveryMemberToggle} type="button" aria-expanded={memberOpen} onClick={() => setMemberOpen(value => !value)}><span>{t('accounts.recovery.phoneMember')}<small>{t('accounts.recovery.phoneMemberHelp')}</small></span><Icon name="i-down" size={18} /></button>
      <form data-open={memberOpen || undefined} onSubmit={event => {event.preventDefault(); if (member && code && !busy && passwordStrength(password).acceptable) onMemberReset(member, code, password);}}>
        <h3>{t('accounts.recovery.member')}</h3>
        <p>{t('accounts.recovery.memberBody')}</p>
        <div className={styles.localRecoveryPair}>
          <TextField variant="auth" label={t('accounts.signIn.username')} name="username" autoComplete="username" value={member} onChange={event => setMember(event.currentTarget.value)} disabled={busy} required />
          <TextField variant="auth" label={t('accounts.recovery.code')} name="reset-code" autoComplete="one-time-code" placeholder={t('accounts.recovery.codePlaceholder')} value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} required />
        </div>
        <TextField variant="auth" type="password" label={t('accounts.recovery.newPassword')} name="new-password" autoComplete="new-password" placeholder={t('services.password.minimum')} value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required />
        <Button variant="glass" type="submit" loading={busy} disabled={!member || !code || !passwordStrength(password).acceptable}>{t('accounts.recovery.resetSignIn')}</Button>
        <p className={styles.localRecoveryQuiet}>{t('accounts.recovery.memberHelp')}</p>
      </form>
      </div>
    </div>
    <button type="button" className={styles.localRecoveryBack} onClick={onBack}><Icon name="i-left" size={18} />{t('accounts.recovery.back')}</button>
  </AuthStage>;
}
