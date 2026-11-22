import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {SecurityState} from '../../../../../../packages/client-core/src/hosted-security';
import {Button, TextField, FormError} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {PasswordStrengthMeter} from '../../../kit/accounts/PasswordStrengthMeter';
import {SecurityDangerButton, SecurityFooter, SecurityGroup, SecurityHeading, SecurityMoreButton, SecurityProvider, SecurityRow, SecuritySecret, SecurityStack, SecurityText} from '../../../kit/accounts/AccountSecurityParts';
import type {NativeAccountWorkspace} from '../../../platform/account-workspace';
import type {NativeAccountSecurity, NativeAccountSecurityKind} from '../../../platform/account-security';
import {useAccountSecurity, useAccountSecuritySheet, usePasswordReset} from '../../shared/accounts/useAccountSecurity';

/** Account security in the account workspace: Signing in, Two-step, Your data, then deleting the account. */
export function AccountSecurityPage({workspace, security}: {workspace: NativeAccountWorkspace; security: SecurityState}) {
  const t = defaultI18n.t, page = useAccountSecurity({workspace, security}), {source} = page;
  // Without a password only Add a password works here; Email, Two-step and deletion need one. Export waits for its file saver.
  const action = (kind: NativeAccountSecurityKind, label: string, disabled = false) => kind === 'password' && !security.hasPassword
    ? <Button testID="account-security-password" variant="glass" size="compact" disabled={!source} onPress={page.askReset}>{label}</Button>
    : !security.hasPassword || kind === 'export' ? <SecurityText secondary>{t('account.security.appUnavailable')}</SecurityText>
      : <Button testID={`account-security-${kind}`} variant="glass" size="compact" disabled={!source || disabled} onPress={() => page.open(kind)}>{label}</Button>;
  return <>
    {page.unavailable && <FormError placement="inline">{page.unavailable}</FormError>}
    <SecurityGroup title={t('web.hostedSecurity.signingIn')}>
      {/* A Google- or Apple-only account has no Password row until More sign-in options shows it. */}
      {page.providerOnly ? page.moreSignIn && <SecurityRow name={t('web.password')} detail={t('web.hostedSecurity.notSet')}>{action('password', t('web.hostedSecurity.addPassword'), page.credentialLocked)}</SecurityRow>
        : <SecurityRow name={t('web.password')} detail={t(security.hasPassword ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off')}>
          {action('password', security.hasPassword ? t('web.hostedSecurity.change') : t('account.setPassword'), security.hasPassword && page.credentialLocked)}</SecurityRow>}
      <SecurityRow name={t('web.hostedSecurity.email')} detail={security.contactAddress || t('web.hostedSecurity.notSet')}>{action('email', t('web.hostedSecurity.change'), page.credentialLocked)}</SecurityRow>
      {security.providers.map(provider => <SecurityProvider key={provider.id} name={t(provider.provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')} detail={t('web.hostedSecurity.connected')}/>)}
    </SecurityGroup>
    {page.providerOnly && !page.moreSignIn && <SecurityMoreButton testID="account-security-more-sign-in" onPress={page.showMoreSignIn}>{t('web.hostedSecurity.moreSignInOptions')}</SecurityMoreButton>}
    <SecurityGroup title={t('web.twoStep.title')}>
      {!security.hasPassword
        ? <SecurityRow name={t('web.twoStep.title')} detail={t('account.security.passwordNeeded')}><Button testID="account-security-two-step-password" variant="link" disabled={!source} onPress={page.askReset}>{t('web.hostedSecurity.addPassword')}</Button></SecurityRow>
        : <>
          <SecurityRow name={t('web.twoStep.title')} detail={t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off')}>
            {action(security.mfaEnabled ? 'mfa-off' : 'mfa-on', security.mfaEnabled ? t('web.hostedSecurity.turnOff') : t('web.twoStep.turnOn'), page.credentialLocked)}</SecurityRow>
          {security.mfaEnabled && <SecurityRow name={t('web.hostedSecurity.recoveryCodes')} detail={t('web.hostedSecurity.codesLeft', {count: security.recoveryCodesRemaining})}>{action('codes', t('web.hostedSecurity.makeCodes'), page.credentialLocked)}</SecurityRow>}
        </>}
    </SecurityGroup>
    <SecurityGroup title={t('services.security.yourData')}>
      <SecurityRow name={t('web.account.export.button')} detail={t('web.account.export.help')}>{action('export', t('web.account.export.download'))}</SecurityRow>
    </SecurityGroup>
    <SecurityStack after>
      <SecurityText>{t('web.deleteAccount.title')}</SecurityText>
      {/* Deletion needs the password typed for it (#3392): an account without one sets it first. */}
      {!security.hasPassword && <SecurityText secondary>{t('account.security.appUnavailable')}</SecurityText>}
      <SecurityDangerButton testID="account-security-delete" variant="glass" disabled={!source || !security.hasPassword} onPress={() => page.open('delete')}>{t('web.deleteAccount.action')}</SecurityDangerButton>
    </SecurityStack>
    <AccountSecuritySheet security={security} source={source} onFinished={page.finished}/>
    <PasswordResetSheet source={source} open={page.resetAsk} onClose={page.closeReset} onFinished={page.finished}/>
  </>;
}

/** Add a password, wherever it is offered: one plain line that the person signs in again afterwards, then the hosted reset
 * page, as the web's Set a password does. */
export function PasswordResetSheet({source, open, onClose, onFinished}: {source?: NativeAccountSecurity; open: boolean; onClose: () => void; onFinished: () => void}) {
  const t = defaultI18n.t, reset = usePasswordReset({source, open, onClose, onFinished});
  return <BottomSheet testID="account-password-reset-sheet" open={open} onOpenChange={value => {if (!value && !reset.busy) onClose();}} title={t('web.hostedSecurity.addPassword')}
    footer={<SecurityFooter>
      <Button testID="account-password-reset-continue" loading={reset.busy} disabled={!source} onPress={reset.go}>{t('action.continue')}</Button>
      <Button variant="text" disabled={reset.busy} onPress={onClose}>{t('action.cancel')}</Button>
    </SecurityFooter>}>
    <SecurityStack>
      {reset.failure && <FormError placement="inline">{reset.failure}</FormError>}
      <SecurityText secondary>{t('account.security.passwordResetHelp')}</SecurityText>
    </SecurityStack>
  </BottomSheet>;
}

/** The canonical account-owned task, including provider confirmation and its factor step. */
export function AccountSecuritySheet({security, source, onFinished}: {security: SecurityState; source?: NativeAccountSecurity; onFinished: () => void}) {
  const t = defaultI18n.t, sheet = useAccountSecuritySheet({security, source, onFinished}), {state} = sheet;
  return (
    <BottomSheet testID="account-security-sheet" open={!!state.kind} onOpenChange={value => {if (!value) sheet.close();}} title={sheet.title ? t(sheet.title) : ''}
      footer={<SecurityFooter>
        {sheet.complete ? <Button onPress={sheet.close}>{t('action.done')}</Button> : sheet.providerForm ? null
          // Deleting can't be undone: the destructive button, never the default, and only it sends the command.
          : sheet.deleting && sheet.finalDelete ? <SecurityDangerButton testID="account-security-delete-confirm" loading={state.busy} variant="glass"
            disabled={sheet.submitDisabled} onPress={sheet.deleteAccount}>{t('web.deleteAccount.confirmAction')}</SecurityDangerButton>
          : sheet.deleting ? <Button testID="account-security-delete-next" disabled={sheet.submitDisabled} onPress={sheet.toFinalDelete}>{t('action.continue')}</Button>
          : <Button loading={state.busy} disabled={sheet.submitDisabled} onPress={sheet.submit}>
          {state.kind === 'export' ? t('web.account.export.button') : state.step === 'email' || state.step === 'mfa' || state.step === 'factor' ? t('security.twoStep.verify') : t('action.continue')}
        </Button>}
        {!sheet.complete && <Button variant="text" disabled={state.busy} onPress={sheet.close}>{t('action.cancel')}</Button>}
      </SecurityFooter>}>
      <SecurityStack>
        {state.failure && <FormError placement="inline">{state.failure}</FormError>}
        {sheet.providerForm && <>
          {state.kind === 'password' && <SecurityText secondary>{t('web.hostedSecurity.othersSignedOut')}</SecurityText>}
          {state.kind === 'email' && <SecurityText secondary>{t('services.security.emailPending')}</SecurityText>}
          {state.kind === 'mfa-off' && <SecurityText secondary>{t('services.security.disableBody')}</SecurityText>}
          {state.kind === 'codes' && <SecurityText secondary>{t('web.hostedSecurity.recoveryCodesHelp')}</SecurityText>}
          {state.kind === 'password' && <TextField label={t('accounts.recovery.newPassword')} value={sheet.newPassword} onChangeText={sheet.setNewPassword} secureTextEntry editable={sheet.editable} autoComplete="new-password" textContentType="newPassword"/>}
          {state.kind === 'password' && <PasswordStrengthMeter password={sheet.newPassword}/>}
          {state.kind === 'email' && <TextField label={t('web.hostedSecurity.newEmail')} value={sheet.email} onChangeText={sheet.setEmail} editable={sheet.editable} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="emailAddress"/>}
          {security.providers.map(link => <Button key={link.id} testID={`account-security-provider-${link.provider}`} variant="glass" disabled={state.busy || !sheet.providerInput}
            onPress={() => sheet.confirmWithProvider(link.id)}>
            {t(link.provider === 'apple' ? 'services.provider.apple' : 'services.provider.google')}</Button>)}
        </>}
        {state.step === 'factor' && <TextField label={t('web.reset.factor')} value={sheet.code} onChangeText={sheet.setCode} editable={!state.busy} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {sheet.credentials && security.hasPassword && <>
          {state.kind === 'password' && <SecurityText secondary>{t('web.hostedSecurity.othersSignedOut')}</SecurityText>}
          {state.kind === 'mfa-off' && <SecurityText secondary>{t('web.hostedSecurity.passwordAlone')}</SecurityText>}
          {state.kind === 'codes' && <SecurityText secondary>{t('web.hostedSecurity.recoveryCodesHelp')}</SecurityText>}
          <TextField label={t('account.currentPassword')} value={sheet.password} onChangeText={sheet.setPassword} secureTextEntry editable={sheet.editable} autoComplete="current-password" textContentType="password"/>
          {state.kind === 'password' && <TextField label={t('accounts.recovery.newPassword')} value={sheet.newPassword} onChangeText={sheet.setNewPassword} secureTextEntry editable={sheet.editable} autoComplete="new-password" textContentType="newPassword"/>}
          {state.kind === 'password' && <PasswordStrengthMeter password={sheet.newPassword}/>}
          {state.kind === 'email' && <TextField label={t('web.hostedSecurity.newEmail')} value={sheet.email} onChangeText={sheet.setEmail} editable={sheet.editable} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="emailAddress"/>}
          {security.mfaEnabled && <TextField label={t('web.reset.factor')} value={sheet.factor} onChangeText={sheet.setFactor} editable={sheet.editable} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        </>}
        {state.step === 'email' && <SecurityText secondary>{`${t('web.hostedSecurity.sentCode', {address: state.result?.maskedAddress ?? ''})} ${t('services.security.emailPending')}`}</SecurityText>}
        {state.step === 'mfa' && <><SecurityText secondary>{t('web.hostedSecurity.enterKey')}</SecurityText><SecuritySecret>{state.result?.secret}</SecuritySecret></>}
        {/* The emailed code is eight letters and digits (#3392); the authenticator's code stays numeric. */}
        {state.step === 'email' && <TextField testID="account-security-email-code" label={t('auth.verificationCode')} value={sheet.code} onChangeText={sheet.setCode} editable={!state.busy} autoCapitalize="characters" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {state.step === 'mfa' && <TextField label={t('auth.verificationCode')} value={sheet.code} onChangeText={sheet.setCode} editable={!state.busy} keyboardType="number-pad" autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {state.step === 'codes' && <>
          <SecurityText secondary>{t('services.security.codesBody')}</SecurityText>
          <SecuritySecret>{state.result?.recoveryCodes?.join('\n')}</SecuritySecret>
          <Button variant="glass" onPress={sheet.copyCodes}>{t('web.twoStep.copy')}</Button>
          {sheet.copyFailure && <FormError placement="inline">{t('error.unavailable')}</FormError>}
        </>}
        {sheet.deleting && !sheet.finalDelete && <>
          <SecurityText secondary>{t('account.delete.consequence')}</SecurityText>
          <TextField testID="account-security-delete-password" label={t('account.currentPassword')} value={sheet.password} onChangeText={sheet.setPassword} secureTextEntry editable={!state.busy} autoComplete="current-password" textContentType="password"/>
          {security.mfaEnabled && <TextField testID="account-security-delete-code" label={t('web.reset.factor')} value={sheet.factor} onChangeText={sheet.setFactor} editable={!state.busy} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        </>}
        {sheet.deleting && sheet.finalDelete && <>
          <SecurityHeading>{t('account.delete.title')}</SecurityHeading>
          <SecurityText secondary>{t('account.delete.consequence')}</SecurityText>
        </>}
        {state.kind === 'export' && state.step === 'form' && <SecurityText secondary>{t('web.account.export.help')}</SecurityText>}
        {state.step === 'export' && <SecurityText secondary>{state.exportJob?.state === 'ready' ? t('web.account.notif.exportReady') : t('web.account.export.preparing')}</SecurityText>}
        {state.step === 'done' && <SecurityText>{t('settings.save.saved')}</SecurityText>}
      </SecurityStack>
    </BottomSheet>
  );
}
