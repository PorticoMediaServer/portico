import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {StateLayout} from '../../kit/states';
import {Button, TextField} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import type {NativeProfileRecovery} from '../../platform/profile-recovery';
import {PhonePINEntry} from './PhonePINEntry';

const t = defaultI18n.t;

/** Auth18 on a phone. The controller owns every credential and the temporary proof
 * family; this presenter only shows the current phase and reports the chosen PIN. */
export function PhoneProfileRecovery({controller, profileName, username, onOpen, onClose}: {
  controller: NativeProfileRecovery; profileName: string; username: string; onOpen: (pin: string) => void; onClose: () => void;
}) {
  const theme = useTheme(), [state, setState] = useState(controller.getSnapshot());
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [pin, setPin] = useState('');
  useEffect(() => {
    const off = controller.subscribe(() => setState(controller.getSnapshot()));
    return () => {off(); controller.dispose();};
  }, [controller]);
  const error = state.error ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 12}]}>{presentError(state.error, 'profiles').body}</Text> : null;
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
  if (state.phase === 'portico') return <StateLayout testID="accounts-pin-recovery-portico" title={t('accounts.profiles.forgotTitle', {profile: profileName})} body={t('accounts.profiles.forgotBody')}
    beforeActions={error} primaryAction={{id: 'portico', label: t('accounts.signIn.handoff'), loading: state.busy, onPress: () => {void controller.portico();}}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
  return <StateLayout testID="accounts-pin-recovery-password" title={t('accounts.profiles.forgotTitle', {profile: profileName})} body={t('accounts.profiles.forgotBody')}
    beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 12}}>{error}<TextField label={t('accounts.profiles.accountPassword', {username})} value={password} onChangeText={setPassword}
      secureTextEntry autoComplete="current-password" editable={!state.busy}/></View>}
    primaryAction={{id: 'prove-account', label: t('accounts.signIn.continue'), disabled: state.busy || !password, loading: state.busy,
      onPress: () => {const value = password; setPassword(''); void controller.password(value);}}}
    secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: state.busy, onPress: onClose}}/>;
}
