import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, TextField} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {PhoneAccountForm} from './PhoneAccountForm';

/** auth.html frame 6: signing in to a server's own account on a phone or tablet. "Sign in to" over the server's name,
 * the error box above the fields, the address it signs in to (shown, not edited), username, password with show/hide,
 * Sign in, then Forgot password? and the server's other ways in (`links`). */
export function PhoneServerSignIn({busy, error, serverName, address, username, password, onUsernameChange, onPasswordChange, onSignIn, onBack, links}: {
  busy: boolean; error?: React.ReactNode; serverName: string; address: string; username: string; password: string;
  onUsernameChange: (value: string) => void; onPasswordChange: (value: string) => void; onSignIn: () => void; onBack: () => void; links?: React.ReactNode;
}) {
  const theme = useTheme(), t = defaultI18n.t, [shown, setShown] = useState(false);
  const ready = !busy && !!username && !!password;
  return <PhoneAccountForm testID="accounts-local" title={serverName} body={t('accounts.signIn.localBody')}
    kicker={<Text style={[theme.type.captionStrong, {fontSize: 13.5 * theme.textScale, color: theme.colors.accentStrong}]}>{t('accounts.signIn.to')}</Text>}
    detailsAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: onBack}}
    primaryAction={{id: 'local-sign-in', label: t('auth.signIn'), disabled: !ready, loading: busy, onPress: () => {if (ready) onSignIn();}}}
    beforeActions={<>
      {error}
      <View style={{alignSelf: 'stretch', gap: 12, marginBottom: 12}}>
        <TextField appearance="auth" testID="accounts-local-address" label={t('accounts.signIn.address')} value={address} readOnly/>
        <TextField appearance="auth" testID="accounts-local-username" label={t('auth.username')} value={username} onChangeText={onUsernameChange} editable={!busy}
          textContentType="username" autoComplete="username" autoCapitalize="none" autoCorrect={false}/>
        <TextField appearance="auth" testID="accounts-local-password" label={t('auth.password')} value={password} onChangeText={onPasswordChange} editable={!busy}
          secureTextEntry={!shown} textContentType="password" autoComplete="current-password" autoCapitalize="none" autoCorrect={false}
          returnKeyType="go" onSubmitEditing={() => {if (ready) onSignIn();}}
          trailing={<Button testID="accounts-local-password-visibility" variant="text" size="compact" iconOnly icon={shown ? 'i-eyeoff' : 'i-eye'} iconSize={18}
            accessibilityLabel={t(shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')} accessibilityState={{selected: shown}}
            disabled={busy} style={{height: 40, width: 40, paddingHorizontal: 0}} onPress={() => setShown(value => !value)}/>}/>
      </View>
    </>}>
    {links != null && <View style={{alignSelf: 'stretch', alignItems: 'flex-start', gap: 4, marginTop: 10}}>{links}</View>}
  </PhoneAccountForm>;
}
