import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError, TextField} from '../../../kit/controls';
import {AccountFields, AccountLinks} from '../../../kit/accounts/AccountParts';
import {StateActionButton} from '../../../kit/states/StateActionButton';
import {SignInFrame} from '../../../kit/frames';
import {PasswordStrengthMeter} from '../../../screens/accounts/PasswordStrengthMeter';
import type {NativeMemberAccountMove} from '../../../platform/member-account-move';
import {useMoveCode} from '../../shared/accounts/useMoveCode';

/** Connect44b/c on a phone or tablet: the owner's move code (typed, or scanned where the device can), then the new
 * local sign-in with the shared strength meter. The same sign-in frame and fields as a server's own sign-in (frame 6). */
export function MoveCodePage<Result>({controller, onBack, isCurrent}: {
  controller: NativeMemberAccountMove<Result>; onBack: () => void;
  /** Captured member controller and its original verified operation, never a successor. */
  isCurrent: () => boolean;
}) {
  const t = defaultI18n.t, form = useMoveCode(controller, onBack, isCurrent);
  if (!form.visible) return null;
  const eye = <Button testID="accounts-move-password-visibility" variant="text" size="compact" iconOnly icon={form.shown ? 'i-eyeoff' : 'i-eye'}
    accessibilityLabel={t(form.shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')} accessibilityState={{selected: form.shown}}
    disabled={form.busy} onPress={form.toggleShown}/>;
  return <SignInFrame testID={form.local ? 'accounts-move-local-sign-in' : 'accounts-move-code'}
    title={t(form.local ? 'accounts.move.chooseSignIn' : 'accounts.move.enterCode')} lede={form.local ? t('accounts.move.profilesKept') : undefined}
    back={{label: t('accounts.signIn.back'), onPress: form.back, disabled: form.busy, testID: 'accounts-move-code-back'}}>
    {form.error !== undefined && <FormError placement="block">{form.error}</FormError>}
    {form.local ? <AccountFields>
      <TextField appearance="auth" testID="accounts-move-username" label={t('accounts.signIn.username')} value={form.username} onChangeText={form.setUsername}
        editable={!form.busy} autoCapitalize="none" autoCorrect={false} autoComplete="username" textContentType="username"/>
      <TextField appearance="auth" testID="accounts-move-new-password" label={t('accounts.signIn.password')} value={form.password} onChangeText={form.setPassword}
        editable={!form.busy} secureTextEntry={!form.shown} autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword"
        returnKeyType="next" trailing={eye}/>
      <PasswordStrengthMeter password={form.password}/>
      <TextField appearance="auth" testID="accounts-move-confirm-password" label={t('accounts.signIn.confirmPassword')} value={form.confirmation}
        onChangeText={form.setConfirmation} editable={!form.busy} secureTextEntry={!form.shown} autoCapitalize="none" autoCorrect={false}
        autoComplete="new-password" textContentType="newPassword" returnKeyType="go" onSubmitEditing={form.submit}
        error={form.differ ? t('accounts.signIn.passwordsDiffer') : undefined}/>
    </AccountFields> : <AccountFields>
      <TextField appearance="auth" testID="accounts-move-code-field" label={t('accounts.move.code')} value={form.code} onChangeText={form.setCode}
        editable={!form.busy} autoCapitalize="none" autoCorrect={false} returnKeyType="go" onSubmitEditing={form.submit}
        error={form.invalidCode ? t('accounts.move.codeInvalid') : undefined}/>
    </AccountFields>}
    <StateActionButton testID="accounts-move-code" variant="primary" align="stretch" action={{id: 'move-code-continue', label: t('accounts.signIn.continue'),
      disabled: !form.ready, loading: form.busy, onPress: form.submit}}/>
    {form.canScan && <AccountLinks><Button testID="accounts-move-scan" variant="link" disabled={form.busy || form.scanning} loading={form.scanning || undefined}
      onPress={form.scan}>{t('accounts.move.scanQR')}</Button></AccountLinks>}
    {form.scanError !== undefined && <FormError>{form.scanError}</FormError>}
  </SignInFrame>;
}
