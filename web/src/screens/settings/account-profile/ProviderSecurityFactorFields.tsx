import {defaultI18n} from '@i18n';
import {ServicesLabel} from '../../../kit/accounts/ServicesAuth';
import {TextField} from '../../../kit/controls';
import styles from './profile.module.css';

const t = defaultI18n.t;

/**
 * ACC-LEAF-23: the actual second-factor field a provider continuation asks for
 * when the original transaction says mfaRequired. Plain text with one-time-code
 * autocomplete, so a recovery code is as acceptable as an authenticator code; no
 * password field, no six-digit rewrite, validation, state, effect, resend, API or
 * callback wrapping. The owner's real CompleteSecurity checks stay theirs.
 */
export function ProviderSecurityFactorFields({value, disabled, onChange}: {value: string; disabled: boolean; onChange: (value: string) => void}) {
  return <div className={styles.fields}>
    <TextField label={<ServicesLabel detail={t('services.security.twoStepOnHint')}>{t('services.security.authenticatorCode')}</ServicesLabel>}
      name="code" autoComplete="one-time-code" value={value} disabled={disabled} onChange={event => onChange(event.currentTarget.value)} />
  </div>;
}
