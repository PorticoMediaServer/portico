import React, {useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, TextField} from '../../../kit/controls';
import {AccountFields, AccountLinks} from '../../../kit/accounts/AccountParts';
import {StateActionButton} from '../../../kit/states/StateActionButton';
import {SignInFrame} from '../../../kit/frames';

/** auth.html frame 6: signing in to a server's own account on a phone or tablet. "Sign in to" over the server's name,
 * the error box above the fields, the address it signs in to (shown, not edited), username, password with show/hide,
 * Sign in, then Forgot password? and the server's other ways in (`links`). */
export function ServerSignInPage({busy, error, passwordInvalid = false, serverName, address, username, password, onUsernameChange, onPasswordChange, onSignIn, onBack, links}: {
  busy: boolean; error?: React.ReactNode; /** The server refused these credentials: the password field is drawn in red. */ passwordInvalid?: boolean; serverName: string; address: string; username: string; password: string;
  onUsernameChange: (value: string) => void; onPasswordChange: (value: string) => void; onSignIn: () => void; onBack: () => void; links?: React.ReactNode;
}) {
  const t = defaultI18n.t, [shown, setShown] = useState(false);
  const ready = !busy && !!username && !!password;
  return <SignInFrame testID="accounts-local" title={serverName} lede={t('accounts.signIn.localBody')}
    kicker={t('accounts.signIn.to')}
    back={{label: t('action.back'), onPress: onBack, disabled: busy, testID: 'accounts-local-back'}}>
      {error}
      <AccountFields>
        <TextField appearance="auth" testID="accounts-local-address" label={t('accounts.signIn.address')} value={address} readOnly/>
        <TextField appearance="auth" testID="accounts-local-username" label={t('auth.username')} value={username} onChangeText={onUsernameChange} editable={!busy}
          textContentType="username" autoComplete="username" autoCapitalize="none" autoCorrect={false}/>
        <TextField appearance="auth" testID="accounts-local-password" label={t('auth.password')} value={password} onChangeText={onPasswordChange} editable={!busy} invalid={passwordInvalid}
          secureTextEntry={!shown} textContentType="password" autoComplete="current-password" autoCapitalize="none" autoCorrect={false}
          returnKeyType="go" onSubmitEditing={() => {if (ready) onSignIn();}}
          trailing={<Button testID="accounts-local-password-visibility" variant="text" size="compact" iconOnly icon={shown ? 'i-eyeoff' : 'i-eye'}
            accessibilityLabel={t(shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')} accessibilityState={{selected: shown}}
            disabled={busy} onPress={() => setShown(value => !value)}/>}/>
      </AccountFields>
      <StateActionButton testID="accounts-local" variant="primary" align="stretch" action={{id: 'local-sign-in', label: t('auth.signIn'),
        disabled: !ready, loading: busy, onPress: () => {if (ready) onSignIn();}}}/>
      {links != null && <AccountLinks>{links}</AccountLinks>}
  </SignInFrame>;
}
