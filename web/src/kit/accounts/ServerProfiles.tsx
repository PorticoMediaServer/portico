import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {colors, profileArtColors} from '@design';
import type {ProfilePickerProfile} from '../artwork/ProfilePicker';
import {ProfileFace, ProfilePicker} from '../artwork/ProfilePicker';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import {AuthCodeBoxes, AuthError, AuthStage} from './AuthStage';
import {usePINFeedback} from './pin-feedback';
import styles from './accounts.module.css';

const t = defaultI18n.t;
// Auth's approved blue face is brighter than the compact account avatar.
const profileTint = (profile: ProfilePickerProfile) => profile.art === 'blue' ? colors.accountAvatar1Start : undefined;
export function ServerProfiles({serverName, profiles, onSelect, onSwitchServer, error, autoFocusId}: {
  serverName: string; profiles: readonly ProfilePickerProfile[]; onSelect: (profile: ProfilePickerProfile) => void; onSwitchServer: () => void; error?: unknown; autoFocusId?: string;
}) {
  // Every same-account profile supplied by this server can be selected. Library
  // scope applies inside the profile; it is never a picker restriction.
  return <AuthStage shape="round" phoneShape="top" className={styles.profilesStage} layout="center" legal={false} topRight={<Button icon="i-server" size="small" onClick={onSwitchServer}>{t('settings.switchServer')}</Button>}>
    <h1 className={styles.pickerTitle}>{t('accounts.profiles.onServer', {server: serverName})}</h1>
    <AuthError error={error} /><ProfilePicker profiles={profiles} onSelect={onSelect} autoFocusId={autoFocusId} getProfileTint={profileTint} layout="auto" />
  </AuthStage>;
}
export function ServerProfilePIN({serverName, profile, onComplete, onOtherProfiles, onForgotPIN, onSwitchServer, remember, onRememberChange, error, errorText, rejectionKey, failure, busy, initialPIN = '', autoFocus = true}: {
  serverName: string; profile: ProfilePickerProfile; onComplete: (pin: string) => void; onOtherProfiles: () => void; onForgotPIN: () => void; onSwitchServer: () => void;
  remember: boolean; onRememberChange: (value: boolean) => void; error?: boolean; errorText?: ReactNode; rejectionKey?: unknown; failure?: unknown; busy?: boolean; initialPIN?: string; autoFocus?: boolean;
}) {
  const {pin, change} = usePINFeedback(initialPIN, error || errorText || failure ? rejectionKey ?? failure ?? true : undefined);
  const context = t('accounts.profiles.serverContext', {server: '\uFFFC'}).split('\uFFFC');
  return <AuthStage shape="round" phoneShape="top" className={styles.pinStage} layout="center" legal={false} topRight={<>
    <span className={styles.serverContext}>{context[0]}<b>{serverName}</b>{context[1]}</span><Button icon="i-server" size="small" onClick={onSwitchServer}>{t('settings.switchServer')}</Button>
  </>}><button type="button" className={styles.phoneProfileBack} onClick={onOtherProfiles}><Icon name="i-left" size={22} />{t('profiles.title')}</button><div className={styles.pin}>
    <div className={styles.desktopFace}><ProfileFace initial={Array.from(profile.name.trim())[0] ?? ''} art={profile.art && Object.hasOwn(profileArtColors, profile.art) ? profile.art as keyof typeof profileArtColors : 'blue'} tint={profileTint(profile)} locked size={148} /></div>
    <div className={styles.phoneFace}><ProfileFace initial={Array.from(profile.name.trim())[0] ?? ''} art={profile.art && Object.hasOwn(profileArtColors, profile.art) ? profile.art as keyof typeof profileArtColors : 'blue'} tint={profileTint(profile)} size={84} /></div>
    <h2 className={styles.desktopPINHeading}>{profile.name}</h2><h1 className={styles.phonePINHeading}>{t('accounts.profiles.pin')}</h1><p className={styles.desktopPINHeading}>{t('accounts.profiles.pinForProfile', {profile: profile.name})}</p><p className={styles.phonePINHeading}>{profile.name}</p>
    <AuthError error={failure} />
    <label className={styles.phonePINLabel}>{t('accounts.profiles.pinLabel')}</label>
    <AuthCodeBoxes length={4} value={pin} label={t('accounts.profiles.pinLabel')} onChange={change} autoFocus={autoFocus} disabled={busy}
      error={errorText ?? (error && t('accounts.profiles.pinFailed'))} onComplete={onComplete} />
    <label className={styles.remember}><input type="checkbox" checked={remember} onChange={event => onRememberChange(event.currentTarget.checked)} />{t('web.direct.rememberProfile')}</label>
    <div className={styles.pinActions}><Button size="small" icon="i-left" onClick={onOtherProfiles}>{t('web.direct.otherProfiles')}</Button><Button variant="ghost" size="small" onClick={onForgotPIN}>{t('pin.forgot')}</Button></div>
  </div></AuthStage>;
}
