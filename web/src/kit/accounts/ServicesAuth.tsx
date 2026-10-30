import {Fragment, type ReactNode} from 'react';
import {createI18n, deviceRegion, parseMessage, resolveRegion, type MessageId} from '@i18n';
import type {IdentityProvider} from '@core/hosted-security.ts';
import {passwordStrength} from '@core/password-strength.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Icon, type IconName} from '../Icon';
import {AuthStage} from './AuthStage';
import {ProfileAvatarFace} from '../shell/AccountSurfaces';
import styles from './services-auth.module.css';

const t = createI18n(resolveRegion(undefined, deviceRegion())).t;
export function ServicesAuthStage({children, variant = 'auth', legal = variant !== 'signup' && variant !== 'providerOnboarding'}: {children: ReactNode; variant?: 'auth' | 'signup' | 'providerOnboarding' | 'reset' | 'state' | 'device' | 'deviceReview' | 'deviceSignIn' | 'ownerReview'; legal?: boolean}) {
  return <AuthStage service legal={legal} shape={variant === 'state' ? 'round' : 'side'} layout={variant === 'state' ? 'center' : 'column'}
    className={styles.stage} contentClassName={[styles.content, styles[variant]].join(' ')}>{children}</AuthStage>;
}
export function ServicesHeading({children, singleLine = false, phoneAccountForm = false}: {children: ReactNode; singleLine?: boolean; phoneAccountForm?: boolean}) {
  return <h1 className={styles.heading} data-single-line={singleLine || undefined} data-phone-account-form={phoneAccountForm || undefined}>{children}</h1>;
}
export function ServicesLead({children}: {children: ReactNode}) { return <p className={styles.lead}>{children}</p>; }
export function ServicesQuiet({children}: {children: ReactNode}) { return <p className={styles.quiet}>{children}</p>; }
export function ServicesNotice({children, tone = 'warning', icon, busy = false}: {children: ReactNode; tone?: 'warning' | 'error' | 'info' | 'success'; icon?: IconName; busy?: boolean}) {
  return <div className={styles.notice} data-tone={tone} role={tone === 'error' ? 'alert' : 'status'} aria-busy={busy || undefined}>
    {busy ? <span className={styles.spinner} aria-hidden="true" /> : <Icon name={icon ?? (tone === 'success' ? 'i-check' : tone === 'info' ? 'i-info' : 'i-warn')} size={20} />}
    <span>{children}</span>
  </div>;
}
export function HostedError({error}: {error: unknown}) {
  if (!error) return null;
  const result = presentError(error, 'portico-account', {operation: 'action', i18n: createI18n(resolveRegion(undefined, deviceRegion()))});
  return result.silent ? null : <ServicesNotice tone="error">{result.body}</ServicesNotice>;
}
export function ServicesStrength({password, id}: {password: string; id: string}) {
  if (!password) return null;
  const strength = passwordStrength(password);
  // Only the two textual strength states drawn in account 9/14/15 are introduced here.
  const label = strength.level === 0 ? t('services.password.tooShort') : strength.level === 4 ? t('services.password.strong') : '';
  return <div id={id} className={styles.strength} data-level={strength.level} aria-live="polite">
    {[1, 2, 3, 4].map(level => <i key={level} data-on={level <= Math.max(1, strength.level) || undefined} aria-hidden="true" />)}
    {label && <span>{label}</span>}
  </div>;
}
export function ServicesActions({children}: {children: ReactNode}) { return <div className={styles.actions}>{children}</div>; }
export function ServicesLabel({children, detail}: {children: ReactNode; detail?: ReactNode}) { return <span className={styles.label}>{children}{detail && <small>{detail}</small>}</span>; }
export function ServicesStateIcon({tone = 'success', icon = 'i-check', size = 34}: {tone?: 'success' | 'warning' | 'error' | 'info'; icon?: IconName; size?: number}) {
  return <span className={styles.stateIcon} data-tone={tone}><Icon name={icon} size={size} /></span>;
}
export function ServicesProviderButton({provider, children, disabled, onClick}: {provider: IdentityProvider; children: ReactNode; disabled?: boolean; onClick: () => void}) {
  return <button type="button" className={styles.provider} data-provider={provider} disabled={disabled} onClick={onClick}><Icon name={provider === 'apple' ? 'i-apple' : 'i-google'} size={20} />{children}</button>;
}
export function ServicesProviderContext({provider, email}: {provider: IdentityProvider; email?: string}) {
  return <span className={[styles.codeChip, styles.providerContext].join(' ')}><Icon name={provider === 'apple' ? 'i-apple' : 'i-google'} size={18} />
    <span>{t('services.provider.heading', {provider: t(provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}{email && <b>{email}</b>}</span>
  </span>;
}
export function ServicesBackAction({children}: {children: ReactNode}) { return <span className={styles.backAction}><Icon name="i-left" size={18} />{children}</span>; }
export function ServicesInvitationCredentials({children}: {children: ReactNode}) { return <div className={styles.invitationCredentials}>{children}</div>; }
export function ServicesProviderAccount({name, username, provider}: {name: string; username: string; provider: IdentityProvider}) {
  return <div className={styles.providerAccount}><ProfileAvatarFace initial={Array.from(name)[0]?.toLocaleUpperCase() ?? ''} size={48} />
    <span><b>{name}</b><small>{username}<i aria-hidden="true" />{t('services.provider.signedIn', {provider: t(provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}</small></span>
  </div>;
}
export function ServicesProviderReturnHint() { return <p className={styles.providerReturnHint}>{t('services.provider.returnHint')}</p>; }
export function ServicesDivider({children}: {children: ReactNode}) { return <div className={styles.divider}>{children}</div>; }
export function ServicesPair({children}: {children: ReactNode}) { return <div className={styles.pair}>{children}</div>; }
export function ServicesHint({children}: {children: ReactNode}) { return <p className={styles.hint}>{children}</p>; }
export function ServicesTerms({provider}: {provider?: IdentityProvider} = {}) {
  const message = t('services.signup.agreement', {terms: '{terms}', privacy: '{privacy}'});
  return <p className={styles.terms}>{provider && <>{t('services.provider.privacy', {provider: t(provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}{' '}</>}{parseMessage(message).map((part, index) => part.kind === 'text' ? <Fragment key={index}>{part.value}</Fragment> : part.kind === 'arg' && part.name === 'terms' ? <a key={index} href="https://getportico.tv/terms">{t('services.signup.terms')}</a> : part.kind === 'arg' && part.name === 'privacy' ? <a key={index} href="https://getportico.tv/privacy">{t('services.signup.privacy')}</a> : null)}</p>;
}
export function ServicesFacts({items}: {items: readonly {id: string; label: ReactNode; value: ReactNode}[]}) {
  return <dl className={styles.facts}>{items.map(item => <div key={item.id} data-fact={item.id}><dt>{item.label}</dt><dd>{item.value}</dd></div>)}</dl>;
}
export function ServicesCodeChip({label, code}: {label: ReactNode; code: string}) {
  return <div className={styles.codeChip}><Icon name="i-tv" size={18} />{label}<b>{code}</b></div>;
}
export function ServicesAccountLine({children}: {children: ReactNode}) { return <p className={styles.accountLine}>{children}</p>; }
export function ServicesBackLink({children, href}: {children: ReactNode; href: string}) { return <div className={styles.backLink}><a href={href}>{children}</a></div>; }
export function ServicesSignedInAccount({username, disabled, onDifferent}: {username: string; disabled?: boolean; onDifferent: () => void}) {
  return <div className={styles.returnAccount}><span className={styles.returnAvatar} aria-hidden="true">{Array.from(username)[0]?.toLocaleUpperCase()}</span>
    <span><b>{t('services.return.signedIn', {username})}</b><small>{t('accounts.invitation.account')}</small></span>
    <button type="button" disabled={disabled} onClick={onDifferent}>{t('accounts.invitation.notYou')}</button></div>;
}
export function ServicesDeviceAccount({name, disabled, onSignOut}: {name: string; disabled?: boolean; onSignOut: () => void}) {
  const message = t('services.device.approving', {name: '{name}'});
  return <ServicesAccountLine>{parseMessage(message).map((part, index) => part.kind === 'text' ? <Fragment key={index}>{part.value}</Fragment> : part.kind === 'arg' && part.name === 'name' ? <b key={index}>{name}</b> : null)}<button type="button" disabled={disabled} onClick={onSignOut}><span className={styles.deviceDesktop}>{t('services.device.notYou')}</span><span className={styles.devicePhone}>{t('services.device.notYouPhone')}</span></button></ServicesAccountLine>;
}
export function ServicesDeviceName({name, television}: {name: string; television: boolean}) {
  return <span className={styles.deviceName}>{television && <span className={styles.deviceIcon}><Icon name="i-tv" size={20} /></span>}{name}</span>;
}
export function ServicesDeviceCode({children}: {children: ReactNode}) { return <span className={styles.deviceCode}>{children}</span>; }
export function ServicesDeviceCodeHelp({fromQR}: {fromQR: boolean}) {
  return <><span className={styles.deviceDesktop}>{t('services.device.codeHelp')}</span><span className={styles.devicePhone}>{t(fromQR ? 'services.device.codeFromQR' : 'services.device.codeHelp')}</span></>;
}
export function ServicesDeviceWarning({children}: {children: ReactNode}) { return <p className={styles.deviceWarning}>{children}</p>; }
export function ServicesDeviceAnother({disabled, onClick}: {disabled?: boolean; onClick: () => void}) { return <div className={styles.deviceAnother}><button type="button" disabled={disabled} onClick={onClick}>{t('web.device.anotherCode')}</button></div>; }
export function ServicesRegistrationLead({address, message: id = 'auth.codeSentTo'}: {address: string; message?: MessageId}) {
  const message = t(id, {address: '{address}'});
  return <ServicesLead>{parseMessage(message).map((part, index) => part.kind === 'text' ? <Fragment key={index}>{part.value}</Fragment> : part.kind === 'arg' && part.name === 'address' ? <b key={index} className={styles.verifiedAddress}>{address}</b> : null)}</ServicesLead>;
}
export function ServicesVerificationCode({children, label = true}: {children: ReactNode; label?: boolean}) { return <div className={styles.verificationCode}>{label && <span aria-hidden="true">{t('auth.code')}</span>}{children}</div>; }
