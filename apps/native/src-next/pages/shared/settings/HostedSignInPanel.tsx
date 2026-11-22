import React from 'react';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {HostedMoreSignIn, HostedSignInFailure, HostedTwoStepControl} from '../../../kit/settings/HostedSignInParts';
import type {NativeAccountSecurityKind} from '../../../platform/account-security';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {AccountSecuritySheet, PasswordResetSheet} from '../../touch/accounts/AccountSecurityPage';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this panel.
import {useHostedSignIn} from '../../../pages/shared/settings/useHostedSignIn';

/** Settings › Password or Two-step for a Portico Account: the row and its action, recovery codes, the failure with Try again,
 * More sign-in options for a Google- or Apple-only account, and the security and reset sheets. Every action is the hook's. */
export function NativeHostedSignInPanel({kind, context, lifetime, t, runtime}: NativeSettingsPanelDeps & {kind: 'password' | 'twoStep'; runtime: NativeShellRuntime}) {
  const view = useHostedSignIn({kind, context, lifetime, runtime}), {ready, security} = view;
  if (!view.shown) return null;
  const failure = view.failure ? <HostedSignInFailure message={view.failure}/> : null;
  if (view.elsewhere) return kind !== 'password' ? null : <>
    <SettingsRow first testID="settings-panel-portico-account" label={t('settings.row.porticoAccount')} description={view.username}
      control={<Button testID="settings-manage-portico-account" variant="glass" size="compact" onPress={view.manage}>{t('web.signIn.manage')}</Button>}/>
    {failure}
  </>;
  // Without a password: Add a password opens the hosted reset page; Two-step says a password comes first and links to it.
  const action = (task: NativeAccountSecurityKind, label: string) => ready && (!security?.hasPassword
    ? task === 'password' ? <Button testID="account-security-password" variant="glass" size="compact" onPress={view.askReset}>{label}</Button>
      : <Button testID="account-security-two-step-password" variant="link" onPress={view.askReset}>{t('web.hostedSecurity.addPassword')}</Button>
    : <Button testID={`account-security-${task}`} variant="glass" size="compact" disabled={view.locked} onPress={() => view.open(task)}>{label}</Button>);
  const resetSheet = <PasswordResetSheet source={ready} open={view.resetAsk} onClose={view.closeReset} onFinished={view.refreshNow}/>;
  const securitySheet = security && <AccountSecuritySheet security={security} source={ready} onFinished={view.refreshNow}/>;
  if (view.providerOnly && !view.moreSignIn) return <>
    <HostedMoreSignIn label={t('web.hostedSecurity.moreSignInOptions')} onPress={view.showMoreSignIn}/>
    {failure}{securitySheet}{resetSheet}
  </>;
  return <>
    <SettingsRow first testID={`settings-panel-${kind}`} label={t(kind === 'password' ? 'settings.row.password' : 'settings.row.twoStep')}
      value={kind === 'twoStep' && security ? t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off') : view.providerOnly ? t('web.hostedSecurity.notSet') : undefined}
      description={kind === 'twoStep' && security && !security.hasPassword ? t('account.security.passwordNeeded') : undefined}
      control={security ? kind === 'password' ? action('password', t(view.providerOnly ? 'web.hostedSecurity.addPassword' : security.hasPassword ? 'web.hostedSecurity.change' : 'account.setPassword'))
        : <HostedTwoStepControl status={t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off')}>
          {action(security.mfaEnabled ? 'mfa-off' : 'mfa-on', t(security.mfaEnabled ? 'web.hostedSecurity.turnOff' : 'web.twoStep.turnOn'))}</HostedTwoStepControl> : undefined}/>
    {kind === 'twoStep' && security?.mfaEnabled && <SettingsRow testID="settings-panel-recovery-codes" label={t('web.hostedSecurity.recoveryCodes')}
      control={action('codes', t('web.hostedSecurity.makeCodes'))}/>}
    {view.failure && <HostedSignInFailure message={view.failure} retry={view.canRetry
      ? <Button testID={`account-security-retry-${kind}`} variant="glass" size="compact" onPress={view.retry}>{t('action.tryAgain')}</Button> : undefined}/>}
    {securitySheet}{resetSheet}
  </>;
}
