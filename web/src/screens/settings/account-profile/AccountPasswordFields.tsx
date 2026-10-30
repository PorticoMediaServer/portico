import {defaultI18n} from '@i18n';
import {ServicesLabel, ServicesStrength} from '../../../kit/accounts/ServicesAuth';
import {TextField} from '../../../kit/controls';
import styles from './profile.module.css';

const t = defaultI18n.t;

/**
 * SET-4AA18AB: the account-profile Sign-in password proof, as fields only.
 * The current password is always asked; the new password carries the drawn rule
 * and strength meter; the code appears only while two-step is on and accepts a
 * recovery code as readily as an authenticator one, so it is a plain text field.
 * No dialog, state, effects, request or validation authority: the caller owns the
 * original opening, the request and clearing the secrets.
 */
export function AccountPasswordFields({currentPassword, newPassword, code, twoFactor, disabled, onCurrentPasswordChange, onNewPasswordChange, onCodeChange}: {
  currentPassword: string;
  newPassword: string;
  code: string;
  twoFactor: boolean;
  disabled: boolean;
  onCurrentPasswordChange: (value: string) => void;
  onNewPasswordChange: (value: string) => void;
  onCodeChange: (value: string) => void;
}) {
  return <div className={styles.fields}>
    <TextField label={t('account.currentPassword')} name="current-password" type="password" autoComplete="current-password"
      value={currentPassword} disabled={disabled} onChange={event => onCurrentPasswordChange(event.currentTarget.value)} />
    <div>
      <TextField label={<ServicesLabel detail={t('services.password.minimum')}>{t('auth.newPassword')}</ServicesLabel>} name="new-password" type="password" autoComplete="new-password"
        value={newPassword} disabled={disabled} onChange={event => onNewPasswordChange(event.currentTarget.value)} />
      <ServicesStrength password={newPassword} id="account-profile-strength" />
    </div>
    {twoFactor && <TextField label={<ServicesLabel detail={t('services.security.twoStepOnHint')}>{t('services.security.authenticatorCode')}</ServicesLabel>} name="code" autoComplete="one-time-code"
      value={code} disabled={disabled} onChange={event => onCodeChange(event.currentTarget.value)} />}
  </div>;
}
