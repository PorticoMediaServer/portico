import React from 'react';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {AccountEntryActions} from '../../../../kit/accounts/AccountEntryParts';
import {EndedMoveCodeAction} from '../../../../kit/accounts/EndedMoveCodeAction';
import {nativeAccounts} from '../../../../platform/accounts';
import {TVCredentialForm, TVFrontDoor, TVRequiredSetupHandoff} from '../../../../tv/auth';
import {AccountWorkspacePage} from '../../../touch/accounts/AccountWorkspacePage';
import {SignInPage} from '../../../touch/accounts/SignInPage';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders welcome, ended-sign-in, TV credentials/required-setup, account workspace, and account sign-out stages. */
export function renderEntryWelcomeStage({view, props, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {onBack, endedSignIn, resumeAccount} = props;
  const {
    latestEndedHint, theme, stage, setStage, busy, setError, address, setAddress, username, setUsername,
    password, setPassword, setCode, server, endedTarget, tvKind, requiredSetup, setRequiredSetup,
    memberMoveRef, memberOperation, lifetime, operation, account, busyRef, stageRef, stageError,
    accountWorkspace, retireAccountWorkspace, accountScope, acting, run, reset, openInvitationEntry,
    back, tvDoor, consume, directory, portico, setProviderNotice, startAccount, tvDoorTried,
    beginEndedMemberCode, inspect, action, tvDevice, openLegalLink, openEndedSignInPage,
  } = view;
  const {field, fields, status, providerNoticeView, layout, tvReason} = kit;

  if (tvDevice && stage === 'tv-required-setup' && requiredSetup) {
    return <TVRequiredSetupHandoff serverName={requiredSetup.serverName}
      requirement={requiredSetup.requirement} url={new URL('/settings/profile', requiredSetup.serverOrigin).href} busy={busy}
      copy={{body: requiredSetup.requirement === 'password' ? 'next.auth.tv.requiredPasswordBody' : 'next.auth.tv.requiredTwoStepBody', reference: 'next.auth.tv.handoffHint'}}
      onSignInAgain={() => {if (!busy) {setRequiredSetup(undefined); setError(undefined); setStage('tv-credentials');}}} onBack={back}/>;
  }

  if (tvDevice && stage === 'tv-credentials') {
    return <TVCredentialForm {...(tvKind === 'local'
      ? {kind: 'local' as const, serverName: server!.name}
      : {kind: 'portico' as const, usernamePlaceholder: 'auth.usernameOrEmailPlaceholder' as const})}
      username={username} password={password} onUsernameChange={setUsername} onPasswordChange={setPassword}
      busy={busy} error={status} onBack={back} onSubmit={() => {void run(async () => {
        if (tvKind === 'portico') {await account.current!.password(username, password); setPassword(''); await directory();}
        else {const result = await operation.current!.signInLocal(username, password); setPassword(''); consume(result);}
      });}}/>;
  }

  if (stage === 'ended-sign-in' && endedSignIn) {
    return layout(
      endedSignIn.serverName,
      t('web.signIn.ended', {server: endedSignIn.serverName}),
      endedSignIn.signIn === 'local' ? fields(
        field(t('accounts.signIn.address'), address, setAddress),
        field(t('auth.username'), username, setUsername, false, 'username'),
        field(t('auth.password'), password, setPassword, true, 'password'),
      ) : undefined,
      endedSignIn.signIn === 'local' ? action('sign-in-again', t('auth.signIn'), async () => {
        if (!lifetime.current || stageRef.current !== 'ended-sign-in') return;
        endedTarget.current = Object.freeze({serverId: endedSignIn.serverId, fingerprint: endedSignIn.fingerprint});
        const source = await inspect(address);
        if (!source || !lifetime.current || operation.current !== source) return;
        source.assertSignInTarget(endedSignIn.serverId, endedSignIn.fingerprint);
        const result = await source.signInLocal(username, password);
        if (lifetime.current && operation.current === source) {setPassword(''); consume(result);}
      }, !address.trim() || !username.trim() || !password) : action('portico-again', t('accounts.signIn.continuePortico'), portico),
      <AccountEntryActions>
        {!tvDevice && endedSignIn.signIn === 'portico' && <EndedMoveCodeAction scope={endedSignIn} busy={busy}
          isCurrent={() => lifetime.current && stageRef.current === 'ended-sign-in' && latestEndedHint.current === endedSignIn}
          onStart={(guard, signal) => run(() => beginEndedMemberCode(guard, signal))}/>}
        {endedSignIn.signIn === 'local' && <Button variant="link" disabled={busy} onPress={() => {
          if (lifetime.current && stageRef.current === 'ended-sign-in') openEndedSignInPage(endedSignIn);
        }}>{t('auth.forgotPassword')}</Button>}
        <Button variant="link" disabled={busy} onPress={() => {if (lifetime.current && stageRef.current === 'ended-sign-in') {setUsername(''); setPassword(''); reset(); onBack?.();}}}>{t('auth.useDifferentAccount')}</Button>
      </AccountEntryActions>,
    );
  }

  if (stage === 'account-workspace' && accountWorkspace.current?.createSource) {
    const ticket = accountWorkspace.current;
    const current = () => ticket.active && accountWorkspace.current === ticket && lifetime.current && stageRef.current === 'account-workspace';
    const leave = (destination: 'servers' | 'address' | 'account-sign-out' | 'invitation-entry') => {
      if (!current()) return;
      const live = nativeAccounts.accountCredentials().getSnapshot().session;
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

  if (stage === 'welcome' && theme.isTV) {
    return <TVFrontDoor busy={busy} reason={tvReason()} autoStart={tvDoor && !tvDoorTried.current && !resumeAccount && !stageError}
      onPortico={() => {tvDoorTried.current = true; void run(portico);}} onLocal={() => {
        if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) {
          setAddress(''); setUsername(''); setPassword(''); setError(undefined); stageRef.current = 'address'; setStage('address');
        }
      }}/>;
  }

  if (stage === 'welcome') {
    return <SignInPage busy={busy} error={status ?? providerNoticeView} onOpenLink={openLegalLink}
      passwordEnabled registration recovery username={username} password={password}
      onUsernameChange={value => {if (lifetime.current && stageRef.current === 'welcome') setUsername(value);}}
      onPasswordChange={value => {if (lifetime.current && stageRef.current === 'welcome') setPassword(value);}}
      onProvider={id => startAccount({kind: 'provider', id})} onContinue={() => {if (username && password) startAccount({kind: 'continue'});}}
      onForgot={() => startAccount({kind: 'forgot'})} onCreate={() => startAccount({kind: 'create'})}
      onLocal={() => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) {setError(undefined); setProviderNotice(undefined); stageRef.current = 'address'; setStage('address');}}}/>;
  }

  if (stage === 'account-sign-out') {
    return layout(
      t('accounts.signOut.portico'),
      undefined,
      undefined,
      action('account-sign-out', t('action.signOut'), async () => {
        await nativeAccounts.signOutPorticoAccount(accountScope.current!);
        // Not you? during a move: the server's proof stays, and the next Portico Account signs in for it.
        const move = memberMoveRef.current;
        if (move && memberOperation.current === operation.current && move.getSnapshot().phase === 'portico') {setStage('member-move'); return;}
        reset();
      }),
    );
  }

  return undefined;
}
