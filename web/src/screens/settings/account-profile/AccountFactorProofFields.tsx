import {defaultI18n} from '@i18n';
import {TextField} from '../../../kit/controls';
import styles from './profile.module.css';

const t = defaultI18n.t;

/**
 * SET-4AA18AB: the proof an existing factor asks for before it is turned off or
 * confirmed — the current password and one authenticator or recovery code, as
 * fields only. Plain text for the code, so a recovery code is accepted as well as
 * a six-digit one. No dialog, state, effects, API, permission or controller: the
 * caller owns clearing and the action itself.
 */
export function AccountFactorProofFields({password, code, disabled, onPasswordChange, onCodeChange}: {
  password: string;
  code: string;
  disabled: boolean;
  onPasswordChange: (value: string) => void;
  onCodeChange: (value: string) => void;
}) {
  return <div className={styles.fields}>
    <TextField label={t('account.currentPassword')} name="current-password" type="password" autoComplete="current-password"
      value={password} disabled={disabled} onChange={event => onPasswordChange(event.currentTarget.value)} />
    <TextField label={t('services.security.authenticatorCode')} name="code" autoComplete="one-time-code"
      value={code} disabled={disabled} onChange={event => onCodeChange(event.currentTarget.value)} />
  </div>;
}
