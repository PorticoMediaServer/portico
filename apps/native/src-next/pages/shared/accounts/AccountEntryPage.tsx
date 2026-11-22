import React from 'react';
import {moveOfferDays} from '../../../../../../packages/client-core/src/sign-in';
import {TVAuthLayout} from '../../../kit/frames/TVAuthLayout';
import {StateLayout} from '../../../kit/states';
import {Button, TextField, FormError, Switch} from '../../../kit/controls';
import {ProfilePicker} from '../../../kit/artwork';
import {BootWaitLine} from '../../../shell/StartupBoot';
import {ServerNeedsUpdate} from '../../../kit/accounts/ServerNeedsUpdate';
import {nativeAccounts} from '../../../platform/accounts';
import {NativeServerSignIn} from '../../../platform/server-sign-in';
import {NativePorticoSignIn, porticoUsernameUnavailable} from '../../../platform/portico-sign-in';
import {TVCredentialForm, TVCodePresenter, TVVerificationForm, TVRequiredSetupHandoff, TVFrontDoor, TVProfilePINForm, TVForgotPINHandoff, TVMoveHandoff, TVWhosWatching, TVPickerRememberRow} from '../../../tv/auth';
import {TVIdentityCodeForm} from '../../../tv/auth/TVIdentityCodeForm';
import {passwordStrength} from '../../../../../../packages/client-core/src/password-strength';
import {parseJoinLink} from '../../../../../../packages/client-core/src/join-link';
import {formatShortInvitationCode, parseShortInvitationCode} from '../../../../../../packages/client-core/src/short-invitation-code';
import {PhoneOwnerReset} from '../../../kit/accounts/PhoneOwnerReset';
import {SetupPage} from '../../touch/accounts/SetupPage';
import {PhoneFindServer} from '../../../kit/accounts/PhoneFindServer';
import {ServerSignInPage} from '../../touch/accounts/ServerSignInPage';
import {PhonePINEntry} from '../../../kit/accounts/PhonePINEntry';
import {NoServersPage as PhoneNoServersPage} from '../../touch/accounts/NoServersPage';
import {qrScannerAvailable, scanQRCode} from '../../../platform/qr-scanner';
import {AccountWorkspacePage} from '../../touch/accounts/AccountWorkspacePage';
import {OfflineServersPage} from '../../touch/accounts/OfflineServersPage';
import {NoServersPage as TVNoServersPage} from '../../tv/accounts/NoServersPage';
import {OfflineServersPage as TVOfflineServersPage} from '../../tv/accounts/OfflineServersPage';
import {PhoneAccountMoveNotice} from '../../../kit/accounts/PhoneAccountMoveNotice';
import {MovePasswordPage} from '../../touch/accounts/MovePasswordPage';
import {MoveCodePage} from '../../touch/accounts/MoveCodePage';
import {EndedMoveCodeAction} from '../../../kit/accounts/EndedMoveCodeAction';
import {DirectoryAccessRefused} from '../../../kit/accounts/DirectoryAccessRefused';
import {directoryAccessRefused, directoryClockSkew, directoryIdentityUnconfirmed} from '../../../screens/accounts/directory-refusal';
import {ProfilePINPage} from '../../touch/accounts/ProfilePINPage';
import {ProfilesPage} from '../../touch/accounts/ProfilesPage';
import {afterEntry} from '../../../shell/after-entry';
import {RememberProfileRow} from '../../../kit/accounts/RememberProfileRow';
import {SignInPage} from '../../touch/accounts/SignInPage';
import {AccountEntryActions, AccountEntryAddress, AccountEntryChoices, AccountEntryCodeNote, AccountEntryNearby, AccountEntryNearbyLine, AccountEntryNearbyServer, AccountEntryServerList, AccountEntryServerRow, AccountEntryChoicesLegal, AccountEntryCode, AccountEntryDetails, AccountEntryLine, AccountEntryScan, AccountEntryWordmark, AccountEntryEndedNotice, AccountEntryFields, AccountEntryFootNote, AccountEntryLegal, AccountEntryNote, AccountEntryNotes, AccountEntryPasswordToggle, AccountEntryResend, AccountEntrySentNotes, AccountEntryStep} from '../../../kit/accounts/AccountEntryParts';
import {AccountForm} from '../../../kit/accounts/AccountForm';
import {PasswordStrengthMeter} from '../../../kit/accounts/PasswordStrengthMeter';
import {ProfileRecoveryPage} from '../../touch/accounts/ProfileRecoveryPage';
import {directoryOfflineBody, directoryRows} from '../../../screens/accounts/directory-chooser';
// Named through pages/shared (not `./`) so the screen harness loads the hook for real when a test loads this page.
import {profileFace, setupElsewhereRefusal, useAccountEntry, type AccountEntryProps, type PINRecovery} from '../../../pages/shared/accounts/useAccountEntry';
import {defaultI18n} from '../../../../../../packages/i18n/src';

export type {AccountEntryProps, NativeAccountEntryStage} from '../../../pages/shared/accounts/useAccountEntry';
const t = defaultI18n.t;

/** Current normal entry, drawn in connect39 onward. The separately labelled historical
 * setup entry is not imported. Every button below dispatches an actual native/core command. */
