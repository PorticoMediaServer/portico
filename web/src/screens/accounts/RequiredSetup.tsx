import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {AuthError, AuthStage} from '../../kit/accounts';
import {RequiredRecoveryCodesDialog, RequiredTwoStepDialog} from '../../kit/accounts/RequiredSetup';
import {Button} from '../../kit/controls';
import type {RequiredFactorForm} from './required-factor';

/** The account screen retains this original form through confirmation and the
 * once-only code page, even when entry moves to profiles. Creating/enrolling
 * the form belongs to the actual sign-in gesture, never a render or import. */
export function RequiredFactorDialogs({form, error, onCancel, onDone}: {
  form: RequiredFactorForm; error?: unknown; onCancel: () => void; onDone: () => void;
}) {
  const state = useSyncExternalStore(form.subscribe, form.getSnapshot);
  if (state.phase === 'code' && state.enrolment) return <RequiredTwoStepDialog enrolment={state.enrolment} busy={state.busy} error={state.error ?? error}
    onConfirm={code => void form.confirm(code)} onCancel={() => {form.close(); onCancel();}} />;
  if (state.phase === 'recovery' && state.codes) return <RequiredRecoveryCodesDialog codes={state.codes} onDone={() => {if (form.done()) onDone();}} />;
  if (state.phase === 'closed') return null;
  return <AuthStage legal={false}><AuthError error={state.error ?? error} />
    {!state.busy && Boolean(state.error ?? error) && <Button onClick={() => void form.enrol()}>{defaultI18n.t('action.tryAgain')}</Button>}
  </AuthStage>;
}
