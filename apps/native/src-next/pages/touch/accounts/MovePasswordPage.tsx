import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError, TextField} from '../../../kit/controls';
import {AccountFields, AccountHint} from '../../../kit/accounts/AccountParts';
import {StateActionButton} from '../../../kit/states/StateActionButton';
import {SignInFrame} from '../../../kit/frames';
import type {NativeMemberAccountMove} from '../../../platform/member-account-move';
import {useMovePassword} from '../../shared/accounts/useMovePassword';

/** Connect41b on a phone or tablet: the old server account's username and password, proved to the move client only.
 * The same sign-in frame and fields as a server's own sign-in (frame 6). */
export function MovePasswordPage<Result>({controller, serverName, onBack}: {
  controller: NativeMemberAccountMove<Result>; serverName: string; onBack: () => void;
}) {
  const t = defaultI18n.t, form = useMovePassword(controller, onBack);
  if (!form.visible) return null;
  return <SignInFrame testID="accounts-move-password" title={t('accounts.move.oldSignIn')} lede={t('accounts.move.oldSignInBody', {server: serverName})}
    back={{label: t('accounts.signIn.back'), onPress: form.back, disabled: form.busy, testID: 'accounts-move-password-back'}}>
    {form.error !== undefined && <FormError placement="block">{form.error}</FormError>}
    <AccountFields>
      <TextField appearance="auth" testID="accounts-move-password-username" label={t('accounts.signIn.username')} value={form.username} onChangeText={form.setUsername}
        editable={!form.busy} autoCapitalize="none" autoCorrect={false} autoComplete="username" textContentType="username" returnKeyType="next" onSubmitEditing={form.next}/>
      <TextField ref={form.passwordInput} appearance="auth" testID="accounts-move-password-password" label={t('accounts.signIn.password')} value={form.password}
        onChangeText={form.setPassword} editable={!form.busy} secureTextEntry={!form.shown} autoCapitalize="none" autoCorrect={false}
        autoComplete="current-password" textContentType="password" returnKeyType="go" onSubmitEditing={form.submit}
        trailing={<Button testID="accounts-move-password-visibility" variant="text" size="compact" iconOnly icon={form.shown ? 'i-eyeoff' : 'i-eye'}
          accessibilityLabel={t(form.shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')} accessibilityState={{selected: form.shown}}
          disabled={form.busy} onPress={form.toggleShown}/>}/>
    </AccountFields>
    <StateActionButton testID="accounts-move-password" variant="primary" align="stretch" action={{id: 'move-password-continue', label: t('accounts.signIn.continue'),
      disabled: !form.ready, loading: form.busy, onPress: form.submit}}/>
    <AccountHint>{t('accounts.move.oldPasswordHelp')}</AccountHint>
  </SignInFrame>;
}
