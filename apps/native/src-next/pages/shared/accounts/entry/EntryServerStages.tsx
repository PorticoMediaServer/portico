import React from 'react';
import {moveOfferDays} from '../../../../../../../packages/client-core/src/sign-in';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button, TextField} from '../../../../kit/controls';
import {AccountEntryActions, AccountEntryAddress, AccountEntryDetails, AccountEntryLine, AccountEntryNearby, AccountEntryNearbyLine, AccountEntryNearbyServer} from '../../../../kit/accounts/AccountEntryParts';
import {PhoneAccountMoveNotice} from '../../../../kit/accounts/PhoneAccountMoveNotice';
import {PhoneFindServer} from '../../../../kit/accounts/PhoneFindServer';
import {PhoneOwnerReset} from '../../../../kit/accounts/PhoneOwnerReset';
import {TVMoveHandoff} from '../../../../tv/auth';
import {TVIdentityCodeForm} from '../../../../tv/auth/TVIdentityCodeForm';
import {MoveCodePage} from '../../../touch/accounts/MoveCodePage';
import {MovePasswordPage} from '../../../touch/accounts/MovePasswordPage';
import {ServerSignInPage} from '../../../touch/accounts/ServerSignInPage';
import {SetupPage} from '../../../touch/accounts/SetupPage';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders direct-server discovery, setup, identity-code, local sign-in, owner reset, and member-move stages. */
export function renderEntryServerStage({view, props, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {onComplete, onSetupComplete} = props;
  const {
    theme, stage, setStage, busy, setError, address, setAddress, username, setUsername, password, setPassword,
    code, setCode, resetLocation, setResetLocation, ownerResetOpening, setupAddress, server,
    identityCode, setIdentityCode, identityWrong, setIdentityWrong, identityAddress, endedTarget,
    memberMove, moveSnapshot, memberMoveRef, memberOperation, stopMemberMove,
    nearby, nearbyStatus, setNearbyRefresh, nearbySource, lifetime, operation, busyRef, stageRef,
    run, openInvitationEntry, back, consume, startTV, portico,
    beginMemberMove, beginMemberCode, createMoveAccount, differentMoveAccount,
    inspect, confirmCode, action, backAction, passwordInvalid, setupTarget, tvDevice,
    openLocalSignInPage, localNetworkNote,
  } = view;
  const {field, fields, status, FormLayout, EntryLayout, formStep, layout} = kit;

  if (stage === 'setup-confirm') {
    return layout(
      t('accounts.setup.confirmTitle'),
      t('accounts.setup.confirmBody'),
      <AccountEntryDetails>
        <AccountEntryLine tone="strong">{server!.name}</AccountEntryLine>
        <AccountEntryLine tone="caption">{new URL(setupAddress).host}</AccountEntryLine>
        <AccountEntryLine>{t('accounts.setup.confirmWarning')}</AccountEntryLine>
      </AccountEntryDetails>,
      action('confirm-setup', t('accounts.setup.confirmAction'), async () => {if (lifetime.current && stageRef.current === 'setup-confirm') setStage('setup');}),
    );
  }

  // First-run setup on the shared controller; it pairs with the same first-contact proof as sign-in.
  if (stage === 'setup' && setupTarget) {
    return <SetupPage target={setupTarget}
      onAdmitted={id => {if (!lifetime.current || stageRef.current !== 'setup') return; onSetupComplete?.(id); onComplete();}}
      onLeave={() => {if (lifetime.current && stageRef.current === 'setup') setStage('setup-confirm');}}/>;
  }

  if (stage === 'address') {
    const ticket = nearbySource.current;
    const current = () => !!ticket?.active && nearbySource.current === ticket && lifetime.current && stageRef.current === 'address';
    if (!theme.isTV) {
      return <PhoneFindServer busy={busy} error={status} address={address} onAddressChange={setAddress}
        found={nearby.map(value => ({origin: value.origin, name: value.server.name, host: new URL(value.origin).host, setupRequired: value.server.setupRequired === true}))}
        searching={nearbyStatus === 'loading'} failed={nearbyStatus === 'failed'} localNetworkNote={localNetworkNote}
        onContinue={() => {if (address.trim()) void run(async () => {await inspect();});}}
        onChoose={origin => {if (!current() || busyRef.current) return; setAddress(origin); void run(async () => {await inspect(origin);});}}
        onSearchAgain={() => {if (!current() || busyRef.current) return; ticket!.active = false; setNearbyRefresh(value => value + 1);}}
        onPortico={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) back();}}
        onInvitation={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) openInvitationEntry();}}
        onBack={() => {if (lifetime.current && stageRef.current === 'address' && !busyRef.current) back();}}/>;
    }
    return layout(
      t('accounts.signIn.local'),
      undefined,
      fields(field(t('accounts.signIn.address'), address, setAddress)),
      action('inspect', t('action.continue'), async () => {await inspect();}, !address.trim()),
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
      </AccountEntryNearby>,
    );
  }

  if (stage === 'owner-reset' || stage === 'owner-reset-code' || stage === 'owner-reset-done') {
    const ticket = ownerResetOpening.current, source = ticket?.source, renderedStage = stage;
    const current = () => !!ticket && ownerResetOpening.current === ticket && operation.current === source
      && !ticket.controller.signal.aborted && lifetime.current && stageRef.current === renderedStage;
    const actionRun = (work: () => Promise<void>) => {if (!current()) return; void run(async () => {
      if (!current()) return;
      try {await work();} catch (err) {if (current()) throw err;}
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
    // During a move to Portico Accounts the notice says so before the old sign-in is offered (P2-66).
    const moveDays = server?.move === 'to_portico' ? moveOfferDays(server.moveEndsAt) : undefined;
    const notice = server?.move === 'to_portico' ? t('accounts.move.toPortico', {server: server.name}) + ' ' + t('accounts.signIn.porticoNotice') : t('accounts.signIn.porticoNotice');
    if (tvDevice && server?.move === 'to_portico') {
      return <EntryLayout testID="accounts-entry" title={t('accounts.signIn.usesPortico', {server: server.name})} body={notice}
        kicker={formStep} beforeActions={<>{status}</>} primaryAction={action('portico', t('accounts.signIn.continuePortico'), portico)}
        additionalActions={[{id: 'move-on-phone', label: t('next.auth.tv.moveOnPhone'), disabled: busy, onPress: () => {if (!busy) setStage('tv-move-handoff');}}]}
        secondaryAction={backAction}/>;
    }
    return layout(
      t('accounts.signIn.usesPortico', {server: server!.name}),
      notice,
      undefined,
      action('portico', t('accounts.signIn.continuePortico'), portico),
      !tvDevice && <AccountEntryActions>
        {moveDays !== undefined && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberMove);}}>{t('accounts.move.oldPasswordAction', {days: moveDays})}</Button>}
        {server?.move === 'to_portico' && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberCode);}}>{t('accounts.move.haveCode')}</Button>}
      </AccountEntryActions>,
    );
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

  if (stage === 'identity-code' && tvDevice) {
    return <TVIdentityCodeForm server={identityAddress} code={identityCode} wrong={identityWrong} busy={busy}
      onChange={value => {setIdentityCode(value); setIdentityWrong(false);}} onSubmit={value => {void run(() => confirmCode(value));}}/>;
  }

  if (stage === 'identity-code') {
    return <FormLayout testID="accounts-identity-code" title={t('connection.identity.checkTitle', {server: identityAddress})} body={t('connection.identity.checkBody')}
      kicker={tvDevice ? undefined : <AccountEntryAddress>{identityAddress}</AccountEntryAddress>}
      beforeActions={<>{fields(<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('connection.identity.codeLabel')} value={identityCode} autoCapitalize="characters" autoCorrect={false} editable={!busy}
        error={identityWrong ? t('connection.identity.codeWrong') : undefined} onChangeText={value => {
          const raw = value.toUpperCase().replace(/[\s-]/g, '').slice(0, 12), shown = raw.match(/.{1,4}/g)?.join('-') ?? '';
          setIdentityCode(shown); setIdentityWrong(false); if (tvDevice && raw.length === 12) void run(() => confirmCode(shown));
        }}/>)}{status}</>}
      primaryAction={tvDevice ? undefined : action('check-code', t('action.continue'), () => confirmCode(), identityCode.replace(/-/g, '').length !== 12)}
      secondaryAction={backAction} help={t('connection.identity.checkHome')}/>;
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
        try {openInvitationEntry(source.serverOrigin());} catch (err) {setError(err);}
      }}>{t('web.directJoin.have')}</Button>}
      {!tvDevice && server?.move === 'to_local' && <Button variant="link" disabled={busy} onPress={() => {void run(beginMemberCode);}}>{t('accounts.move.haveCode')}</Button>}
    </>;
    const signIn = async () => {consume(await operation.current!.signInLocal(username, password)); setPassword('');};
    if (!theme.isTV && source) {
      return <ServerSignInPage busy={busy} error={status} passwordInvalid={passwordInvalid} serverName={server!.name}
        address={address.trim() ? address.trim().replace(/^https?:\/\//i, '') : new URL(source.serverOrigin()).host}
        username={username} password={password} onUsernameChange={setUsername} onPasswordChange={setPassword} links={links}
        onSignIn={() => {if (lifetime.current && stageRef.current === 'local' && source === operation.current) void run(signIn);}} onBack={back}/>;
    }
    return layout(
      server!.name,
      t('accounts.signIn.localBody'),
      fields(field(t('auth.username'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')),
      action('local-sign-in', t('auth.signIn'), signIn, !username || !password),
      <AccountEntryActions tight>{links}</AccountEntryActions>,
    );
  }

  if (theme.isTV && stage === 'tv-move-handoff' && server) {
    return <TVMoveHandoff serverName={server.name} url={new URL('/sign-in', operation.current!.inspectedOrigin()).href} onBack={back}/>;
  }

  return undefined;
}
