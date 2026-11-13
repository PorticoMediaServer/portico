import React, {useEffect, useState} from 'react';
import {Linking, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {StateLayout} from '../../kit/states';
import {Button, TextField, FormError} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {nativePorticoRecovery, type NativePorticoRecovery, type NativeProfileRecovery} from '../../platform/profile-recovery';
import {hostedUrl} from '../../platform/account-authority';
import {PhonePINEntry} from './PhonePINEntry';

const t = defaultI18n.t;

/** Auth18 on a phone. The controller owns every credential and the temporary proof
 * family; this presenter only shows the current phase and reports the chosen PIN. */
export function PhoneProfileRecovery({controller, profileName, username, onOpen, onClose, portico: preparePortico = nativePorticoRecovery}: {
  controller: NativeProfileRecovery; profileName: string; username: string; onOpen: (pin: string) => void; onClose: () => void;
  /** Test seam: the signed-in Portico Account for the Portico step. */
  portico?: () => Promise<NativePorticoRecovery>;
}) {
  const theme = useTheme(), [state, setState] = useState(controller.getSnapshot());
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [pin, setPin] = useState(''), [twoStep, setTwoStep] = useState('');
  // On a Portico Accounts server the password is the Portico Account's, typed here and checked by Portico Services.
  const [portico, setPortico] = useState<Readonly<{phase: 'loading'} | {phase: 'ready'; value: NativePorticoRecovery} | {phase: 'failed'; error: unknown}>>(), [attempt, setAttempt] = useState(0);
  const porticoStep = state.phase === 'portico';
  useEffect(() => {
    if (!porticoStep) return;
    let live = true; setPortico({phase: 'loading'});
    preparePortico().then(value => {if (live) setPortico({phase: 'ready', value});}, error => {if (live) setPortico({phase: 'failed', error});});
    return () => {live = false;};
  }, [porticoStep, attempt, preparePortico]);
  useEffect(() => {
    const off = controller.subscribe(() => setState(controller.getSnapshot()));
    return () => {off(); controller.dispose();};
  }, [controller]);
  const error = state.error ? <FormError placement="block">{presentError(state.error, 'profiles').body}</FormError> : null;
  if (state.phase === 'done') return <StateLayout testID="accounts-pin-recovery-done" title={t('profile.pinChanged')} body={t('profile.pinSavedBody', {name: profileName})}
    beforeActions={error} primaryAction={{id: 'open-profile', label: t('accounts.profiles.open', {profile: profileName}), onPress: () => onOpen(pin)}}
    secondaryAction={{id: 'done', label: t('action.done'), onPress: onClose}}/>;
  if (state.phase === 'pin') return <StateLayout testID="accounts-pin-recovery-pin" title={t('profile.pinRecoveryTitle', {name: profileName})}
    beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 12}}>{error}<PhonePINEntry label={t('profile.pinChoose')} value={pin} onChange={setPin} busy={state.busy}
      onComplete={value => {void controller.reset(value);}}/></View>}
    primaryAction={{id: 'set-pin', label: t('profile.pinRecoveryAction'), disabled: state.busy || pin.length !== 4, loading: state.busy, onPress: () => {void controller.reset(pin);}}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
  if (state.phase === 'challenge') return <StateLayout testID="accounts-pin-recovery-code" title={t('accounts.profiles.forgotTitle', {profile: profileName})}
    beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 12}}>{error}<TextField label={t('profile.pinRecoveryCode')} value={code} onChangeText={setCode} autoCapitalize="characters" autoCorrect={false} editable={!state.busy}/></View>}
    primaryAction={{id: 'verify-code', label: t('accounts.signIn.continue'), disabled: state.busy || !code.trim(), loading: state.busy,
      onPress: () => {const value = code; setCode(''); void controller.code(value);}}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
  if (state.phase === 'portico') {
    const title = t('accounts.profiles.forgotTitle', {profile: profileName}), cancel = {id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose};
    if (!portico || portico.phase === 'loading') return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('status.loading')} secondaryAction={cancel}/>;
    if (portico.phase === 'failed') return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('accounts.profiles.forgotBody')}
      beforeActions={<FormError placement="block">{presentError(portico.error, 'profiles').body}</FormError>}
      primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: () => setAttempt(value => value + 1)}} secondaryAction={cancel}/>;
    const {account, source} = portico.value;
    const refusedForPassword = (state.error as {code?: unknown} | undefined)?.code === 'account_password_required';
    if (!account.hasPassword || refusedForPassword) return <StateLayout testID="accounts-pin-recovery-portico-password-needed" title={title} body={t('accounts.profiles.porticoNeedsPassword')}
      primaryAction={{id: 'set-password', label: t('accounts.profiles.porticoSetPassword'), onPress: () => {void Linking.openURL(hostedUrl + '/account?tab=security');}}} secondaryAction={cancel}/>;
    const ready = !!password && (!account.twoStep || twoStep.trim().length === 6);
    return <StateLayout testID="accounts-pin-recovery-portico" title={title} body={t('accounts.profiles.forgotBody')}
      beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 12, gap: 12}}>{error}
        <TextField label={t('accounts.profiles.porticoPassword', {username: account.username})} value={password} onChangeText={setPassword} secureTextEntry autoComplete="current-password" editable={!state.busy}/>
        {account.twoStep && <TextField label={t('accounts.profiles.porticoTwoStep')} value={twoStep} onChangeText={setTwoStep} keyboardType="number-pad" autoComplete="one-time-code" maxLength={6} editable={!state.busy}/>}
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.profiles.porticoChecked')}</Text></View>}
      primaryAction={{id: 'prove-portico', label: t('accounts.signIn.continue'), disabled: state.busy || !ready, loading: state.busy,
        onPress: () => {const value = password, typed = twoStep.trim(); setPassword(''); setTwoStep(''); void controller.porticoPassword(value, typed, source);}}}
      secondaryAction={cancel}/>;
  }
  return <StateLayout testID="accounts-pin-recovery-password" title={t('accounts.profiles.forgotTitle', {profile: profileName})} body={t('accounts.profiles.forgotBody')}
    beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 12}}>{error}<TextField label={t('accounts.profiles.accountPassword', {username})} value={password} onChangeText={setPassword}
      secureTextEntry autoComplete="current-password" editable={!state.busy}/></View>}
    primaryAction={{id: 'prove-account', label: t('accounts.signIn.continue'), disabled: state.busy || !password, loading: state.busy,
      onPress: () => {const value = password; setPassword(''); void controller.password(value);}}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
}
