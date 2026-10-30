import {useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {Button} from '../controls/Button';
import {TextField} from '../controls/Fields';
import {Icon, type IconName} from '../Icon';
import {AuthError, AuthLinks, AuthStage} from './AuthStage';
import styles from './accounts.module.css';

const t = defaultI18n.t;
export type LocalInvitationFormProps = Readonly<{
  serverName: string; inviter?: string; libraries: readonly string[]; busy?: boolean; error?: unknown; errorContent?: ReactNode;
  initialValues?: Readonly<{name?: string; username?: string; password?: string; confirmation?: string}>;
  onJoin: (username: string, password: string, name: string) => void; onBack: () => void; bare?: boolean;
}>;
/** Auth 12. The plain password fields are deliberate: this frame has no reveal action. */
export function LocalInvitationForm({serverName, inviter, libraries, busy, error, errorContent, initialValues, onJoin, onBack, bare = false}: LocalInvitationFormProps) {
  const [name, setName] = useState(initialValues?.name ?? ''), [username, setUsername] = useState(initialValues?.username ?? '');
  const [password, setPassword] = useState(initialValues?.password ?? ''), [confirmation, setConfirmation] = useState(initialValues?.confirmation ?? '');
  const [confirmed, setConfirmed] = useState(false);
  const [changedUsername, setChangedUsername] = useState(false);
  const usernameTaken = Boolean(error && typeof error === 'object' && 'code' in error && error.code === 'username_taken');
  const strength = passwordStrength(password), trimmed = username.trim();
  const valid = trimmed.length >= 3 && trimmed.length <= 64 && !/[\s@]/.test(trimmed) && strength.acceptable && password === confirmation;
  const mismatch = confirmed && confirmation && password !== confirmation ? t('web.directJoin.mismatch') : undefined;
  // Auth12's enumeration has no serial comma. Keep its conjunction in the
  // catalogue; library names remain the server's actual data.
  const sharedLibraries = libraries.length < 2 ? libraries[0] ?? '' : t('accounts.invitation.libraryList', {head: libraries.slice(0, -1).join(', '), last: libraries[libraries.length - 1]});
  // These three labels are drawn. The remaining computed levels await exact copy;
  // never relabel a weak password as Good or Strong, and never gate on advice.
  const strengthLabel = strength.label === 'Good' ? t('accounts.password.good') : strength.label === 'Strong' ? t('services.password.strong') : strength.label === 'Too short' ? t('services.password.tooShort') : undefined;
  return <AuthStage phoneShape="top" bare={bare} className={styles.localInvitationStage} contentClassName={styles.invitationContent}>
    <p className={styles.eyebrow}>{t('accounts.invitation.to')}</p><h1>{serverName}</h1>
    <p className={styles.lede}><span className={styles.desktopInvitationBody}>{t('accounts.invitation.localBody')}</span><span className={styles.phoneInvitationBody}>{t('accounts.invitation.localPhoneBody', {server: serverName})}</span></p>
    <div className={styles.phoneInvitationLibraries}><InvitationLibraries libraries={libraries.map(name => ({name}))} /></div>
    {inviter && <div className={styles.invitedBy}><span className={styles.inviterAvatar} aria-hidden="true">{Array.from(inviter)[0]}</span>
      <span><b>{inviter}</b>{' '}{t('accounts.invitation.invitedYou')}{libraries.length > 0 && <><i className={styles.invitationGap} aria-hidden="true" />{sharedLibraries}</>}</span>
    </div>}
    {errorContent ?? <AuthError error={usernameTaken ? undefined : error} />}
    <form className={[styles.form, styles.invitationForm].join(' ')} onSubmit={event => {event.preventDefault(); setConfirmed(true); setChangedUsername(false); if (valid && !busy) onJoin(trimmed, password, name.trim());}}>
      <TextField variant="auth" containerClassName={styles.invitationName} label={<>{t('web.directJoin.name')}{' '}<small>{t('field.optional')}</small></>} name="name" autoComplete="name" description={t('web.directJoin.nameHelp')} value={name} onChange={event => setName(event.currentTarget.value)} disabled={busy} />
      <TextField variant="auth" label={t('web.directJoin.username')} name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} description={usernameTaken && !changedUsername ? undefined : t('web.directJoin.usernameHelp')} error={usernameTaken && !changedUsername ? t('web.directJoin.error.taken') : undefined} value={username} onChange={event => {setUsername(event.currentTarget.value); setChangedUsername(true);}} disabled={busy} required minLength={3} maxLength={64} pattern={'[^\\s@]{3,64}'} />
      <div className={styles.invitationPair}>
        <div className={styles.invitationPassword}>
          <TextField variant="auth" label={t('web.directJoin.password')} name="password" type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required />
          {password && <div className={styles.passwordStrength} data-level={strength.level}>{Array.from({length: 4}, (_, index) => <i key={index} data-on={index < Math.max(1, strength.level) || undefined} aria-hidden="true" />)}{strengthLabel && <span>{strengthLabel}</span>}</div>}
        </div>
        <TextField variant="auth" label={<><span className={styles.desktopInvitationBody}>{t('web.directJoin.confirm')}</span><span className={styles.phoneInvitationBody}>{t('accounts.signIn.confirm')}</span></>} name="confirmation" type="password" autoComplete="new-password" value={confirmation} onBlur={() => setConfirmed(true)} onChange={event => {setConfirmation(event.currentTarget.value); if (confirmed) setConfirmed(false);}} error={mismatch} disabled={busy} required />
      </div>
      <Button className={styles.invitationSubmit} variant="primary" type="submit" loading={busy} disabled={!valid}>{t('web.directJoin.submit')}</Button>
    </form>
    <AuthLinks><button type="button" data-quiet onClick={onBack}>{t('web.hostedAccount.back')}</button></AuthLinks>
  </AuthStage>;
}

