import {defaultI18n} from '@i18n';
import {TextField} from '../../../kit/controls';
import {ServicesQuiet} from '../../../kit/accounts/ServicesAuth';
import {SecurityProviderChoices} from './SecurityProviderChoices';
import styles from './profile.module.css';

const t = defaultI18n.t;

type Purpose = 'password_set_or_change' | 'email_change' | 'mfa_enroll' | 'mfa_disable' | 'recovery_codes';

const explanation: Partial<Record<Purpose, Parameters<typeof t>[0]>> = {
  password_set_or_change: 'web.hostedSecurity.othersSignedOut',
  email_change: 'services.security.emailPending',
  mfa_disable: 'services.security.disableBody',
};

/**
 * ACC-LEAF-22: what a provider Security continuation asks for before the original
 * target is captured — the drawn explanation for this task, the new email when the
 * task changes it, and every actual linked provider the caller supplies. The two
 * optional disables let the owner disable one side without disabling the other: an
 * invalid provider choice must not lock the email field, and a captured/frozen
 * target must not forbid retrying the same provider. It owns no dialog, footer,
 * state, effect, IO, proof, validation, authority or navigation.
 */
export function ProviderSecurityPrepareFields({task, email, providers, disabled, emailDisabled = false, providerDisabled = false, onEmailChange, onChooseProvider}: {
  task: Purpose;
  email: string;
  providers: readonly {id: string; provider: 'apple' | 'google'}[];
  disabled: boolean;
  emailDisabled?: boolean;
  providerDisabled?: boolean;
  onEmailChange: (value: string) => void;
  onChooseProvider: (id: string) => void;
}) {
  const note = explanation[task];
  return <div className={styles.fields}>
    {note && <ServicesQuiet>{t(note)}</ServicesQuiet>}
    {task === 'email_change' && <TextField label={t('web.hostedSecurity.newEmail')} name="email" type="email" autoComplete="email"
      description={t('services.security.emailCode')} value={email} disabled={disabled || emailDisabled}
      onChange={event => onEmailChange(event.currentTarget.value)} />}
    <SecurityProviderChoices providers={providers} disabled={disabled || providerDisabled} onChoose={onChooseProvider} />
  </div>;
}
