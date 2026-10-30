import {useId} from 'react';
import {defaultI18n} from '@i18n';
import {ServicesLabel, ServicesStrength} from '../../../kit/accounts/ServicesAuth';
import {AuthPasswordField} from '../../../kit/accounts/AuthStage';
import {TextField} from '../../../kit/controls';
import styles from './profile.module.css';

const t = defaultI18n.t;

/**
 * Root70: the fields a provider-only account shows when it adds a local password.
 * There is deliberately no current-password field — the owner mounts this only
 * after the real original provider recent-proof — and the component authorizes
 * nothing: it holds no validation, intent, proof, API, state or effect.
 */
export function ProviderPasswordFields({newPassword, factor, twoFactor, disabled, onNewPasswordChange, onFactorChange}: {
  newPassword: string;
  factor: string;
  twoFactor: boolean;
  disabled: boolean;
  onNewPasswordChange: (value: string) => void;
  onFactorChange: (value: string) => void;
}) {
  const strengthId = useId();
  return <div className={styles.fields}>
    <div>
      <AuthPasswordField label={<ServicesLabel detail={t('services.password.minimum')}>{t('auth.newPassword')}</ServicesLabel>} name="new-password" autoComplete="new-password"
        value={newPassword} disabled={disabled} onChange={event => onNewPasswordChange(event.currentTarget.value)} aria-describedby={strengthId} />
      <ServicesStrength password={newPassword} id={strengthId} />
    </div>
    {twoFactor && <TextField label={<ServicesLabel detail={t('services.security.twoStepOnHint')}>{t('services.security.authenticatorCode')}</ServicesLabel>} name="code" autoComplete="one-time-code"
      value={factor} disabled={disabled} onChange={event => onFactorChange(event.currentTarget.value)} />}
  </div>;
}