/** A kind-specific glyph is supplied only from an actual library kind. A name
 * alone never becomes a movie/TV/category claim. */
export function PorticoInvitation({serverName, libraries, member, account, signedOutContent, busy, error, errorContent, onJoin, onDifferentAccount, onCancel, bare = false, compact = false}: {
  serverName: string; libraries: readonly Readonly<{name: string; icon?: IconName}>[]; member: boolean;
  account?: Readonly<{username: string}>; signedOutContent: ReactNode; busy?: boolean; error?: unknown; errorContent?: ReactNode;
  onJoin: () => void; onDifferentAccount: () => void; onCancel?: () => void; bare?: boolean;
  /** Connect 27's dialog: a compact server preview and one action row. The page
   * presentation stays the default for every other caller. */
  compact?: boolean;
}) {
  return <AuthStage shape="top" legal={false} bare={bare} className={styles.porticoInvitationStage} contentClassName={styles.porticoInvitationContent}
    topRight={!compact && account && <span className={styles.invitationHeaderAvatar} aria-hidden="true">{Array.from(account.username)[0]?.toUpperCase()}</span>}>
    {compact ? <div className={styles.invitationPreview}><span className={styles.invitationPreviewIcon}><Icon name="i-server" size={26} /></span>
      <span><b>{serverName}</b><small>{t('accounts.invitation.invitedYouToJoin')}</small></span></div>
      : <><p className={styles.eyebrow}>{t('accounts.invitation.to')}</p><h1>{serverName}</h1>
        <p className={styles.lede}>{t('accounts.invitation.porticoBody', {server: serverName})}</p></>}
    <InvitationLibraries libraries={libraries} member={member} />
    {errorContent ?? <AuthError error={error} />}
    {!account ? signedOutContent : compact
      ? <div className={styles.invitationCompactActions}>
        <Button variant="primary" loading={busy} onClick={onJoin}>{t('accounts.invitation.joinAs', {username: account.username})}</Button>
        {onCancel && <Button onClick={onCancel} disabled={busy}>{t('action.cancel')}</Button>}
      </div>
      : <><div className={styles.invitationAccount}><span className={styles.invitationAccountAvatar} aria-hidden="true">{Array.from(account.username)[0]?.toUpperCase()}</span>
        <span><b>{t('accounts.invitation.joinAs', {username: account.username})}</b><small>{t('accounts.invitation.account')}</small></span>
        <button type="button" onClick={onDifferentAccount} disabled={busy}>{t('accounts.invitation.notYou')}</button>
      </div><Button variant="primary" loading={busy} onClick={onJoin}>{t('accounts.invitation.join', {server: serverName})}</Button>
        <p className={styles.invitationQuiet}>{t('accounts.invitation.porticoJoinedHelp', {server: serverName})}</p></>}
  </AuthStage>;
}

function InvitationLibraries({libraries, member = false}: {libraries: readonly Readonly<{name: string; icon?: IconName}>[]; member?: boolean}) {
  return <div className={styles.invitationLibraries}><span>{t('accounts.invitation.libraries')}</span><div>{libraries.map((library, index) => <span key={index}>{library.icon && <Icon name={library.icon} size={16} />}{library.name}</span>)}</div>
    {member && <p>{t('accounts.invitation.member')}</p>}
  </div>;
}

export function InvitationUnavailable({otherAccount = false, onBack, onDifferentAccount, bare = false}: {otherAccount?: boolean; onBack: () => void; onDifferentAccount: () => void; bare?: boolean}) {
  return <AuthStage shape="top" legal={false} bare={bare} className={styles.invitationFailureStage} contentClassName={styles.invitationFailureContent}>
    <div className={styles.invitationFailureIcon}><Icon name={otherAccount ? 'i-userx' : 'i-clock'} size={34} /></div>
    <h2>{t(otherAccount ? 'accounts.invitation.otherAccountTitle' : 'accounts.invitation.expiredTitle')}</h2>
    <p>{t(otherAccount ? 'web.directJoin.forAccount' : 'accounts.invitation.expiredBody')}</p>
    {otherAccount && <Button variant="primary" onClick={onDifferentAccount}>{t('accounts.move.differentAccount')}</Button>}
    <Button variant="glass" onClick={onBack}>{t('web.hostedAccount.back')}</Button>
    {!otherAccount && <small>{t('accounts.invitation.expiryHelp')}</small>}
  </AuthStage>;
}
