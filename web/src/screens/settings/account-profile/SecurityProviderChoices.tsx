import {defaultI18n} from '@i18n';
import {ServicesProviderButton} from '../../../kit/accounts/ServicesAuth';
import styles from './profile.module.css';

const t = defaultI18n.t;

/**
 * Root70: the linked provider identities an account may choose between, in the
 * drawn services.provider wording. It renders only the supplied actual links —
 * no invented provider, email or account lookup, href, location, popup, state,
 * effect, API or implicit selection. The owner's callback binds the exact
 * provider ID and revision together with the original Security intent.
 */
export function SecurityProviderChoices({providers, disabled, onChoose}: {
  providers: readonly {id: string; provider: 'apple' | 'google'}[];
  disabled: boolean;
  onChoose: (id: string) => void;
}) {
  return <div className={styles.fields}>
    {providers.map(link => <ServicesProviderButton key={link.id} provider={link.provider} disabled={disabled} onClick={() => onChoose(link.id)}>
      {t(link.provider === 'apple' ? 'services.provider.apple' : 'services.provider.google')}
    </ServicesProviderButton>)}
  </div>;
}
