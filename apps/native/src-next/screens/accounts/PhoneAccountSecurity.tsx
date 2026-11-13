import React, {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {NativeModules, Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Button, TextField, FormError} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import {copyText} from '../../platform/clipboard';
import type {NativeAccountWorkspace} from '../../platform/account-workspace';
import type {NativeAccountSecurity, NativeAccountSecurityKind, NativeAccountSecuritySnapshot} from '../../platform/account-security';
import {providerOnlySignIn, type SecurityState} from '../../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {PasswordStrengthMeter} from './PasswordStrengthMeter';

const idle: NativeAccountSecuritySnapshot = {step: 'form', busy: false};
const noSubscribe = () => () => {};
const idleSnapshot = () => idle;
const titles: Record<NativeAccountSecurityKind, MessageId> = {
  password: 'account.changePasswordTitle', email: 'web.hostedSecurity.changeEmailTitle', 'mfa-on': 'web.twoStep.turnOnTitle',
  'mfa-off': 'web.twoStep.turnOffTitle', codes: 'web.hostedSecurity.newCodesTitle', export: 'web.account.export.button', delete: 'web.deleteAccount.row',
};

/** Account-only security uses the same scoped service with or without a reachable server. */
export function PhoneAccountSecurity({workspace, security}: {workspace: NativeAccountWorkspace; security: SecurityState}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [source, setSource] = useState<NativeAccountSecurity>();
  const [unavailable, setUnavailable] = useState<string>();
  // A Google- or Apple-only account (Justin, 1 Nov): Add a password waits behind More sign-in options.
  const [moreSignIn, setMoreSignIn] = useState(false);
  // Add a password and Two-step's "Set a password" open the hosted reset page after one plain line (#3392).
  const [resetAsk, setResetAsk] = useState(false);
  useLayoutEffect(() => {
    if (!workspace.current()) return;
    let controller: NativeAccountSecurity;
    try {controller = workspace.securityActions();} catch (error) {setUnavailable(presentError(error, 'portico-account').body); return;}
    setUnavailable(undefined); setSource(controller);
    return () => {controller.dispose(); setSource(value => value === controller ? undefined : value);};
  }, [workspace, security]);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? idleSnapshot);
  const current = () => !!source?.current() && source.getSnapshot() === state;
  const open = (kind: NativeAccountSecurityKind) => {if (current()) source!.open(kind);};
  const text = (value: string, secondary = false, key?: string) => <Text key={key} style={[theme.type.body, {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{value}</Text>;
  const group = (name: string, children: React.ReactNode) => <View style={{gap: 10}}><Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{name}</Text>
    <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 14, gap: 14}}>{children}</View></View>;
  // Without a password only Add a password works here; Email, Two-step and deletion need one. Export waits for its file saver.
  const action = (kind: NativeAccountSecurityKind, label: string, disabled = false) => kind === 'password' && !security.hasPassword
    ? <Button testID="account-security-password" variant="glass" size="compact" disabled={!source} onPress={() => setResetAsk(true)}>{label}</Button>
    : !security.hasPassword || kind === 'export' ? text(t('account.security.appUnavailable'), true)
      : <Button testID={`account-security-${kind}`} variant="glass" size="compact" disabled={!source || disabled} onPress={() => open(kind)}>{label}</Button>;
  const row = (name: string, detail: string, control?: React.ReactNode) => <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 12}}>
    <View style={{flex: 1, minWidth: 150, gap: 4}}>{text(name)}{text(detail, true)}</View>{control}
  </View>;
  const {credentialLocked} = accountSecurityAvailability(security, NativeModules.PorticoAccountAuth);
  const providerOnly = providerOnlySignIn(security);
  return <>
    {unavailable && <FormError placement="inline">{unavailable}</FormError>}
    {group(t('web.hostedSecurity.signingIn'), <>
      {/* A Google- or Apple-only account has no Password row until More sign-in options shows it. */}
      {providerOnly ? moreSignIn && row(t('web.password'), t('web.hostedSecurity.notSet'), action('password', t('web.hostedSecurity.addPassword'), credentialLocked))
        : row(t('web.password'), t(security.hasPassword ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off'), action('password', security.hasPassword ? t('web.hostedSecurity.change') : t('account.setPassword'), security.hasPassword && credentialLocked))}
      {row(t('web.hostedSecurity.email'), security.contactAddress || t('web.hostedSecurity.notSet'), action('email', t('web.hostedSecurity.change'), credentialLocked))}
      {security.providers.map(provider => <View key={provider.id}>{text(t(provider.provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google'))}{text(t('web.hostedSecurity.connected'), true)}</View>)}
    </>)}
    {providerOnly && !moreSignIn && <Button testID="account-security-more-sign-in" variant="text" size="compact" style={{alignSelf: 'flex-start', marginTop: -4}}
      onPress={() => setMoreSignIn(true)}>{t('web.hostedSecurity.moreSignInOptions')}</Button>}
    {group(t('web.twoStep.title'), !security.hasPassword
      ? row(t('web.twoStep.title'), t('account.security.passwordNeeded'), <Button testID="account-security-two-step-password" variant="link" disabled={!source} onPress={() => setResetAsk(true)}>{t('web.hostedSecurity.addPassword')}</Button>)
      : <>
      {row(t('web.twoStep.title'), t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off'), action(security.mfaEnabled ? 'mfa-off' : 'mfa-on', security.mfaEnabled ? t('web.hostedSecurity.turnOff') : t('web.twoStep.turnOn'), credentialLocked))}
      {security.mfaEnabled && row(t('web.hostedSecurity.recoveryCodes'), t('web.hostedSecurity.codesLeft', {count: security.recoveryCodesRemaining}), action('codes', t('web.hostedSecurity.makeCodes'), credentialLocked))}
    </>)}
    {group(t('services.security.yourData'), row(t('web.account.export.button'), t('web.account.export.help'), action('export', t('web.account.export.download'))))}
    <View style={{gap: 12, marginTop: 8}}>{text(t('web.deleteAccount.title'))}
      {/* Deletion needs the password typed for it (#3392): an account without one sets it first. */}
      {!security.hasPassword && text(t('account.security.appUnavailable'), true)}
      <Button testID="account-security-delete" variant="glass" style={{backgroundColor: `${theme.colors.danger}24`}} textStyle={{color: theme.colors.danger}} disabled={!source || !security.hasPassword} onPress={() => open('delete')}>{t('web.deleteAccount.action')}</Button>
    </View>
    <AccountSecuritySheet security={security} source={source} onFinished={() => {void workspace.open('security');}}/>
    <PasswordResetSheet source={source} open={resetAsk} onClose={() => setResetAsk(false)} onFinished={() => {void workspace.open('security');}}/>
  </>;
}

/** Add a password, wherever it is offered: one plain line that the person signs in again afterwards, then the hosted reset
 * page, as the web's Set a password does. It stays busy while the person is there; on return the adapter checks the sign-in. */
export function PasswordResetSheet({source, open, onClose, onFinished}: {source?: NativeAccountSecurity; open: boolean; onClose: () => void; onFinished: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [busy, setBusy] = useState(false), [failure, setFailure] = useState<string>();
  useLayoutEffect(() => {if (!open) {setBusy(false); setFailure(undefined);}}, [open]);
  const go = async () => {
    if (busy || !source?.current()) return;
    setBusy(true); setFailure(undefined);
    try {await source.openPasswordReset(); onClose(); onFinished();}
    catch (error) {setFailure(presentError(error, 'portico-account').body);}
    finally {setBusy(false);}
  };
  return <BottomSheet testID="account-password-reset-sheet" open={open} onOpenChange={value => {if (!value && !busy) onClose();}} title={t('web.hostedSecurity.addPassword')}
    footer={<View style={{gap: 10}}>
      <Button testID="account-password-reset-continue" loading={busy} disabled={!source} onPress={() => {void go();}}>{t('action.continue')}</Button>
      <Button variant="text" disabled={busy} onPress={onClose}>{t('action.cancel')}</Button>
    </View>}>
    <View style={{gap: 12}}>
      {failure && <FormError placement="inline">{failure}</FormError>}
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('account.security.passwordResetHelp')}</Text>
    </View>
  </BottomSheet>;
}

/** Pure availability shared by the account workspace and server Settings. */
export function accountSecurityAvailability(security: Pick<SecurityState, 'hasPassword' | 'providers'>,
  bridge: Readonly<{canOpenBrowser?: boolean; canSignInApple?: boolean}> | undefined) {
  // Root §70: a Google- or Apple-only account confirms credential changes with that provider.
  const providerProof = !security.hasPassword && security.providers.length > 0 && (bridge?.canOpenBrowser === true || bridge?.canSignInApple === true);
  return {providerProof, credentialLocked: !security.hasPassword && !providerProof};
}

/** The canonical account-owned task, including provider confirmation and its factor step. */
export function AccountSecuritySheet({security, source, onFinished}: {
  security: SecurityState; source?: NativeAccountSecurity; onFinished: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [password, setPassword] = useState(''), [factor, setFactor] = useState(''), [newPassword, setNewPassword] = useState('');
  const [email, setEmail] = useState(''), [code, setCode] = useState(''), [copyFailure, setCopyFailure] = useState(false);
  // Deletion: the password (and code) typed for it, then one final irreversible confirmation (#3392).
  const [finalDelete, setFinalDelete] = useState(false);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? idleSnapshot);
  const current = () => !!source?.current() && source.getSnapshot() === state;
  const clear = () => {setPassword(''); setFactor(''); setNewPassword(''); setEmail(''); setCode(''); setCopyFailure(false); setFinalDelete(false);};
  useLayoutEffect(() => {
    setPassword(''); setFactor(''); setNewPassword(''); setEmail(''); setCode(''); setCopyFailure(false); setFinalDelete(false);
  }, [source, state.kind]);
  // A refused deletion goes back to the form so the password or code can be typed again.
  useLayoutEffect(() => {if (state.failure) setFinalDelete(false);}, [state.failure]);
  const close = () => {if (!current() || state.busy) return; const refresh = state.step !== 'form'; clear(); source!.close(); if (refresh) onFinished();};
  const text = (value: string, secondary = false, key?: string) => <Text key={key} style={[theme.type.body, {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{value}</Text>;
  const credentials = state.kind && !['export', 'delete'].includes(state.kind) && state.step === 'form';
  const deleting = state.kind === 'delete' && state.step === 'form';
  const providerForm = credentials && !security.hasPassword;
  const providerInput = state.kind === 'password' ? !!newPassword : state.kind === 'email' ? !!email.trim() : true;
  const editable = !state.busy && !state.locked;
  const complete = state.step === 'codes' || state.step === 'done' || state.step === 'export';
  const submitDisabled = deleting ? !password || security.mfaEnabled && !factor.trim()
    : state.step === 'email' || state.step === 'mfa' || state.step === 'factor' ? !code.trim()
      : credentials ? !password || security.mfaEnabled && !factor.trim()
        || state.kind === 'password' && !newPassword || state.kind === 'email' && !email.trim() : false;
  return (
    <BottomSheet testID="account-security-sheet" open={!!state.kind} onOpenChange={value => {if (!value) close();}} title={state.kind ? t(state.kind === 'password' && !security.hasPassword ? 'account.setPassword' : titles[state.kind]) : ''}
      footer={<View style={{gap: 10}}>
        {complete ? <Button onPress={close}>{t('action.done')}</Button> : providerForm ? null
          // Deleting can't be undone: the destructive button, never the default, and only it sends the command.
          : deleting && finalDelete ? <Button testID="account-security-delete-confirm" loading={state.busy} variant="glass" style={{backgroundColor: `${theme.colors.danger}24`}}
            textStyle={{color: theme.colors.danger}} disabled={submitDisabled} onPress={() => {if (!current()) return; void source!.deleteAccount(password, factor.trim());}}>{t('web.deleteAccount.confirmAction')}</Button>
          : deleting ? <Button testID="account-security-delete-next" disabled={submitDisabled} onPress={() => {if (current()) setFinalDelete(true);}}>{t('action.continue')}</Button>
          : <Button loading={state.busy} disabled={submitDisabled} onPress={() => {
          if (!current()) return;
          void source!.submit({password, factor, newPassword, email, code});
        }}>
          {state.kind === 'export' ? t('web.account.export.button') : state.step === 'email' || state.step === 'mfa' || state.step === 'factor' ? t('security.twoStep.verify') : t('action.continue')}
        </Button>}
        {!complete && <Button variant="text" disabled={state.busy} onPress={close}>{t('action.cancel')}</Button>}
      </View>}>
      <View style={{gap: 12}}>
        {state.failure && <FormError placement="inline">{state.failure}</FormError>}
        {providerForm && <>
          {state.kind === 'password' && text(t('web.hostedSecurity.othersSignedOut'), true)}
          {state.kind === 'email' && text(t('services.security.emailPending'), true)}
          {state.kind === 'mfa-off' && text(t('services.security.disableBody'), true)}
          {state.kind === 'codes' && text(t('web.hostedSecurity.recoveryCodesHelp'), true)}
          {state.kind === 'password' && <TextField label={t('accounts.recovery.newPassword')} value={newPassword} onChangeText={setNewPassword} secureTextEntry editable={editable} autoComplete="new-password" textContentType="newPassword"/>}
          {state.kind === 'password' && <PasswordStrengthMeter password={newPassword}/>}
          {state.kind === 'email' && <TextField label={t('web.hostedSecurity.newEmail')} value={email} onChangeText={setEmail} editable={editable} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="emailAddress"/>}
          {security.providers.map(link => <Button key={link.id} testID={`account-security-provider-${link.provider}`} variant="glass" disabled={state.busy || !providerInput}
            onPress={() => {if (!current()) return; void source!.confirmWithProvider(link.id, {newPassword, email});}}>
            {t(link.provider === 'apple' ? 'services.provider.apple' : 'services.provider.google')}</Button>)}
        </>}
        {state.step === 'factor' && <TextField label={t('web.reset.factor')} value={code} onChangeText={setCode} editable={!state.busy} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {credentials && security.hasPassword && <>
          {state.kind === 'password' && text(t('web.hostedSecurity.othersSignedOut'), true)}
          {state.kind === 'mfa-off' && text(t('web.hostedSecurity.passwordAlone'), true)}
          {state.kind === 'codes' && text(t('web.hostedSecurity.recoveryCodesHelp'), true)}
          <TextField label={t('account.currentPassword')} value={password} onChangeText={setPassword} secureTextEntry editable={editable} autoComplete="current-password" textContentType="password"/>
          {state.kind === 'password' && <TextField label={t('accounts.recovery.newPassword')} value={newPassword} onChangeText={setNewPassword} secureTextEntry editable={editable} autoComplete="new-password" textContentType="newPassword"/>}
          {state.kind === 'password' && <PasswordStrengthMeter password={newPassword}/>}
          {state.kind === 'email' && <TextField label={t('web.hostedSecurity.newEmail')} value={email} onChangeText={setEmail} editable={editable} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="emailAddress"/>}
          {security.mfaEnabled && <TextField label={t('web.reset.factor')} value={factor} onChangeText={setFactor} editable={editable} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        </>}
        {state.step === 'email' && text(`${t('web.hostedSecurity.sentCode', {address: state.result?.maskedAddress ?? ''})} ${t('services.security.emailPending')}`, true)}
        {state.step === 'mfa' && <>{text(t('web.hostedSecurity.enterKey'), true)}<Text selectable style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{state.result?.secret}</Text></>}
        {/* The emailed code is eight letters and digits (#3392); the authenticator's code stays numeric. */}
        {state.step === 'email' && <TextField testID="account-security-email-code" label={t('auth.verificationCode')} value={code} onChangeText={setCode} editable={!state.busy} autoCapitalize="characters" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {state.step === 'mfa' && <TextField label={t('auth.verificationCode')} value={code} onChangeText={setCode} editable={!state.busy} keyboardType="number-pad" autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        {state.step === 'codes' && <>
          {text(t('services.security.codesBody'), true)}
          <Text selectable style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{state.result?.recoveryCodes?.join('\n')}</Text>
          <Button variant="glass" onPress={() => {if (!current()) return; const captured = state.result; void copyText(captured?.recoveryCodes?.join('\n') ?? '').catch(() => {if (source?.current() && source.getSnapshot().result === captured) setCopyFailure(true);});}}>{t('web.twoStep.copy')}</Button>
          {copyFailure && <FormError placement="inline">{t('error.unavailable')}</FormError>}
        </>}
        {deleting && !finalDelete && <>
          {text(t('account.delete.consequence'), true)}
          <TextField testID="account-security-delete-password" label={t('account.currentPassword')} value={password} onChangeText={setPassword} secureTextEntry editable={!state.busy} autoComplete="current-password" textContentType="password"/>
          {security.mfaEnabled && <TextField testID="account-security-delete-code" label={t('web.reset.factor')} value={factor} onChangeText={setFactor} editable={!state.busy} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode"/>}
        </>}
        {deleting && finalDelete && <>
          <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('account.delete.title')}</Text>
          {text(t('account.delete.consequence'), true)}
        </>}
        {state.kind === 'export' && state.step === 'form' && text(t('web.account.export.help'), true)}
        {state.step === 'export' && text(state.exportJob?.state === 'ready' ? t('web.account.notif.exportReady') : t('web.account.export.preparing'), true)}
        {state.step === 'done' && text(t('settings.save.saved'))}
      </View>
    </BottomSheet>
  );
}