export function AccountEntryPage(props: AccountEntryProps) {
  const {downloads, onBack, onComplete, onSetupComplete, endedSignIn, resumeAccount} = props;
  const {latestEndedHint, preparedAuthSource, providerLoading, setProviderLoading, theme, stage, setStage, busy, error, setError, accountEnded, address, setAddress, username, setUsername, password, setPassword, code, setCode, wrongCodeTimer, usernameUnavailable, setUsernameUnavailable, passwordShown, setPasswordShown, registerOpening, verifyOpening, setVerifyOpening, verifyOpeningRef, providerOpening, providerUsernameUnavailable, setProviderUsernameUnavailable, factorOpening, resetLocation, setResetLocation, ownerResetOpening, setupAddress, acceptTerms, setAcceptTerms, setupPasswordConfirm, setSetupPasswordConfirm, name, setName, email, setEmail, masked, setMasked, server, profiles, profile, canRemember, remember, setRemember, profileFocus, identityCode, setIdentityCode, identityWrong, setIdentityWrong, identityAddress, invitationIntent, endedTarget, servers, target, retryIn, options, transaction, setTransaction, tvState, tvKind, recovery, setRecovery, recoveryCode, setRecoveryCode, requiredSetup, setRequiredSetup, directoryList, memberMove, moveSnapshot, memberMoveRef, memberOperation, memberFactorOperation, stopMemberMove, pinRecovery, setPinRecovery, invitationPreview, setInvitationPreview, invitationOpening, invitationEntryOpening, invitationScan, invitationScanning, setInvitationScanning, scannedInvitationLink, setScannedInvitationLink, invitationSwitch, retireInvitationSwitch, retireInvitation, nearby, nearbyStatus, setNearbyRefresh, nearbySource, lifetime, directoryAttempt, retryDelay, operation, account, pinOpening, pinRecoveryRef, profileRef, retireRecovery, targetRef, busyRef, stageRef, backRef, registerOpeningRef, providerOpeningRef, registerDraftRev, stageError, contact, factorKind, accountWorkspace, retireAccountWorkspace, downloadsOwner, downloadValue, accountScope, acting, stillPIN, openPIN, run, reset, cancelDirectory, openAccountWorkspace, openInvitationEntry, back, tvDoor, consume, directory, connectDirectory, chooseAnother, startTV, publishPreparedAccount, portico, intentRunner, providerNotice, setProviderNotice, startAccount, tvDoorTried, noCodeNow, beginMemberMove, beginMemberCode, beginEndedMemberCode, createMoveAccount, differentMoveAccount, requestReset, inspect, confirmCode, choose, submitTVFactor, action, renderedTV, codeBack, backAction, failure, passwordInvalid, directoryView, hostedWaiting, offlineRows, allDirectoryOffline, setupTarget, refusal, tvDevice, openLegalLink, openEndedSignInPage, openLocalSignInPage, providerOffered, localNetworkNote, shareDownloadPage} = useAccountEntry(props);
  // Every password field on a touch screen can be shown (auth.html frame 6), the new one and the one you sign in with.
  const revealable = (credential: string | undefined, label: string) => !theme.isTV && label === t('auth.password') && (credential === 'new-password' || credential === 'password');
  const field = (label: string, value: string, change: (next: string) => void, secure = false, credential?: 'username' | 'password' | 'new-password' | 'email', invalid?: string, help?: string, placeholder?: string, oneTime = false) => <TextField key={label} label={label} value={value} onChangeText={change}
    appearance={theme.platform === 'phone' ? 'auth' : undefined} secureTextEntry={secure && !(passwordShown && revealable(credential, label))}
    keyboardType={oneTime && !theme.isTV ? 'number-pad' : credential === 'email' ? 'email-address' : undefined} inputMode={oneTime && !theme.isTV ? 'numeric' : undefined}
    textContentType={oneTime && !theme.isTV ? 'oneTimeCode' : credential === 'new-password' ? 'newPassword' : credential === 'email' ? 'emailAddress' : credential}
    autoComplete={oneTime && !theme.isTV ? 'one-time-code' : credential === 'password' ? 'current-password' : credential}
    autoCapitalize="none" autoCorrect={false} editable={!busy} error={invalid} description={help} placeholder={placeholder}
    trailing={revealable(credential, label) ? <AccountEntryPasswordToggle shown={passwordShown} label={t(passwordShown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')}
      disabled={busy} onPress={() => {if (lifetime.current && stageRef.current === stage && !busyRef.current && !acting.current) setPasswordShown(value => !value);}}/> : undefined}/>;
  const codeField = (label: string, value: string, change: (next: string) => void) => field(label, value, change, false, undefined, undefined, undefined, undefined, true);
  // A two-step code on a phone or tablet is six boxes in two threes (auth.html 6, account-10 frame 3, account-31 frame 3);
  // TV keeps its own code field.
  const twoStepField = (value: string, change: (next: string) => void) => theme.isTV ? codeField(t('auth.verificationCode'), value, change)
    : <PhonePINEntry length={6} label={t('auth.verificationCode')} value={value} onChange={change} busy={busy} error={Boolean(stageError)}/>;
  const fields = (...children: React.ReactNode[]) => <AccountEntryFields>{children}</AccountEntryFields>;
  // The kit's labelled switch is the box; the legal line below the form links the Terms of Use.
  const ageTerms = <Switch key="age-terms" testID="accounts-age-terms" checked={acceptTerms} disabled={busy} onCheckedChange={setAcceptTerms}>
    {t('services.signup.ageTerms', {terms: t('services.signup.termsOfUse')})}</Switch>;
  // A TV that reaches a server nobody has set up says where setup happens: this device cannot be
  // the owner, and the generic refusal tells nobody what to do. On the address step the notice is
  // drawn under the field it belongs to, so the field keeps the focus it already holds; a server
  // picked from a list says the same.
  const status = tvDevice && setupElsewhereRefusal(stageError)
    ? <FormError placement="block">{t('accounts.setup.elsewhere')}</FormError>
    : failure && !failure.silent ? <FormError placement="block">{passwordInvalid ? t('web.signIn.error.credentials', {server: server!.name}) : failure.body}</FormError>
    : accountEnded && stage === 'welcome' ? <AccountEntryEndedNotice>{t('web.signIn.error.accountEnded')}</AccountEntryEndedNotice> : null;
  // Phone and tablet share the compact form column (auth.html frames 4 to 6).
  const FormLayout = theme.isTV ? TVAuthLayout : AccountForm;
  // Pickers and directory/recovery states keep their authored centred composition.
  const EntryLayout = theme.isTV ? TVAuthLayout : ['profiles', 'profile-pin', 'servers', 'account-sign-out', 'tv'].includes(stage) ? StateLayout : FormLayout;
  const formStep = theme.platform === 'phone' && stage.startsWith('setup-')
    ? <AccountEntryStep>{t('accounts.setup.title')}</AccountEntryStep> : undefined;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<typeof action>, more?: React.ReactNode) => <EntryLayout testID="accounts-entry" title={title} body={body} kicker={formStep} beforeActions={theme.isTV ? <>{before}{status}</> : <>{status}{before}</>} primaryAction={primary} secondaryAction={backAction} avoidKeyboard={stage === 'profile-pin'}>{more}</EntryLayout>;
  if (refusal) return <ServerNeedsUpdate refusal={refusal} onBack={() => {setError(undefined); reset();}}/>;
  const legalFooter = <AccountEntryLegal onOpenLink={openLegalLink}/>;
  if (tvDevice && stage === 'tv-required-setup' && requiredSetup) return <TVRequiredSetupHandoff serverName={requiredSetup.serverName}
    requirement={requiredSetup.requirement} url={new URL('/settings/profile', requiredSetup.serverOrigin).href} busy={busy}
    copy={{body: requiredSetup.requirement === 'password' ? 'next.auth.tv.requiredPasswordBody' : 'next.auth.tv.requiredTwoStepBody', reference: 'next.auth.tv.handoffHint'}}
    onSignInAgain={() => {if (!busy) {setRequiredSetup(undefined); setError(undefined); setStage('tv-credentials');}}} onBack={back}/>;
  if (tvDevice && stage === 'tv-credentials') return <TVCredentialForm {...(tvKind === 'local'
    ? {kind: 'local' as const, serverName: server!.name}
    : {kind: 'portico' as const, usernamePlaceholder: 'auth.usernameOrEmailPlaceholder' as const})}
    username={username} password={password} onUsernameChange={setUsername} onPasswordChange={setPassword}
    busy={busy} error={status} onBack={back} onSubmit={() => {void run(async () => {
      if (tvKind === 'portico') {await account.current!.password(username, password); setPassword(''); await directory();}
      else {const result = await operation.current!.signInLocal(username, password); setPassword(''); consume(result);}
    });}}/>;
  if (tvDevice && stage === 'factor') return <TVVerificationForm kind={factorKind.current} code={code} onCodeChange={setCode}
    recovery={recovery} recoveryCode={recoveryCode} onRecoveryCodeChange={setRecoveryCode} onRecoveryChange={value => {setRecovery(value); setCode(''); setRecoveryCode(''); setError(undefined);}}
    busy={busy} error={status} onSubmit={value => {void run(() => submitTVFactor(value));}} onBack={back}/>;
  if (stage === 'ended-sign-in' && endedSignIn) return layout(endedSignIn.serverName, t('web.signIn.ended', {server: endedSignIn.serverName}),
    endedSignIn.signIn === 'local' ? fields(field(t('accounts.signIn.address'), address, setAddress), field(t('auth.username'), username, setUsername, false, 'username'),
      field(t('auth.password'), password, setPassword, true, 'password')) : undefined,
    endedSignIn.signIn === 'local' ? action('sign-in-again', t('auth.signIn'), async () => {
      if (!lifetime.current || stageRef.current !== 'ended-sign-in') return;
      endedTarget.current = Object.freeze({serverId: endedSignIn.serverId, fingerprint: endedSignIn.fingerprint});
      const source = await inspect(address);
      if (!source || !lifetime.current || operation.current !== source) return;
      source.assertSignInTarget(endedSignIn.serverId, endedSignIn.fingerprint);
      const result = await source.signInLocal(username, password);
      if (lifetime.current && operation.current === source) {setPassword(''); consume(result);}
    }, !address.trim() || !username.trim() || !password) : action('portico-again', t('accounts.signIn.continuePortico'), portico),
    <AccountEntryActions>{!tvDevice && endedSignIn.signIn === 'portico' && <EndedMoveCodeAction scope={endedSignIn} busy={busy}
      isCurrent={() => lifetime.current && stageRef.current === 'ended-sign-in' && latestEndedHint.current === endedSignIn}
      onStart={(guard, signal) => run(() => beginEndedMemberCode(guard, signal))}/>}
      {endedSignIn.signIn === 'local' && <Button variant="link" disabled={busy} onPress={() => {
      if (lifetime.current && stageRef.current === 'ended-sign-in') openEndedSignInPage(endedSignIn);
    }}>{t('auth.forgotPassword')}</Button>}
      <Button variant="link" disabled={busy} onPress={() => {if (lifetime.current && stageRef.current === 'ended-sign-in') {setUsername(''); setPassword(''); reset(); onBack?.();}}}>{t('auth.useDifferentAccount')}</Button>
    </AccountEntryActions>);
  if (stage === 'account-workspace' && accountWorkspace.current?.createSource) {
    const ticket = accountWorkspace.current;
    const current = () => ticket.active && accountWorkspace.current === ticket && lifetime.current && stageRef.current === 'account-workspace';
    const leave = (destination: 'servers' | 'address' | 'account-sign-out' | 'invitation-entry') => {
      if (!current()) return; const live = nativeAccounts.accountCredentials().getSnapshot().session;
      const valid = live?.account.id === ticket.scope.accountId && live.familyId === ticket.scope.familyId; retireAccountWorkspace();
      if (!valid) {reset(); return;}
      if (destination === 'invitation-entry') {openInvitationEntry(); return;}
      setPassword(''); setCode(''); setError(undefined);
      stageRef.current = destination; setStage(destination);
      if (destination === 'servers') void run(directory);
      if (destination === 'address') setAddress('');
    };
    return <AccountWorkspacePage createSource={ticket.createSource!} username={ticket.username}
      onDone={() => leave('servers')} onJoin={() => leave('invitation-entry')} onFind={() => leave('address')} onSignOut={() => leave('account-sign-out')}/>;
  }
  // Why a TV has no code, in plain words, for the code card (the door and the code screen say the same).
  const tvReason = () => tvDevice && setupElsewhereRefusal(stageError) ? t('accounts.setup.elsewhere')
    : failure && !failure.silent ? failure.category === 'unreachable' || failure.category === 'timeout' ? t('activation.unreachable') : failure.body
    : accountEnded ? t('web.signIn.error.accountEnded') : undefined;
  if (stage === 'welcome' && theme.isTV) return <TVFrontDoor busy={busy} reason={tvReason()} autoStart={tvDoor && !tvDoorTried.current && !resumeAccount && !stageError}
    onPortico={() => {tvDoorTried.current = true; void run(portico);}} onLocal={() => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) {
      setAddress(''); setUsername(''); setPassword(''); setError(undefined); stageRef.current = 'address'; setStage('address');
    }}}/>;
  const authOptions = account.current && preparedAuthSource.current === account.current ? options : undefined;
  const availableProviders = authOptions?.providers.filter(providerOffered) ?? [];
  // Keep the same native ancestry and loading title when callback settlement
  // hands off to directory loading. No signed-out form or second loading mount
  // belongs between them. Decision forms (username/contact/MFA) remain distinct.
  if ((stage === 'portico' && providerLoading && providerLoading === account.current)
    || (stage === 'servers' && directoryList === 'loading'))
    return <StateLayout testID="accounts-server-list-loading" title={t('web.chooser.loading')}/>;
  const providerNoticeView = providerNotice ? <FormError placement="block">{t('accounts.provider.unavailable',
    {provider: t(providerNotice === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}</FormError> : null;
  if (stage === 'welcome') return <SignInPage busy={busy} error={status ?? providerNoticeView} onOpenLink={openLegalLink}
    passwordEnabled registration recovery username={username} password={password}
    onUsernameChange={value => {if (lifetime.current && stageRef.current === 'welcome') setUsername(value);}}
    onPasswordChange={value => {if (lifetime.current && stageRef.current === 'welcome') setPassword(value);}}
    onProvider={id => startAccount({kind: 'provider', id})} onContinue={() => {if (username && password) startAccount({kind: 'continue'});}}
    onForgot={() => startAccount({kind: 'forgot'})} onCreate={() => startAccount({kind: 'create'})}
    onLocal={() => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) {setError(undefined); setProviderNotice(undefined); stageRef.current = 'address'; setStage('address');}}}/>;
  if (stage === 'setup-confirm') return layout(t('accounts.setup.confirmTitle'), t('accounts.setup.confirmBody'),
    <AccountEntryDetails>
      <AccountEntryLine tone="strong">{server!.name}</AccountEntryLine>
      <AccountEntryLine tone="caption">{new URL(setupAddress).host}</AccountEntryLine>
      <AccountEntryLine>{t('accounts.setup.confirmWarning')}</AccountEntryLine>
    </AccountEntryDetails>, action('confirm-setup', t('accounts.setup.confirmAction'), async () => {if (lifetime.current && stageRef.current === 'setup-confirm') setStage('setup');}));
  // First-run setup on the shared controller; it pairs with the same first-contact proof as sign-in.
  if (stage === 'setup' && setupTarget) return <SetupPage target={setupTarget}
    onAdmitted={id => {if (!lifetime.current || stageRef.current !== 'setup') return; onSetupComplete?.(id); onComplete();}}
    onLeave={() => {if (lifetime.current && stageRef.current === 'setup') setStage('setup-confirm');}}/>;
  if (stage === 'address') {
    const ticket = nearbySource.current;
    const current = () => !!ticket?.active && nearbySource.current === ticket && lifetime.current && stageRef.current === 'address';
    if (!theme.isTV) return <PhoneFindServer busy={busy} error={status} address={address} onAddressChange={setAddress}
      found={nearby.map(value => ({origin: value.origin, name: value.server.name, host: new URL(value.origin).host, setupRequired: value.server.setupRequired === true}))}
      searching={nearbyStatus === 'loading'} failed={nearbyStatus === 'failed'} localNetworkNote={localNetworkNote}
      onContinue={() => {if (address.trim()) void run(async () => {await inspect();});}}
      onChoose={origin => {if (!current() || busyRef.current) return; setAddress(origin); void run(async () => {await inspect(origin);});}}
      onSearchAgain={() => {if (!current() || busyRef.current) return; ticket!.active = false; setNearbyRefresh(value => value + 1);}}
      onPortico={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) back();}}
      onInvitation={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) openInvitationEntry();}}
      onBack={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) back();}}/>;
    return layout(t('accounts.signIn.local'), undefined, fields(field(t('accounts.signIn.address'), address, setAddress)), action('inspect', t('action.continue'), async () => {await inspect();}, !address.trim()),
      <AccountEntryNearby>
        <AccountEntryNearbyLine>{t('playOn.onThisNetwork')}</AccountEntryNearbyLine>
        {nearbyStatus === 'loading' && <AccountEntryNearbyLine>{t('status.loading')}</AccountEntryNearbyLine>}
        {nearbyStatus === 'failed' && <AccountEntryNearbyLine kind="notice" alert>{t('connection.discovery.failed')}</AccountEntryNearbyLine>}
        {nearbyStatus === 'ready' && nearby.length === 0 && <AccountEntryNearbyLine kind="notice">{t('connection.discovery.empty')}</AccountEntryNearbyLine>}
        {nearby.map(value => <AccountEntryNearbyServer key={value.origin} host={new URL(value.origin).host}><Button variant="glass" disabled={busy} onPress={() => {
          if (!current() || busyRef.current) return;
          setAddress(value.origin); void run(async () => {await inspect(value.origin);});
        }}>{value.server.name}{value.server.setupRequired ? ' · ' + t('accounts.setup.confirmAction') : ''}</Button></AccountEntryNearbyServer>)}
        {nearbyStatus !== 'loading' && <Button variant="link" disabled={busy} onPress={() => {
          if (!current() || busyRef.current) return; ticket!.active = false; setNearbyRefresh(value => value + 1);
        }}>{t('web.chooser.tryAgain')}</Button>}
      </AccountEntryNearby>);
  }
  if (stage === 'owner-reset' || stage === 'owner-reset-code' || stage === 'owner-reset-done') {
    const ticket = ownerResetOpening.current, source = ticket?.source, renderedStage = stage;
    const current = () => !!ticket && ownerResetOpening.current === ticket && operation.current === source
      && !ticket.controller.signal.aborted && lifetime.current && stageRef.current === renderedStage;
    const actionRun = (work: () => Promise<void>) => {if (!current()) return; void run(async () => {
      if (!current()) return;
      try {await work();} catch (error) {if (current()) throw error;}
    });};
    return <PhoneOwnerReset phase={stage === 'owner-reset' ? 'intro' : stage === 'owner-reset-code' ? 'code' : 'done'} serverName={server!.name}
      location={resetLocation} code={code} onCodeChange={value => {if (current()) setCode(value);}} busy={busy} error={status}
      onBack={() => {if (current()) back();}}
      onWrite={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset') return;
        const location = await source.requestOwnerReset();
        if (current()) {setResetLocation(location); setCode(''); setStage('owner-reset-code');}
      })}
      onVerify={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset-code') return;
        await source.verifyOwnerReset(code);
        if (current()) {setCode(''); setStage('owner-reset-done');}
      })}
      onContinue={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset-done') return;
        const accepted = await source.acceptOwnerReset();
        if (current()) consume(accepted);
      })}/>;
  }
  if (stage === 'uses-portico') {
    const source = operation.current;
    const current = () => !!source && operation.current === source && lifetime.current && stageRef.current === 'uses-portico';
    // During a move to Portico Accounts the notice says so before the old sign-in is offered (P2-66).
    // The days left come from the server's own move window (P2-66, Justin's copy).
    const moveDays = server?.move === 'to_portico' ? moveOfferDays(server.moveEndsAt) : undefined;
    const notice = server?.move === 'to_portico' ? t('accounts.move.toPortico', {server: server.name}) + ' ' + t('accounts.signIn.porticoNotice') : t('accounts.signIn.porticoNotice');
    // A TV never takes an old password with the remote: during a move it hands the move to a phone
    // (the server's own sign-in page, P2-66), and the member then signs in here the Portico way.
    if (tvDevice && server?.move === 'to_portico') return <EntryLayout testID="accounts-entry" title={t('accounts.signIn.usesPortico', {server: server.name})} body={notice}
      kicker={formStep} beforeActions={<>{status}</>} primaryAction={action('portico', t('accounts.signIn.continuePortico'), portico)}
      additionalActions={[{id: 'move-on-phone', label: t('next.auth.tv.moveOnPhone'), disabled: busy, onPress: () => {if (!busy) setStage('tv-move-handoff');}}]}
      secondaryAction={backAction}/>;
    return layout(t('accounts.signIn.usesPortico', {server: server!.name}), notice, undefined, action('portico', t('accounts.signIn.continuePortico'), portico),
      !tvDevice && <AccountEntryActions>
        {moveDays !== undefined && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberMove);}}>{t('accounts.move.oldPasswordAction', {days: moveDays})}</Button>}
        {server?.move === 'to_portico' && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberCode);}}>{t('accounts.move.haveCode')}</Button>}
        {/* No "Can't use your Portico Account?" until the rebuilt server lets the reset file sign the owner in (asked for). */}
      </AccountEntryActions>);
  }
  if (stage === 'member-move' && memberMove && moveSnapshot && memberOperation.current === operation.current) {
    if (moveSnapshot.phase === 'code' || moveSnapshot.phase === 'local') {
      const captured = memberMove, source = memberOperation.current;
      const current = () => !!source && lifetime.current && stageRef.current === 'member-move' && memberMoveRef.current === captured
        && memberOperation.current === source && operation.current === source;
      return <MoveCodePage controller={captured} isCurrent={current} onBack={back}/>;
    }
    if (moveSnapshot.phase === 'password') return <MovePasswordPage controller={memberMove} serverName={server!.name} onBack={back}/>;
    if (moveSnapshot.phase === 'portico') return <PhoneAccountMoveNotice busy={busy || moveSnapshot.busy} state={{kind: 'to-portico', serverName: server!.name,
      onContinue: () => {void run(portico);}, onCreateAccount: () => {void run(createMoveAccount);}}}/>;
    if (moveSnapshot.phase === 'confirm-portico' && moveSnapshot.username && moveSnapshot.porticoAccount) {
      const captured = memberMove, source = memberOperation.current;
      // The server's name from its verified system answer, the server account from its own answer,
      // the Portico Account from the one held here; never from a link.
      return <PhoneAccountMoveNotice busy={busy || moveSnapshot.busy} state={{kind: 'confirm', serverName: source!.serverDescriptor().name,
        username: moveSnapshot.username, account: moveSnapshot.porticoAccount.username,
        onMove: () => {void run(() => captured.redeemPortico());},
        onNotYou: () => {captured.changePorticoAccount(); void run(differentMoveAccount);},
        onCancel: back}}/>;
    }
    if (moveSnapshot.phase === 'collision' && moveSnapshot.collision) return <PhoneAccountMoveNotice busy={busy || moveSnapshot.busy}
      state={{kind: 'collision', details: moveSnapshot.collision, onDifferentAccount: () => {void run(differentMoveAccount);}, onBack: back}}/>;
    if (moveSnapshot.phase === 'adopted' && moveSnapshot.result && moveSnapshot.result.kind !== 'challenge') return <PhoneAccountMoveNotice state={{kind: 'linked', onChooseProfile: () => {
      const result = memberMove.takeAcceptedResult(); if (!result || memberOperation.current !== operation.current) return; stopMemberMove(); consume(result);
    }}}/>;
  }
  if (stage === 'identity-code' && tvDevice) return <TVIdentityCodeForm server={identityAddress} code={identityCode} wrong={identityWrong} busy={busy}
    onChange={value => {setIdentityCode(value); setIdentityWrong(false);}} onSubmit={value => {void run(() => confirmCode(value));}}/>;
  if (stage === 'identity-code') return <FormLayout testID="accounts-identity-code" title={t('connection.identity.checkTitle', {server: identityAddress})} body={t('connection.identity.checkBody')}
    kicker={tvDevice ? undefined : <AccountEntryAddress>{identityAddress}</AccountEntryAddress>}
    beforeActions={<>{fields(<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('connection.identity.codeLabel')} value={identityCode} autoCapitalize="characters" autoCorrect={false} editable={!busy}
      error={identityWrong ? t('connection.identity.codeWrong') : undefined} onChangeText={value => {
        const raw = value.toUpperCase().replace(/[\s-]/g, '').slice(0, 12), shown = raw.match(/.{1,4}/g)?.join('-') ?? '';
        setIdentityCode(shown); setIdentityWrong(false); if (tvDevice && raw.length === 12) void run(() => confirmCode(shown));
      }}/>) }{status}</>}
    primaryAction={tvDevice ? undefined : action('check-code', t('action.continue'), () => confirmCode(), identityCode.replace(/-/g, '').length !== 12)}
    secondaryAction={backAction} help={t('connection.identity.checkHome')}/>;
  if (stage === 'invitation-entry') {
    const ticket = invitationEntryOpening.current, external = ticket?.external;
    const parsed = external ? external.current() ? NativeServerSignIn.parseInvitationEntry(external.raw, address) : undefined
      : scannedInvitationLink ? NativeServerSignIn.parseInvitationEntry(scannedInvitationLink, address)
        : NativeServerSignIn.parseShortInvitationEntry(code, address);
    const current = () => !!ticket?.active && invitationEntryOpening.current === ticket && lifetime.current && stageRef.current === 'invitation-entry' && (!external || external.current());
    const change = (set: (value: string) => void, format = false) => (value: string) => {
      if (!current() || busyRef.current || external) return;
      ticket!.active = false; invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress};
      setScannedInvitationLink(undefined);
      set(format ? NativeServerSignIn.formatInvitationCode(value) : value); setError(undefined);
    };
    const scan = async () => {
      if (!current() || busyRef.current || external || invitationScan.current || !qrScannerAvailable()) return;
      const controller = new AbortController();
      invitationScan.current = controller; setInvitationScanning(true); setError(undefined);
      const guard = () => current() && invitationScan.current === controller && !controller.signal.aborted;
      try {
        const raw = await scanQRCode({signal: controller.signal, isCurrent: guard, title: t('accounts.move.scanQR'), cancelLabel: t('action.cancel')});
        if (!guard() || raw === undefined) return;
        const link = parseJoinLink(raw), short = link ? undefined : parseShortInvitationCode(raw);
        if (!link && !short) {setError(Object.assign(new Error('invitation_qr_invalid'), {code: 'invitation_qr_invalid'})); return;}
        // A scan only edits this form; Continue still runs the canonical preview.
        ticket!.active = false; invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress};
        if (link) {setAddress(link.server); setScannedInvitationLink(raw); setCode('');}
        else {setCode(formatShortInvitationCode(short)!); setScannedInvitationLink(undefined);}
        setError(undefined);
      } catch (failure) {if (guard()) setError(failure);}
      finally {if (invitationScan.current === controller) {invitationScan.current = undefined; if (lifetime.current) setInvitationScanning(false);}}
    };
    const submit = () => {if (!current() || busyRef.current || !parsed) return;
      ticket!.active = false; const intent = Object.freeze({...parsed, publicPreview: true}); invitationIntent.current = intent;
      void run(async () => {try {await inspect(intent.server);} catch (failure) {
        if (lifetime.current && invitationIntent.current === intent && stageRef.current === 'invitation-entry') invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress, external};
        throw failure;
      }});
    };
    const rootStatus = NativeServerSignIn.invitationUnavailable(error)
      ? <FormError placement="inline">{t('web.directJoin.entryUnavailable')}</FormError> : status;
    return <FormLayout testID="accounts-invitation-entry" title={t('web.directJoin.title')} body={external ? undefined : t('web.directJoin.entryBody')}
      kicker={<AccountEntryWordmark label={t('accounts.brand')}/>}
      beforeActions={<AccountEntryDetails roomy>{rootStatus}
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('accounts.signIn.address')} value={address} onChangeText={change(setAddress)} autoCapitalize="none" autoCorrect={false} editable={!busy && !external && !scannedInvitationLink}/>
        {!external && !scannedInvitationLink && <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.code')} placeholder={t('web.directJoin.codePlaceholder')} value={code} onChangeText={change(setCode, true)}
          autoCapitalize="characters" autoCorrect={false} autoComplete="off" editable={!busy} onSubmitEditing={submit}/>}
        {!external && qrScannerAvailable() ? <AccountEntryScan hint={t('services.start.qrPhone')}>
          <Button variant="link" disabled={busy || invitationScanning} loading={invitationScanning || undefined} onPress={() => {void scan();}}>{t('accounts.move.scanQR')}</Button>
        </AccountEntryScan> : null}
      </AccountEntryDetails>} primaryAction={{id: 'preview-invitation-entry', label: t('action.continue'), disabled: busy || !parsed, loading: busy, onPress: submit}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }
  if (stage === 'invitation-account') {
    const ticket = invitationSwitch.current, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && lifetime.current && stageRef.current === 'invitation-account';
    const begin = (register: boolean) => {if (!current() || busyRef.current) return; void run(async () => {
      if (!current()) return; const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
      if (!current()) return;
      if (credentials.getSnapshot().session) {await directory(); return;}
      preparedAuthSource.current = undefined; account.current?.dispose(); const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
        && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
      const available = await source.prepare(); if (!current() || account.current !== source) return;
      if (!publishPreparedAccount(source, available, current)) return;
      stageRef.current = register && available.registration ? 'register' : 'portico'; setStage(stageRef.current);
    });};
    return <FormLayout testID="accounts-invitation-account" title={t('accounts.invitation.to') + ' ' + (preview?.serverName ?? '')}
      body={preview ? t('accounts.invitation.porticoBody', {server: preview.serverName}) : undefined}
      beforeActions={<AccountEntryDetails>{status}
        {preview && <AccountEntryLine>{preview.role === 'admin' ? t('accounts.invitation.admin') : t('accounts.invitation.member')}</AccountEntryLine>}
        {preview?.allLibraries && <AccountEntryLine>{t('server.people.allLibraries')}</AccountEntryLine>}
        {preview?.libraries.map((library, index) => <AccountEntryLine key={index}>{library.name}</AccountEntryLine>)}
      </AccountEntryDetails>} primaryAction={{id: 'sign-in-to-join', label: t('auth.signInWithAccount'), disabled: busy || !preview, loading: busy, onPress: () => begin(false)}}
      secondaryAction={{id: 'create-account-to-join', label: t('auth.createAccount'), disabled: busy || !preview, onPress: () => begin(true)}}
      detailsAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }
  if (stage === 'invitation-sign-out') {
    const ticket = invitationSwitch.current;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && !!ticket.scope && lifetime.current && stageRef.current === 'invitation-sign-out';
    const cancel = () => {if (!current() || busyRef.current) return; retireInvitationSwitch(); stageRef.current = 'address'; setStage('address'); void run(async () => {await inspect(ticket!.entry.server);});};
    return <FormLayout testID="accounts-invitation-sign-out" title={t('accounts.signOut.portico')} body={ticket?.username}
      beforeActions={status} primaryAction={{id: 'switch-invitation-account', label: t('action.signOut'), disabled: busy, loading: busy, onPress: () => {
        if (!current()) return; void run(async () => {
          if (!current()) return; await nativeAccounts.signOutPorticoAccount(ticket!.scope!);
          if (!current()) return;
          setUsername(''); setPassword(''); setCode('');
          preparedAuthSource.current = undefined; account.current?.dispose(); const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
            && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
          const available = await source.prepare();
          if (!current() || account.current !== source) return;
          if (!publishPreparedAccount(source, available, current)) return;
          stageRef.current = 'portico'; setStage('portico');
        });
      }}} secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: busy, onPress: cancel}}/>;
  }
  if (stage === 'invitation-code' || stage === 'invitation-local' || stage === 'invitation-portico') {
    const ticket = invitationOpening.current, renderedStage = stage, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationOpening.current === ticket && operation.current === ticket.source
      && lifetime.current && stageRef.current === renderedStage;
    const change = (set: (value: string) => void) => (value: string) => {if (current()) set(value);};
    const back = () => {if (current() && !busyRef.current) backRef.current();};
    const failure = ticket?.controller.failure(error);
    const invitationStatus = failure === 'unavailable' || failure === 'mistyped'
      ? <FormError placement="block">{t(failure === 'mistyped' ? 'web.directJoin.codeNotRight' : 'web.directJoin.entryUnavailable')}</FormError> : status;
    const differentAccount = () => {
      if (!current() || busyRef.current || ticket?.kind !== 'portico') return;
      try {
        const captured = ticket.controller.captureAccountSwitch(), handoff = {...captured, active: true};
        retireInvitation(); ticket.source.dispose(); operation.current = undefined;
        invitationIntent.current = captured.entry; invitationSwitch.current = handoff; setError(undefined); setPassword(''); setCode('');
        stageRef.current = 'invitation-sign-out'; setStage('invitation-sign-out');
      } catch (failure) {if (current()) setError(failure);}
    };

    if (failure === 'expired') return <StateLayout testID="accounts-invitation-expired" icon="i-warn" title={t('accounts.invitation.expiredTitle')} body={t('accounts.invitation.expiredBody')}
      primaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (stage === 'invitation-code') return <FormLayout testID="accounts-invitation-code" title={t('web.directJoin.title')}
      body={t('services.start.joinBody')} beforeActions={<>{invitationStatus}<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('services.start.invitation')} value={code}
        onChangeText={change(setCode)} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/></>}
      primaryAction={{id: 'preview-invitation', label: t('action.continue'), disabled: busy || !code.trim(), loading: busy, onPress: () => {
        if (!current()) return; void run(async () => {
          if (!current()) return; const value = await ticket!.controller.preview(code);
          if (!current()) return; invitationIntent.current = undefined; setInvitationPreview(value); setCode(''); const destination = ticket!.kind === 'portico' ? 'invitation-portico' : 'invitation-local'; stageRef.current = destination; setStage(destination);
        });
      }}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (failure === 'other-account') return <StateLayout testID="accounts-invitation-other-account" icon="i-person"
      title={t('accounts.invitation.otherAccountTitle')} body={t('web.signIn.error.invitationOtherAccount', {server: preview?.serverName ?? server!.name})}
      primaryAction={{id: 'different-account', label: t('accounts.invitation.notYou'), disabled: busy, onPress: differentAccount}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (!preview) return null;
    if (stage === 'invitation-portico' && ticket?.kind === 'portico') {
      const controller = ticket.controller;
      return <FormLayout testID="accounts-portico-invitation" title={t('accounts.invitation.to') + ' ' + preview.serverName}
        body={t('accounts.invitation.porticoBody', {server: preview.serverName})}
        beforeActions={<AccountEntryDetails>{invitationStatus}
          <AccountEntryLine>{preview.role === 'admin' ? t('accounts.invitation.admin') : t('accounts.invitation.member')}</AccountEntryLine>
          <AccountEntryLine tone="strong">{t('accounts.invitation.libraries')}</AccountEntryLine>
          {preview.allLibraries && <AccountEntryLine>{t('server.people.allLibraries')}</AccountEntryLine>}
          {preview.libraries.map((library, index) => <AccountEntryLine key={index}>{library.name}</AccountEntryLine>)}
          <AccountEntryLine tone="primary">{controller.username}</AccountEntryLine>
          <Button variant="link" disabled={busy} onPress={differentAccount}>{t('accounts.invitation.notYou')}</Button>
        </AccountEntryDetails>}
        primaryAction={{id: 'join-portico-invitation', label: t('accounts.invitation.joinAs', {username: controller.username}), disabled: busy, loading: busy,
          onPress: () => {if (!current()) return; void run(async () => {
            if (!current()) return; const result = await controller.join(preview);
            if (current()) consume(result);
          });}}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    }
    if (ticket?.kind !== 'local') return null;
    const localController = ticket.controller;
    return <FormLayout testID="accounts-local-invitation" title={t('accounts.invitation.to') + ' ' + preview.serverName}
      body={t('accounts.invitation.localPhoneBody', {server: preview.serverName})}
      beforeActions={<AccountEntryDetails>{invitationStatus}
        <AccountEntryLine tone="strong">{t('accounts.invitation.libraries')}</AccountEntryLine>
        {preview.allLibraries && <AccountEntryLine>{t('server.people.allLibraries')}</AccountEntryLine>}
        {preview.libraries.map((library, index) => <AccountEntryLine key={index}>{library.name}</AccountEntryLine>)}
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.name')} value={name} onChangeText={change(setName)} editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.username')} value={username} error={failure === 'username' ? t('web.directJoin.error.taken') : undefined} onChangeText={change(setUsername)} autoCapitalize="none" autoCorrect={false} editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('auth.password')} value={password} onChangeText={change(setPassword)} secureTextEntry autoComplete="new-password" editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.confirm')} value={setupPasswordConfirm} onChangeText={change(setSetupPasswordConfirm)} secureTextEntry autoComplete="new-password" editable={!busy}/>
      </AccountEntryDetails>}
      primaryAction={{id: 'join-local-invitation', label: t('web.directJoin.submit'), loading: busy,
        disabled: busy || !/^[A-Za-z0-9_.-]{3,64}$/.test(username.trim()) || !passwordStrength(password).acceptable || password !== setupPasswordConfirm,
        onPress: () => {if (!current()) return; void run(async () => {
          if (!current()) return; const result = await localController.join(preview, username, password, name);
          if (!current()) return; setPassword(''); setSetupPasswordConfirm(''); consume(result);
        });}}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
  }
  if (stage === 'local') {
    const source = operation.current;
    const links = <>
      <Button variant="link" disabled={busy} onPress={() => {
        if (!source || source !== operation.current || !lifetime.current || stageRef.current !== 'local') return;
        openLocalSignInPage(source);
      }}>{t('auth.forgotPassword')}</Button>
      <Button variant="link" disabled={busy} onPress={() => {void run(() => startTV('local'));}}>{t('auth.useCodeFromAnotherDevice')}</Button>
      {!tvDevice && !endedTarget.current && <Button variant="link" disabled={busy} onPress={() => {
        if (!source || source !== operation.current || !lifetime.current || stageRef.current !== 'local' || busyRef.current) return;
        try {openInvitationEntry(source.serverOrigin());} catch (failure) {setError(failure);}
      }}>{t('web.directJoin.have')}</Button>}
      {!tvDevice && server?.move === 'to_local' && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberCode);}}>{t('accounts.move.haveCode')}</Button>}
    </>;
    const signIn = async () => {consume(await operation.current!.signInLocal(username, password)); setPassword('');};
    if (!theme.isTV && source) return <ServerSignInPage busy={busy} error={status} passwordInvalid={passwordInvalid} serverName={server!.name}
      address={address.trim() ? address.trim().replace(/^https?:\/\//i, '') : new URL(source.serverOrigin()).host}
      username={username} password={password} onUsernameChange={setUsername} onPasswordChange={setPassword} links={links}
      onSignIn={() => {if (lifetime.current && stageRef.current === 'local' && source === operation.current) void run(signIn);}} onBack={back}/>;
    return layout(server!.name, t('accounts.signIn.localBody'), fields(field(t('auth.username'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')),
    action('local-sign-in', t('auth.signIn'), signIn, !username || !password),
    <AccountEntryActions tight>{links}</AccountEntryActions>);
  }
  if (stage === 'factor' && factorKind.current === 'portico' && !tvDevice) {
    // The phone Portico factor form acts only through its own opening: the exact issued
    // witness, source, challenge and the draft revision it renders.
    const opening = factorOpening, revision = registerDraftRev.current, entered = recovery ? recoveryCode : code;
    // Settlement only needs this original form, source and draft; admission additionally
    // needs the exact challenge, which a successful factor clears before it resolves.
    const formLive = () => lifetime.current && stageRef.current === 'factor' && !!opening && opening.owner.live
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    return layout(t('auth.enterVerificationCode'), t('services.signin.twoStep'),
      fields(recovery ? field(t('services.signin.recovery'), entered, edit(setRecoveryCode)) : twoStepField(entered, edit(setCode))),
      action('factor', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !entered || !formLive() || source.factorChallengeIdentity() !== opening.witness) return;
        try {
          await source.factor(entered, {signal: opening.life.signal, isCurrent: formLive}, opening.witness);
        } catch (refusal) {
          // A retired form publishes nothing; only the live original may surface a refusal.
          if (!formLive()) return;
          throw refusal;
        }
        // Only this own accepted completion settles and navigates.
        if (!formLive()) return;
        setCode(''); setRecoveryCode(''); setRecovery(false);
        await directory();
      }, !entered),
      recovery ? undefined : <Button variant="link" disabled={busy} onPress={() => {if (!formLive()) return; registerDraftRev.current++; setRecoveryCode(''); setRecovery(true);}}>{`${t('services.signin.lostPhone')} ${t('services.signin.recovery')}`}</Button>);
  }
  if (stage === 'factor') return layout(t('auth.enterVerificationCode'), undefined, fields(twoStepField(code, setCode)), action('factor', t('action.continue'), async () => {
    if (memberFactorOperation.current && memberFactorOperation.current === operation.current) {
      const result = await memberFactorOperation.current.completeMemberMoveFactor(code); memberFactorOperation.current = undefined; stopMemberMove(); consume(result);
    } else consume(await (operation.current!.directoryChallengePending() ? operation.current!.completeDirectorySecondFactor(code) : operation.current!.completeSecondFactor(code)));
    setCode('');
  }, !code));
  if (stage === 'portico' && !theme.isTV) {
    const source = account.current, ticket = invitationSwitch.current;
    const current = () => !!source && lifetime.current && stageRef.current === 'portico' && account.current === source
      && preparedAuthSource.current === source && invitationSwitch.current === ticket
      && (!ticket || (ticket.active && invitationIntent.current === ticket.entry));
    // Apple and Google are always offered; one Services has not turned on, or this device can't open, says so plainly.
    const onProvider = (id: 'apple' | 'google') => {if (!current() || busy) return;
      if (!availableProviders.some(value => value.id === id)) {setProviderNotice(id); return;}
      setProviderNotice(undefined); void run(async () => {
        if (!current() || !source) return;
        setProviderLoading(source);
        try {
          const tx = await source.provider(id, current); if (!current()) return; setTransaction(tx);
          if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {
            const caller = new AbortController(); await source.finishProvider(undefined, undefined, undefined, {signal: caller.signal, isCurrent: current});
            if (current()) await directory();
          } else if (current()) setStage('provider');
        } catch (error) {if (current()) throw error;}
        finally {if (lifetime.current) setProviderLoading(value => value === source ? undefined : value);}
      });};
    const onContinue = () => {if (!current() || busy || !authOptions?.password || !username || !password || !source) return; void run(async () => {
        if (!current()) return; try {await source.password(username, password, current); if (current()) await directory();} catch (error) {if (current()) throw error;}
      });};
    const onForgot = () => {if (!current() || busy || !authOptions?.recovery) return; void run(requestReset);};
    const onCreate = () => {if (!current() || busy || !authOptions?.registration) return; setPassword(''); setStage('register');};
    intentRunner.current = intent => {if (intent.kind === 'provider') onProvider(intent.id); else if (intent.kind === 'continue') onContinue(); else if (intent.kind === 'forgot') onForgot(); else onCreate();};
    return <SignInPage busy={busy} error={status ?? providerNoticeView} onOpenLink={openLegalLink}
      passwordEnabled={!!authOptions?.password} registration={!!authOptions?.registration} recovery={authOptions?.recovery !== false}
      username={username} password={password} onUsernameChange={value => {if (current()) setUsername(value);}} onPasswordChange={value => {if (current()) setPassword(value);}}
      onProvider={onProvider} onContinue={onContinue} onForgot={onForgot} onCreate={onCreate}
      onLocal={() => {if (!current() || busy) return; setPassword(''); setProviderNotice(undefined); setStage('address');}}/>;
  }
  if (stage === 'portico') return layout(t('accounts.signIn.portico'), t('auth.accountLead'), options?.password ? fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')) : undefined,
    options?.password ? action('account-password', t('auth.signIn'), async () => {await account.current!.password(username, password); await directory();}, !username || !password) : undefined,
    <AccountEntryChoices>{options?.providers.filter(providerOffered).map(value => <Button key={value.id} variant="glass" disabled={busy} onPress={() => {void run(async () => {
      const tx = await account.current!.provider(value.id); setTransaction(tx);
      if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {await account.current!.finishProvider(); await directory();} else setStage('provider');
    });}}>{t(value.id === 'google' ? 'auth.signInWithGoogle' : 'auth.signInWithApple')}</Button>)}
    {options?.registration && <Button variant="link" disabled={busy} onPress={() => {setPassword(''); setStage('register');}}>{t('auth.createAccount')}</Button>}
    {options?.recovery !== false && <Button variant="link" disabled={busy} onPress={() => {void run(requestReset);}}>{t('auth.forgotPassword')}</Button>}
    <Button variant="link" disabled={busy} onPress={() => {setPassword(''); setStage('address');}}>{t('accounts.signIn.local')}</Button>
    <AccountEntryChoicesLegal onOpenLink={openLegalLink}/></AccountEntryChoices>);
  if (stage === 'forgot') return layout(t('auth.forgotPasswordTitle'), t('auth.resetLinkLede'),
    fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username')),
    action('request-reset', t('auth.resetLinkAction'), requestReset, !username.trim()),
    <AccountEntryNotes>
      <AccountEntryNote>{t('auth.privacySameAnswer')}</AccountEntryNote>
      {/* collections 31 draws both privacy sentences on the phone's forgot screen. */}
      {theme.isTV ? null : <AccountEntryNote>{t('auth.noPasswordForSocial')}</AccountEntryNote>}
    </AccountEntryNotes>);
  if (stage === 'forgot-sent') return layout(t('auth.checkEmail'), t('auth.resetLinkSent'),
    // account 10 and 31 draw the link's lifetime under the lede, as web shows it.
    <AccountEntrySentNotes expiry={t('services.recovery.expiry')} privacy={t('auth.privacySameAnswer')}/>,
    action('back-to-sign-in', t('auth.backToSignIn'), async () => {if (lifetime.current) setStage('portico');}),
    <AccountEntryFootNote>{t('auth.noPasswordForSocial')}</AccountEntryFootNote>);
  if (stage === 'register') {
    // The opening this Submit was drawn for and the draft revision it drew. Both must
    // still be the live ones before the call and again when the answer arrives.
    const opening = registerOpening, revision = registerDraftRev.current, submitted = {username, name, email};
    const live = (source: NativePorticoSignIn) => lifetime.current && stageRef.current === 'register' && !!opening
      && opening.owner.live && registerOpeningRef.current === opening && opening.source === source && account.current === source
      && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    const usernameField = field(t('auth.username'), username, next => {setUsernameUnavailable(false); edit(setUsername)(next);}, false, 'username',
      !theme.isTV && usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, theme.isTV ? undefined : t('auth.usernameHelp'));
    const nameField = theme.isTV ? field(t('auth.yourName'), name, edit(setName))
      : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'));
    const emailField = field(t('auth.email'), email, edit(setEmail), false, 'email');
    return layout(t('auth.createYourAccount'), theme.isTV ? undefined : t('auth.oneAccount'), fields(
      ...(theme.isTV ? [usernameField, nameField, emailField] : [emailField, usernameField, nameField]),
      field(t('auth.password'), password, edit(setPassword), true, 'new-password'), !theme.isTV && <PasswordStrengthMeter key="strength" password={password}/>, ageTerms),
      action('register', t(theme.isTV ? 'auth.createAccount' : 'action.continue'), async () => {
        const source = opening?.source;
        if (!source || !live(source)) return;
        try {
          const value = await source.register(submitted.username, theme.isTV ? submitted.name : submitted.name.trim() || submitted.username.trim(), password, submitted.email, acceptTerms);
          if (!live(source)) return;
          setVerifyOpening(Object.freeze({source, registration: value})); setPassword(''); setMasked(value.maskedAddress); setStage('verify');
        } catch (refusal) {
          if (!live(source)) return;
          // The drawn username-field state is only the confirmed public-handle collision;
          // every other refusal keeps the ordinary form error path unchanged.
          if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, !acceptTerms || (theme.isTV ? (!username || !name || !password || !email) : (!username || !password || !email))), theme.isTV ? undefined : legalFooter);
  }
  if (stage === 'verify') {
    const opening = verifyOpening, revision = registerDraftRev.current;
    const live = () => !!opening && lifetime.current && stageRef.current === 'verify' && verifyOpeningRef.current === opening
      && account.current === opening.source && registerDraftRev.current === revision;
    const verified = opening?.registration.contactVerified === true;
    const edit = (apply: (next: string) => void) => (next: string) => {if (!live()) return; registerDraftRev.current++; apply(next);};
    return layout(verified ? t('auth.chooseUsername') : t('auth.checkEmail'), verified ? undefined : t('auth.codeSentTo', {address: masked}),
      verified ? fields(field(t('auth.username'), username, next => {if (!live()) return; setUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
        usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, t('auth.usernameHelp')),
        field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername')))
        : fields(codeField(t('auth.verificationCode'), code, edit(setCode))),
      action('verify', t(theme.isTV || verified ? 'action.continue' : 'auth.createAccount'), async () => {
        if (!opening || !live()) return;
        try {
          await opening.source.verifyRegistration(verified ? '' : code, opening.registration, {isCurrent: live}, verified ? {username, displayName: name} : undefined);
          if (live()) await directory();
        } catch (refusal) {
          if (!live()) return;
          const fresh = porticoUsernameUnavailable(refusal) ? opening.source.registrationAfterCollision(opening.registration) : undefined;
          if (fresh) {setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, verified ? username.trim().length < 3 : !code), verified || theme.isTV ? undefined : <AccountEntryResend help={t('services.signup.codeHelp')} action={
        <Button testID="accounts-registration-resend" variant="link" disabled={busy} onPress={() => {
          if (!opening || !live() || busyRef.current || acting.current) return;
          void run(async () => {
            if (!live()) return;
            try {
              const fresh = await opening.source.resendRegistration(opening.registration, {isCurrent: live});
              if (!live()) return;
              setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setCode(''); setMasked(fresh.maskedAddress);
            } catch (failure) {if (live()) throw failure;}
          });
        }}>{t('auth.sendCodeAgain')}</Button>}/>);
  }
  if (stage === 'provider') {
    // The transaction this form was drawn for and the draft it shows; both must still
    // be live before any completion or proof refresh and again after the answer.
    const opening = providerOpening, tx = transaction, revision = registerDraftRev.current;
    // The exact source is part of the opening, so a retained form can never borrow a
    // successor source; the predicate and its signal travel into the completion port.
    const live = () => lifetime.current && stageRef.current === 'provider' && !!opening && !!tx
      && opening.owner.live && providerOpeningRef.current === opening && opening.transaction === tx
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    // The drawn first-time provider step names itself and the provider it is continuing.
    const providerName = tx?.provider === 'apple' ? t('accounts.provider.apple') : tx?.provider === 'google' ? t('accounts.provider.google') : undefined;
    const phoneStep = !!tx?.needsUsername && !theme.isTV;
    const heading = phoneStep ? t('auth.chooseUsername') : t('accounts.signIn.portico');
    const lede = phoneStep ? t('services.provider.lastThing') : undefined;
    const continuing = tx?.needsUsername && providerName && !theme.isTV
      ? <AccountEntryNote>{t('services.provider.heading', {provider: providerName})}</AccountEntryNote> : undefined;
    // The address the provider chose to share; no relay label is claimed without a producer flag.
    const sharedAddress = !theme.isTV && !!tx?.verifiedEmail && tx.needsUsername
      ? <AccountEntryNote selectable>{`${t('auth.email')}: ${tx.verifiedEmail}`}</AccountEntryNote> : undefined;
    return layout(heading, lede, fields(
      continuing,
      sharedAddress,
      tx?.needsUsername && field(t('auth.username'), username, next => {setProviderUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
        !theme.isTV && providerUsernameUnavailable ? t('web.signIn.error.accountTaken') : undefined,
        theme.isTV ? undefined : t('services.provider.usernameHelp')),
      tx?.needsUsername && (theme.isTV ? field(t('auth.yourName'), name, edit(setName))
        : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'))),
      tx?.needsUsername && ageTerms,
      tx?.mfaRequired && codeField(t('auth.verificationCode'), code, edit(setCode)), !tx?.contactVerified && field(t('auth.email'), email, edit(setEmail))),
      action('provider-complete', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !live()) return;
        if (!tx?.contactVerified) {
          const challenge = await source.sendContact(email);
          if (!live()) return;
          contact.current = challenge.challengeId; setMasked(challenge.maskedAddress); setCode(''); setStage('contact');
        } else {
          try {
            await source.finishProvider(theme.isTV ? username || undefined : username.trim() || undefined,
              theme.isTV ? name || undefined : name.trim() || username.trim() || undefined, code || undefined,
              {signal: opening.life.signal, isCurrent: live}, !!tx?.needsUsername && acceptTerms);
          } catch (refusal) {
            // Only the confirmed public-handle collision marks this field, and only while
            // this very form and draft are still live; a stale refusal publishes nothing.
            if (!live()) return;
            if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setProviderUsernameUnavailable(true); return;}
            throw refusal;
          }
          if (!live()) return;
          await directory();
        }
      }, !!tx?.needsUsername && !acceptTerms), tx?.needsUsername && !theme.isTV ? legalFooter : undefined);
  }
  if (stage === 'contact') return layout(t('auth.checkEmail'), t('auth.codeSentTo', {address: masked}), fields(codeField(t('auth.verificationCode'), code, setCode)), action('contact', t('action.continue'), async () => {
    await account.current!.verifyContact(contact.current!, code); setTransaction(value => value && {...value, contactVerified: true}); setCode(''); setStage('provider');
  }, !code));
  if (stage === 'servers' && directoryList === 'failed') return <StateLayout testID="accounts-server-list-failed" icon="i-warn" tone="error"
    title={t('web.chooser.loadFailed')} body={t('web.chooser.loadFailedBody')}
    // connect#13: the one button that helps holds the starting focus.
    primaryAction={{id: 'retry-server-list', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, preferredFocus: true, onPress: () => {void run(directory);}}}/>;
  if (stage === 'servers' && target && directoryIdentityUnconfirmed(error)) return <StateLayout testID="accounts-server-unconfirmed" icon="i-question"
    title={t('web.chooser.unconfirmedTitle', {server: target.name})} body={t('web.chooser.unconfirmedBody')}
    primaryAction={{id: 'retry-identity', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  const singleTVFailure = tvDevice && servers.length === 1 && !!target && directoryAccessRefused(error);
  const singlePhoneRefusal = !tvDevice && directoryList === 'ready' && servers.length === 1 && !!target
    && servers[0].id === target.id && directoryAccessRefused(error);
  if (stage === 'servers' && target && directoryClockSkew(error)) return <StateLayout testID="accounts-server-clock" icon="i-clock"
    title={t('web.chooser.clockTitle', {server: target.name})} body={t('web.chooser.clockBody')}
    primaryAction={{id: 'retry-clock', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  if (stage === 'servers' && target && directoryAccessRefused(error) && !singleTVFailure && !singlePhoneRefusal) return <DirectoryAccessRefused serverName={target.name} busy={busy}
    onOther={chooseAnother} onRefresh={() => {cancelDirectory(); void run(directory);}}/>;
  if (stage === 'servers' && target && directoryView === 'opening' && !allDirectoryOffline) return <StateLayout testID="accounts-server-opening" title={t('web.chooser.connecting', {server: target.name})}
    primaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  if (allDirectoryOffline && !hostedWaiting && !directoryAccessRefused(error) && (!target || directoryView === 'opening' || directoryView === 'waiting' || directoryView === 'unreachable')) {
    const captured = servers, scope = accountScope.current, attempt = directoryAttempt.current;
    const current = () => !!scope && lifetime.current && stageRef.current === 'servers' && directoryAttempt.current === attempt && accountScope.current === scope
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === scope.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === scope.familyId;
    const capturedDownload = downloadValue?.opening;
    const openDownloads = () => {
      if (!current() || busyRef.current || !downloadValue?.available || !capturedDownload?.active || downloadsOwner.current !== capturedDownload
        || capturedDownload.source !== downloads || !capturedDownload.source.current() || !capturedDownload.source.getSnapshot().available) return;
      capturedDownload.source.open();
    };
    const Offline = tvDevice ? TVOfflineServersPage : OfflineServersPage;
    return <Offline onDownloads={!!capturedDownload && capturedDownload.source === downloads && capturedDownload.active && downloadValue?.available && downloads?.current() ? openDownloads : undefined} username={nativeAccounts.accountCredentials().getSnapshot().session?.account.username}
      onSettings={() => openAccountWorkspace(scope, current)}
      onJoin={() => {if (current() && !busyRef.current) openInvitationEntry();}}
      onFind={() => {if (!current() || busyRef.current) return; cancelDirectory(); setAddress(''); setPassword(''); setCode(''); stageRef.current = 'address'; setStage('address');}}
      rows={offlineRows} scope={scope} revision={attempt} isCurrent={current} busy={busy} activeId={target?.id}
      retryIn={target ? retryIn : undefined} retryProgress={target ? (retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1)) : undefined}
      onRetry={id => {if (!current()) return; const row = captured.find(value => value.id === id); if (row) connectDirectory(row);}}
      onBack={() => {if (current()) back();}} onRefresh={() => {if (current()) {cancelDirectory(); void run(directory);}}}/>;
  }
  if (stage === 'servers' && target && directoryView === 'unreachable' && !singleTVFailure) {
    const body = directoryOfflineBody(target);
    return <StateLayout testID="accounts-server-unreachable" title={t('web.chooser.unreachableTitle', {server: target.name})} body={t(body.key, body.values)}
      primaryAction={{id: 'try-again', label: t('web.chooser.tryAgain'), loading: busy, onPress: () => connectDirectory(target, true)}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  }
  if (hostedWaiting && target) {
    const captured = target, source = operation.current, attempt = directoryAttempt.current, scope = accountScope.current;
    const credentials = !tvDevice && scope ? nativeAccounts.accountCredentials() : undefined;
    const original = Object.freeze({baseUrl: captured.baseUrl, serverId: captured.id, publicKey: captured.publicKey});
    const current = () => {
      const live = credentials?.getSnapshot().session;
      return lifetime.current && stageRef.current === 'servers' && targetRef.current === captured &&
        operation.current === source && directoryAttempt.current === attempt && (tvDevice ||
        accountScope.current === scope && (!source || source.ownerRecoveryCurrent()) &&
        captured.baseUrl === original.baseUrl && captured.id === original.serverId && captured.publicKey === original.publicKey &&
        (!scope || live?.account.id === scope.accountId && live.familyId === scope.familyId));
    };
    return <StateLayout testID="accounts-hosted-waiting" icon="i-cloud-off" title={t('web.chooser.accountWaitingTitle')}
      body={t('web.chooser.accountWaitingBody', {server: captured.name})} retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
      // connect#11 draws Check now with the ring: the one action holds the starting focus.
      primaryAction={{id: 'check-hosted-now', label: t('web.chooser.checkNow'), loading: busy, disabled: busy, preferredFocus: true, onPress: () => {if (current()) connectDirectory(captured, true);}}}
      // "Can't use your Portico Account?" led to the owner's reset file, which on a Portico Accounts server needs the
      // rebuilt server to give the owner a session (asked for; _scratch/builderU/SERVER-NEEDS.md): phones offer another server, as TVs do.
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: () => {if (current()) chooseAnother();}}}/>;
  }
  if (stage === 'servers' && target && directoryView === 'waiting') return <StateLayout testID="accounts-server-waiting" title={t('web.chooser.waitingTitle', {server: target.name})}
    body={t('web.chooser.waitingBody')} retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
    primaryAction={{id: 'check-now', label: t('web.chooser.checkNow'), loading: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  if (stage === 'servers' && (directoryList === 'ready' && servers.length === 0 || singleTVFailure || singlePhoneRefusal)) {
    const source = operation.current, attempt = directoryAttempt.current, scope = accountScope.current, capturedTarget = target;
    const current = () => !!scope && lifetime.current && stageRef.current === 'servers' && operation.current === source
      && directoryAttempt.current === attempt && accountScope.current === scope && (!capturedTarget || targetRef.current === capturedTarget)
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === scope?.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === scope?.familyId;
    const notice = (singleTVFailure || singlePhoneRefusal) && capturedTarget
      ? {title: t('web.chooser.refusedTitle', {server: capturedTarget.name}), body: t('web.chooser.refusedBody')} : undefined;
    const capturedDownload = downloadValue?.opening;
    const openDownloads = () => {
      if (!current() || busyRef.current || !downloadValue?.available || !capturedDownload?.active || downloadsOwner.current !== capturedDownload
        || capturedDownload.source !== downloads || !capturedDownload.source.current() || !capturedDownload.source.getSnapshot().available) return;
      capturedDownload.source.open();
    };
    const EmptyDirectory = tvDevice ? TVNoServersPage : PhoneNoServersPage;
    return <EmptyDirectory {...(!tvDevice ? {onDownloads: !!capturedDownload && capturedDownload.source === downloads && capturedDownload.active && downloadValue?.available && downloads?.current() ? openDownloads : undefined,
      username: nativeAccounts.accountCredentials().getSnapshot().session?.account.username,
      onSettings: () => openAccountWorkspace(scope, current)} : {})} isCurrent={current} notice={notice} busy={busy} error={notice ? undefined : status} onBack={() => {if (current()) back();}}
      onJoin={(raw, origin) => {
        if (!current() || busyRef.current || acting.current) return;
        const link = NativeServerSignIn.parseInvitationEntry(raw, origin);
        if (!link) {setError(Object.assign(new Error('invitation_incomplete'), {code: 'invitation_incomplete'})); return;}
        cancelDirectory(); invitationIntent.current = Object.freeze({...link}); setPassword(''); setCode(''); setAddress(link.server);
        stageRef.current = 'address'; setStage('address'); void run(async () => {await inspect(link.server);});
      }}
      onFind={() => {if (!current() || busyRef.current) return; cancelDirectory(); setAddress(''); setPassword(''); setCode(''); stageRef.current = 'address'; setStage('address');}}
      onRefresh={() => {if (current()) void run(directory);}}
      onShare={() => {if (!current() || busyRef.current) return; void run(async () => {
        if (!current()) return;
        await shareDownloadPage();
      });}}
      onSignOut={() => {if (current() && !busyRef.current) {stageRef.current = 'account-sign-out'; setStage('account-sign-out');}}}/>;
  }
  if (stage === 'servers') return layout(t('portico.landing.pickTitle'), servers.length ? t('accounts.session.portico') : t('web.hostedAccount.noServersBody'),
    <AccountEntryServerList>{directoryRows(server && server.signIn === 'portico' ? servers.filter(value => value.id === server.id) : servers).map((row, index) => {
      const value = servers.find(item => item.id === row.id)!;
      // TV: the list has no primary action, so the first server takes the remote's focus on arrival
      // and again when a failed connection hands control back (P2-74 X-01, E-tv-01).
      return <AccountEntryServerRow key={row.id} name={row.name} owner={row.owner ? t('accounts.settings.owner') : undefined}
        subtitle={row.subtitle ? t(row.subtitle.key, row.subtitle.values) : undefined} offline={row.offline} disabled={busy} preferredFocus={tvDevice && index === 0 && !busy}
        accessibilityLabel={`${row.name}${row.owner ? `, ${t('accounts.settings.owner')}` : ''}${row.subtitle ? `, ${t(row.subtitle.key, row.subtitle.values)}` : ''}`}
        onPress={() => connectDirectory(value)}/>;
    })}</AccountEntryServerList>, undefined,
    <Button variant="link" disabled={busy} onPress={() => setStage('account-sign-out')}>{t('accounts.signOut.portico')}</Button>);
  if (stage === 'account-sign-out') return layout(t('accounts.signOut.portico'), undefined, undefined,
    action('account-sign-out', t('action.signOut'), async () => {
      await nativeAccounts.signOutPorticoAccount(accountScope.current!);
      // Not you? during a move: the server's proof stays, and the next Portico Account signs in for it.
      const move = memberMoveRef.current;
      if (move && memberOperation.current === operation.current && move.getSnapshot().phase === 'portico') {setStage('member-move'); return;}
      reset();
    }));
  if (pinRecovery) {
    const captured = pinRecovery;
    const current = () => lifetime.current && !captured.retired && pinRecoveryRef.current === captured
      && operation.current === captured.operation && profileRef.current === captured.profile && stageRef.current === 'profile-pin';
    return <ProfileRecoveryPage controller={captured.controller} profileName={captured.profile.name} username={username}
      onOpen={value => {
        if (!current() || captured.controller.getSnapshot().phase !== 'done' || !/^\d{4}$/.test(value) || busyRef.current || acting.current) return;
        retireRecovery(captured); setCode(value);
        void run(async () => {await captured.operation.chooseProfile(captured.profile.id, value, canRemember && remember); if (lifetime.current && operation.current === captured.operation) onComplete();});
      }}
      onClose={() => {if (!current()) return; retireRecovery(captured); setCode(''); setError(undefined);}}/>;
  }
  if (stage === 'profiles') {
    const originalOperation = operation.current!;
    const currentPicker = () => lifetime.current && operation.current === originalOperation && stageRef.current === 'profiles'
      && !busyRef.current && !acting.current;
    const selectProfile = (id: string) => {
      if (!currentPicker()) return;
      const selected = profiles.find(item => item.id === id);
      if (!selected) return;
      if (!theme.isTV) afterEntry.clear();
      if (selected.pinRequired) openPIN(selected, originalOperation); else void run(() => choose(id));
    };
    // TV (F-PROFILE-7): one centred row of faces and nothing else; Back is the remote key.
    if (theme.isTV) return <TVWhosWatching testID="accounts-entry">
      <ProfilePicker focusScope={`tv.auth.profiles.${server!.id}`} focusBusy={busy} title={t('accounts.profiles.onServer', {server: server!.name})} profiles={profiles.map(value => ({id: value.id, name: value.name, ...profileFace(value), primary: value.primary, pinRequired: value.pinRequired, disabled: busy}))} preferredProfileId={profileFocus.get(originalOperation)}
        onFocusChange={id => {if (id && operation.current === originalOperation) profileFocus.set(originalOperation, id);}}
        onSelect={selectProfile}/>
      {canRemember && <TVPickerRememberRow checked={remember} onChange={setRemember} busy={busy}/>}
      {status}
      <BootWaitLine active={busy} serverName={server!.name} testID="accounts-profile-wait"/>
    </TVWhosWatching>;
    // Add profile and Manage profiles open Profiles from the main profile, after its PIN if it has one (rules 25 and 27).
    const main = profiles.find(value => value.primary);
    const openManagement = () => {
      if (!currentPicker() || !main) return;
      afterEntry.request({kind: 'profiles'}, main.id);
      if (main.pinRequired) openPIN(main, originalOperation); else void run(() => choose(main.id));
    };
    const profileManagement = main ? {onManage: openManagement, ...(profiles.length < 8 ? {onAdd: openManagement} : {})} : {};
    return <ProfilesPage testID="accounts-entry" onBack={backAction ? back : undefined} backDisabled={busy}
      picker={<ProfilePicker focusScope={`tv.auth.profiles.${server!.id}`} focusBusy={busy} profiles={profiles.map(value => ({id: value.id, name: value.name, ...profileFace(value), primary: value.primary, pinRequired: value.pinRequired, disabled: busy}))} preferredProfileId={theme.isTV ? profileFocus.get(originalOperation) : undefined}
      onFocusChange={id => {if (id && operation.current === originalOperation) profileFocus.set(originalOperation, id);}} title={t('auth.whosWatching')} {...profileManagement} description={t('accounts.profiles.serverContext', {server: server!.name})}
      onSelect={selectProfile}/>}>
    {canRemember && <RememberProfileRow focusId="accounts-remember-profile" checked={remember} onChange={setRemember} disabled={busy} underFaces/>}
    {status}
    <BootWaitLine active={busy} serverName={server!.name} testID="accounts-profile-wait"/></ProfilesPage>;
  }
  if (theme.isTV && stage === 'tv-move-handoff' && server) return <TVMoveHandoff serverName={server.name}
    url={new URL('/sign-in', operation.current!.inspectedOrigin()).href} onBack={back}/>;
  if (theme.isTV && stage === 'profile-forgot-pin') return <TVForgotPINHandoff profileName={profile!.name}
    serverAddress={operation.current!.serverOrigin()} server={{id: server!.id, signIn: server!.signIn}} onBack={back}/>;
  if (stage === 'profile-pin' && pinOpening.current) {
    const opening = pinOpening.current, {profile: target, operation: source} = opening;
    const allowed = () => stillPIN(opening) && !pinRecoveryRef.current && !busyRef.current && !acting.current;
    const changePIN = (value: string) => {
      if (!allowed()) return;
      // Once the viewer edits the boxes, the previous refusal must not clear the new input.
      if (wrongCodeTimer.current) {clearTimeout(wrongCodeTimer.current); wrongCodeTimer.current = undefined;}
      setCode(value);
    };
    const submitPIN = (value: string) => {
      if (!allowed()) return;
      void run(async () => {
        await source.chooseProfile(target.id, value, canRemember && remember);
        if (stillPIN(opening)) onComplete();
      });
    };
    const backPIN = () => {if (allowed()) back();};
    if (theme.isTV) return <TVProfilePINForm
      profile={{id: target.id, name: target.name, ...profileFace(target), primary: target.primary, pinRequired: true}}
      code={code} onCodeChange={changePIN} busy={busy} error={status} onBack={backPIN}
      {...(canRemember ? {remember: {checked: remember, onChange: setRemember}} : {})}
      hints={busy ? <BootWaitLine active serverName={server?.name} testID="accounts-profile-wait"/> : undefined}
      onSubmit={submitPIN}
      onForgotPIN={() => {
        if (!allowed()) return;
        pinOpening.current = undefined; stageRef.current = 'profile-forgot-pin';
        setCode(''); setError(undefined); setStage('profile-forgot-pin');
      }}/>;
    return <ProfilePINPage testID="accounts-entry" profile={{name: target.name, face: profileFace(target)}}
      value={code} onChange={changePIN} onSubmit={submitPIN} busy={busy} rejected={!!error} status={status}
      remember={canRemember ? <RememberProfileRow focusId="accounts-remember-profile" checked={remember} onChange={setRemember} disabled={busy}/> : undefined}
      hints={<BootWaitLine active={busy} serverName={server?.name} testID="accounts-profile-wait"/>} onBack={backPIN}
      onForgot={() => {
        if (!allowed()) return;
        try {
          setError(undefined);
          const next: PINRecovery = {profile: target, operation: source, controller: source.profileRecovery(target.id), retired: false};
          pinOpening.current = {...opening};
          pinRecoveryRef.current = next; setPinRecovery(next);
        } catch (failure) {setError(failure);}
      }}/>;
  }
  const consentBody = tvKind === 'local' && server
    ? tvState.verificationUri ? t('accounts.consent.local', {server: server.name, address: tvState.verificationUri}) : undefined
    : t('accounts.consent.portico');
  const approval = renderedTV?.approval();
  const retryApproval = approval?.phase === 'failed' ? () => {void renderedTV?.retryApproval();} : undefined;
  const newCode = (approval ? approval.phase === 'terminal' && renderedTV?.canTypeCredentials()
    : tvState.phase === 'error' || tvState.phase === 'denied' || error)
    ? () => {void renderedTV?.newCode();} : undefined;
  // A Portico code that could not be created keeps the Activate composition: the card says why and the screen asks again
  // every 30 seconds through newCode, which refuses a second request while one is in flight.
  if (noCodeNow) return <TVCodePresenter mode="portico" body={consentBody} busy={busy} reason={tvReason() ?? t('activation.unreachable')}
    onTypeCredentials={() => {void renderedTV?.credentials();}} onBack={codeBack} credentialsDisabled={!renderedTV?.canTypeCredentials()}
    onLocal={tvDoor ? () => {if (renderedTV?.current() && !busyRef.current) {back(); setError(undefined); stageRef.current = 'address'; setStage('address');}} : undefined}/>;
  if (tvDevice) return <TVCodePresenter {...(tvKind === 'local' ? {mode: 'local' as const, serverName: server!.name} : {mode: 'portico' as const})} body={consentBody}
    verificationUri={tvState.verificationUri} verificationUriComplete={tvState.verificationUriComplete} userCode={tvState.userCode}
    status={<>{status}{(tvState.phase === 'denied' || tvState.phase === 'error') && <AccountEntryCodeNote>{t(tvState.phase === 'denied' ? 'activation.declined' : 'activation.failed')}</AccountEntryCodeNote>}</>}
    busy={busy} onTypeCredentials={() => {void renderedTV?.credentials();}} onBack={codeBack}
    onLocal={tvDoor ? () => {if (renderedTV?.current() && !busyRef.current) {back(); setError(undefined); stageRef.current = 'address'; setStage('address');}} : undefined} onNewCode={newCode} onRetry={retryApproval} retryAt={approval?.retryAt} credentialsDisabled={!!approval && !renderedTV?.canTypeCredentials()}/>;
  // A phone or tablet signing in with a code another device approves: its own title ("Use a code from another device", the
  // button that opened it), not the TV's "Activate Portico on this TV".
  return layout(t('auth.useCodeFromAnotherDevice'), consentBody,
    <AccountEntryCode qr={tvState.verificationUriComplete} code={tvState.userCode ?? t('activation.preparingCode')} address={tvState.verificationUri}
      note={t(tvState.phase === 'denied' ? 'activation.declined' : tvState.phase === 'error' ? 'activation.failed' : 'activation.codeRefreshes')}/>,
    retryApproval ? {id: 'retry-approval', label: t('action.tryAgain'), disabled: busy || (approval?.retryAt ?? 0) > Date.now(), loading: busy, onPress: retryApproval} : newCode ? {id: 'new-code', label: t('activation.getNewCode'), disabled: busy, loading: busy, onPress: newCode} : undefined);
}
