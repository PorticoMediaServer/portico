import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, type TextInput} from 'react-native';
import {FormScrollView} from '../../platform/keyboard';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {ColourBackdrop} from '../../kit/artwork';
import {Button, TextField} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeMemberAccountMove} from '../../platform/member-account-move';

/** Connect41b only: this command proves the Waiting account to the move client.
 * It never calls ordinary password sign-in on a Portico server. */
export function PhoneMovePassword<Result>({controller, serverName, onBack}: {
  controller: NativeMemberAccountMove<Result>; serverName: string; onBack: () => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const [username, setUsername] = useState(''), [password, setPassword] = useState(''), [showPassword, setShowPassword] = useState(false);
  const passwordInput = useRef<TextInput>(null);
  useEffect(() => {if (state.phase !== 'password') {setPassword(''); setShowPassword(false);}}, [state.phase]);
  if (state.phase !== 'password') return null;
  const submit = () => {
    if (state.busy || !username || !password) return;
    const supplied = password; setPassword(''); setShowPassword(false);
    void controller.proveOldPassword(username, supplied);
  };
  const back = () => {if (!state.busy) {setPassword(''); setShowPassword(false); controller.cancel(); onBack();}};
  return <View testID="accounts-move-password" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <FormScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{flexGrow: 1, paddingHorizontal: 22,
      paddingTop: Math.max(104, insets.top + 54), paddingBottom: Math.max(30, insets.bottom + 16)}}>
      <Text accessibilityRole="header" style={[nativeType({size: 34, lineHeight: 35.36, weight: 800, tracking: -.04}, theme.textScale),
        {color: theme.colors.text, marginBottom: 8}]}>{t('accounts.move.oldSignIn')}</Text>
      <Text style={[nativeType({size: 14.5, lineHeight: 21.75, weight: 400, tracking: 0}, theme.textScale),
        {color: theme.colors.textSecondary, marginBottom: 20}]}>{t('accounts.move.oldSignInBody', {server: serverName})}</Text>
      {state.error != null && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 12}]}>
        {presentError(state.error, 'sign-in').body}
      </Text>}
      <TextField label={t('accounts.signIn.username')} value={username} onChangeText={setUsername} editable={!state.busy}
        autoCapitalize="none" autoCorrect={false} autoComplete="username" textContentType="username" returnKeyType="next" onSubmitEditing={() => {if (!state.busy) passwordInput.current?.focus();}}
        fieldStyle={{height: 48, borderRadius: 11}} style={{fontSize: 16 * theme.textScale}} containerStyle={{marginBottom: 12}}/>
      <View style={{marginBottom: 12}}>
        <Text style={[nativeType({size: 13, lineHeight: 19.5, weight: 600, tracking: 0}, theme.textScale),
          {color: theme.colors.textSecondary, marginBottom: 6}]}>{t('accounts.signIn.password')}</Text>
        <View>
          <TextField ref={passwordInput} accessibilityLabel={t('accounts.signIn.password')} value={password} onChangeText={setPassword} editable={!state.busy}
            secureTextEntry={!showPassword} autoCapitalize="none" autoCorrect={false} autoComplete="current-password" textContentType="password"
            returnKeyType="go" onSubmitEditing={submit} fieldStyle={{height: 48, borderRadius: 11, paddingRight: 94}} style={{fontSize: 16 * theme.textScale}}/>
          <Button variant="text" icon={showPassword ? 'i-eyeoff' : 'i-eye'} iconSize={20} iconColor={theme.colors.textSecondary}
            disabled={state.busy} accessibilityState={{selected: showPassword}} onPress={() => setShowPassword(value => !value)}
            style={{position: 'absolute', right: 6, top: 4, height: 40, paddingHorizontal: 8}} textStyle={{fontSize: 13 * theme.textScale, color: theme.colors.textSecondary}}>
            {t(showPassword ? 'preferences.hide' : 'accounts.signIn.showPassword')}
          </Button>
        </View>
      </View>
      <Button disabled={state.busy || !username || !password} loading={state.busy || undefined} onPress={submit}
        style={{height: 48, borderRadius: 11, alignSelf: 'stretch', marginTop: 8}} textStyle={{fontSize: 16 * theme.textScale}}>{t('accounts.signIn.continue')}</Button>
      <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 14}]}>{t('accounts.move.oldPasswordHelp')}</Text>
    </FormScrollView>
    <View style={{position: 'absolute', top: Math.max(50, insets.top), left: 12, height: 48, justifyContent: 'center'}}>
      <Button variant="text" icon="i-left" iconSize={22} disabled={state.busy} onPress={back}>{t('accounts.signIn.back')}</Button>
    </View>
  </View>;
}
