import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {StateLayout} from '../../../kit/states';
import {Button, TextField, FormError} from '../../../kit/controls';
import {PhonePINEntry} from '../../../kit/accounts/PhonePINEntry';
import {ProviderButton} from '../../../kit/accounts/ProviderButton';
import {RecoveryFields, RecoveryNote} from '../../../kit/accounts/RecoveryParts';
import type {NativePorticoRecovery, NativeProfileRecovery} from '../../../platform/profile-recovery';
import {useProfileRecovery} from '../../shared/accounts/useProfileRecovery';

const t = defaultI18n.t;

/** Forgot PIN on a phone (Auth18): prove the account (its password, a code, or the Portico Account's password or provider),
 * then choose the new PIN. The controller owns every credential; this page shows the current step. */
export function ProfileRecoveryPage({controller, profileName, username, onOpen, onClose, portico}: {
  controller: NativeProfileRecovery; profileName: string; username: string; onOpen: (pin: string) => void; onClose: () => void;
  /** Test seam: the signed-in Portico Account for the Portico step. */
  portico?: () => Promise<NativePorticoRecovery>;
}) {
  const recovery = useProfileRecovery(controller, portico), {state} = recovery;
  const error = recovery.error !== undefined ? <FormError placement="block">{recovery.error}</FormError> : null;
  if (state.phase === 'done') return <StateLayout testID="accounts-pin-recovery-done" title={t('profile.pinChanged')} body={t('profile.pinSavedBody', {name: profileName})}
    beforeActions={error} primaryAction={{id: 'open-profile', label: t('accounts.profiles.open', {profile: profileName}), onPress: () => onOpen(recovery.pin)}}
    secondaryAction={{id: 'done', label: t('action.done'), onPress: onClose}}/>;
  if (state.phase === 'pin') return <StateLayout testID="accounts-pin-recovery-pin" title={t('profile.pinRecoveryTitle', {name: profileName})}
    beforeActions={<RecoveryFields>{error}<PhonePINEntry label={t('profile.pinChoose')} value={recovery.pin} onChange={recovery.setPin} busy={state.busy}
      onComplete={recovery.setPinNow}/></RecoveryFields>}
    primaryAction={{id: 'set-pin', label: t('profile.pinRecoveryAction'), disabled: state.busy || recovery.pin.length !== 4, loading: state.busy, onPress: () => recovery.setPinNow(recovery.pin)}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
  if (state.phase === 'challenge') return <StateLayout testID="accounts-pin-recovery-code" title={t('accounts.profiles.forgotTitle', {profile: profileName})}
    beforeActions={<RecoveryFields>{error}<TextField label={t('profile.pinRecoveryCode')} value={recovery.code} onChangeText={recovery.setCode} autoCapitalize="characters" autoCorrect={false} editable={!state.busy}/></RecoveryFields>}
    primaryAction={{id: 'verify-code', label: t('accounts.signIn.continue'), disabled: state.busy || !recovery.code.trim(), loading: state.busy, onPress: recovery.sendCode}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
  if (state.phase === 'portico') {
    const title = t('accounts.profiles.forgotTitle', {profile: profileName}), cancel = {id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose};
    if (recovery.porticoFailure !== undefined) return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('accounts.profiles.forgotBody')}
      beforeActions={<FormError placement="block">{recovery.porticoFailure}</FormError>}
      primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: recovery.retryPortico}} secondaryAction={cancel}/>;
    const account = recovery.account;
    if (recovery.porticoLoading || !account) return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('status.loading')} secondaryAction={cancel}/>;
    const providerError = (key: Parameters<typeof t>[0]) => <FormError placement="inline">{t(key)}</FormError>;
    if (recovery.providerStep) {
      // The account signs in only with Google or Apple: a fresh sign-in with one proves it's them.
      if (recovery.codeAsk) return <StateLayout testID="accounts-pin-recovery-portico-provider-code" title={title} body={t('accounts.profiles.porticoProviderChecked')}
        beforeActions={<RecoveryFields>
          <TextField label={t('accounts.profiles.porticoTwoStep')} value={recovery.twoStep} onChangeText={recovery.setTwoStep} keyboardType="number-pad" autoComplete="one-time-code" maxLength={6}/></RecoveryFields>}
        primaryAction={{id: 'provider-code', label: t('accounts.signIn.continue'), disabled: recovery.twoStep.trim().length !== 6, onPress: recovery.sendProviderCode}}
        secondaryAction={{id: 'cancel', label: t('action.cancel'), onPress: () => recovery.cancelProviderCode(onClose)}}/>;
      return <StateLayout testID="accounts-pin-recovery-portico-provider" title={title} body={t('accounts.profiles.porticoProviderLead', {username: account.username})}
        beforeActions={<RecoveryFields gap="tight">
          {recovery.providerSentence ? providerError(recovery.providerSentence) : error}
          {recovery.providers.map(provider => <ProviderButton key={provider} provider={provider} busy={state.busy}
            testID={`accounts-pin-recovery-${provider}`} label={t(provider === 'google' ? 'accounts.profiles.porticoProviderGoogle' : 'accounts.profiles.porticoProviderApple')}
            onPress={() => recovery.startProvider(provider)}/>)}
          <RecoveryNote>{t('accounts.profiles.porticoProviderChecked')}</RecoveryNote>
          <Button variant="link" disabled={state.busy} onPress={recovery.openSetPassword}>{t('accounts.profiles.porticoSetPassword')}</Button>
        </RecoveryFields>}
        secondaryAction={cancel}/>;
    }
    if (recovery.passwordNeeded) return <StateLayout testID="accounts-pin-recovery-portico-password-needed" title={title} body={t('accounts.profiles.porticoNeedsPassword')}
      primaryAction={{id: 'set-password', label: t('accounts.profiles.porticoSetPassword'), onPress: recovery.openSetPassword}} secondaryAction={cancel}/>;
    return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('accounts.profiles.forgotBody')}
      beforeActions={<RecoveryFields gap="loose">{recovery.refused === 'account_password_exists' ? providerError('accounts.profiles.porticoPasswordExists') : error}
        <TextField label={t('accounts.profiles.porticoPassword', {username: account.username})} value={recovery.password} onChangeText={recovery.setPassword} secureTextEntry autoComplete="current-password" editable={!state.busy}/>
        {account.twoStep && <TextField label={t('accounts.profiles.porticoTwoStep')} value={recovery.twoStep} onChangeText={recovery.setTwoStep} keyboardType="number-pad" autoComplete="one-time-code" maxLength={6} editable={!state.busy}/>}
        <RecoveryNote>{t('accounts.profiles.porticoChecked')}</RecoveryNote></RecoveryFields>}
      primaryAction={{id: 'prove-portico', label: t('accounts.signIn.continue'), disabled: state.busy || !recovery.porticoReady, loading: state.busy, onPress: recovery.sendPorticoPassword}}
      secondaryAction={cancel}/>;
  }
  return <StateLayout testID="accounts-pin-recovery-password" title={t('accounts.profiles.forgotTitle', {profile: profileName})} body={t('accounts.profiles.forgotBody')}
    beforeActions={<RecoveryFields>{error}<TextField label={t('accounts.profiles.accountPassword', {username})} value={recovery.password} onChangeText={recovery.setPassword}
      secureTextEntry autoComplete="current-password" editable={!state.busy}/></RecoveryFields>}
    primaryAction={{id: 'prove-account', label: t('accounts.signIn.continue'), disabled: state.busy || !recovery.password, loading: state.busy, onPress: recovery.sendPassword}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
}
