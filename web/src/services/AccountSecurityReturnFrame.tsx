import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../kit/controls';
import {HostedError, ServicesActions, ServicesAuthStage, ServicesHeading} from '../kit/accounts/ServicesAuth';

const t = defaultI18n.t;

/**
 * ACC-LEAF-24: the fixed entry/return frame for an actual Account Security
 * continuation — the drawn heading, the bounded error line, one content slot and
 * the two actions. Busy disables both actions; Continue appears only when the owner
 * supplies its callback. It owns no state, effect, network, URL, session, proof,
 * retry, timer or copy beyond the existing action labels.
 */
export function AccountSecurityReturnFrame({title, error, busy, children, onCancel, onContinue, continueLabel, continueDisabled = false}: {
  title: string;
  error?: unknown;
  busy: boolean;
  children?: ReactNode;
  onCancel: () => void;
  onContinue?: () => void;
  continueLabel?: string;
  continueDisabled?: boolean;
}) {
  return <ServicesAuthStage><ServicesHeading singleLine>{title}</ServicesHeading>
    <HostedError error={error} />
    {children}
    <ServicesActions>
      <Button size="small" disabled={busy} onClick={onCancel}>{t('action.cancel')}</Button>
      {onContinue && <Button size="small" variant="primary" disabled={busy || continueDisabled} onClick={onContinue}>{continueLabel ?? t('action.continue')}</Button>}
    </ServicesActions>
  </ServicesAuthStage>;
}
