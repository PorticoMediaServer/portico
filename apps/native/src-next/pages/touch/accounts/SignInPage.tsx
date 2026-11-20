import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, TextField} from '../../../kit/controls';
import {SignInFrame} from '../../../kit/frames';
import {AccountDivider, AccountFields, AccountLinks, AccountTerms} from '../../../kit/accounts/AccountParts';
import {ProviderButton} from '../../../screens/accounts/ProviderButton';

/** auth.html frame 4, the first screen a signed-out phone or tablet shows. Apple and Google are always drawn; the
 * screen that owns it says plainly when one is not available yet. */
export function SignInPage({busy, error, passwordEnabled, registration, recovery, username, password,
  onUsernameChange, onPasswordChange, onContinue, onProvider, onForgot, onCreate, onLocal, onRetry, onOpenLink}: {
  busy: boolean; error?: React.ReactNode; passwordEnabled: boolean;
  registration: boolean; recovery: boolean; username: string; password: string;
  onUsernameChange: (value: string) => void; onPasswordChange: (value: string) => void;
  onContinue: () => void; onProvider: (id: 'apple' | 'google') => void; onForgot: () => void; onCreate: () => void; onLocal: () => void; onRetry?: () => void; onOpenLink: (url: string) => void;
}) {
  const t = defaultI18n.t, ready = !busy && !!username && !!password;
  return <SignInFrame testID="accounts-welcome" title={t('web.signin.welcomeHome')} lede={t('auth.accountLead')} terms={<AccountTerms open={onOpenLink}/>}>
    {error}
    {onRetry && <Button testID="accounts-auth-retry" variant="glass" align="stretch" disabled={busy} onPress={onRetry}>{t('action.tryAgain')}</Button>}
    <AccountFields>
      {(['apple', 'google'] as const).map(id => <ProviderButton key={id} provider={id} testID={`accounts-auth-${id}`} busy={busy}
        label={t(id === 'google' ? 'auth.signInWithGoogle' : 'auth.signInWithApple')} onPress={() => onProvider(id)}/>)}
    </AccountFields>
    {passwordEnabled && <>
      <AccountDivider>{t('auth.orWithEmail')}</AccountDivider>
      <AccountFields>
        <TextField appearance="auth" testID="accounts-auth-username" accessibilityLabel={t('auth.usernameOrEmail')} label={t('auth.usernameOrEmail')}
          value={username} placeholder="name@example.com" onChangeText={onUsernameChange} editable={!busy}
          textContentType="username" autoComplete="username" autoCapitalize="none" autoCorrect={false}/>
        <TextField appearance="auth" testID="accounts-auth-password" accessibilityLabel={t('auth.password')} label={t('auth.password')}
          value={password} onChangeText={onPasswordChange} editable={!busy} secureTextEntry textContentType="password"
          autoComplete="current-password" autoCapitalize="none" autoCorrect={false}/>
      </AccountFields>
      <Button testID="accounts-auth-continue" variant="primary" align="stretch" disabled={!ready}
        onPress={() => {if (ready) onContinue();}}>{t('action.continue')}</Button>
      <AccountLinks>
        {recovery && <Button testID="accounts-auth-forgot" variant="link" size="compact" disabled={busy} onPress={onForgot}>{t('auth.forgotPassword')}</Button>}
        {registration && <Button testID="accounts-auth-create" variant="link" size="compact" disabled={busy} onPress={onCreate}>{t('auth.createAccount')}</Button>}
      </AccountLinks>
    </>}
    <Button testID="accounts-auth-local" variant="link" size="compact" align="center" disabled={busy} onPress={onLocal}>{t('auth.signInDirectly')}</Button>
  </SignInFrame>;
}
