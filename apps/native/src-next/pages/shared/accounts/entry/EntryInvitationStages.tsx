import React from 'react';
import {parseJoinLink} from '../../../../../../../packages/client-core/src/join-link';
import {passwordStrength} from '../../../../../../../packages/client-core/src/password-strength';
import {formatShortInvitationCode, parseShortInvitationCode} from '../../../../../../../packages/client-core/src/short-invitation-code';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button, FormError, TextField} from '../../../../kit/controls';
import {StateLayout} from '../../../../kit/states';
import {AccountEntryDetails, AccountEntryLine, AccountEntryScan, AccountEntryWordmark} from '../../../../kit/accounts/AccountEntryParts';
import {nativeAccounts} from '../../../../platform/accounts';
import {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import {qrScannerAvailable, scanQRCode} from '../../../../platform/qr-scanner';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders invitation entry, account-choice, sign-out switch, code, local-join, and portico-join stages. */
export function renderEntryInvitationStage({view, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {
    preparedAuthSource, theme, stage, setStage, busy, error, setError,
    address, setAddress, username, setUsername, password, setPassword, code, setCode,
    setupPasswordConfirm, setSetupPasswordConfirm, name, setName, server,
    invitationIntent, invitationPreview, setInvitationPreview, invitationOpening, invitationEntryOpening,
    invitationScan, invitationScanning, setInvitationScanning, scannedInvitationLink, setScannedInvitationLink,
    invitationSwitch, retireInvitationSwitch, retireInvitation,
    lifetime, operation, account, busyRef, stageRef, backRef,
    run, back, consume, directory, publishPreparedAccount, inspect,
  } = view;
  const {status, FormLayout} = kit;

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
      } catch (err) {if (guard()) setError(err);}
      finally {if (invitationScan.current === controller) {invitationScan.current = undefined; if (lifetime.current) setInvitationScanning(false);}}
    };
    const submit = () => {
      if (!current() || busyRef.current || !parsed) return;
      ticket!.active = false; const intent = Object.freeze({...parsed, publicPreview: true}); invitationIntent.current = intent;
      void run(async () => {
        try {await inspect(intent.server);} catch (err) {
          if (lifetime.current && invitationIntent.current === intent && stageRef.current === 'invitation-entry') invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress, external};
          throw err;
        }
      });
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
      </AccountEntryDetails>}
      primaryAction={{id: 'preview-invitation-entry', label: t('action.continue'), disabled: busy || !parsed, loading: busy, onPress: submit}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }

  if (stage === 'invitation-account') {
    const ticket = invitationSwitch.current, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && lifetime.current && stageRef.current === 'invitation-account';
    const begin = (register: boolean) => {
      if (!current() || busyRef.current) return;
      void run(async () => {
        if (!current()) return;
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
        if (!current()) return;
        if (credentials.getSnapshot().session) {await directory(); return;}
        preparedAuthSource.current = undefined; account.current?.dispose();
        const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
          && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
        const available = await source.prepare(); if (!current() || account.current !== source) return;
        if (!publishPreparedAccount(source, available, current)) return;
        stageRef.current = register && available.registration ? 'register' : 'portico'; setStage(stageRef.current);
      });
    };
    return <FormLayout testID="accounts-invitation-account" title={t('accounts.invitation.to') + ' ' + (preview?.serverName ?? '')}
      body={preview ? t('accounts.invitation.porticoBody', {server: preview.serverName}) : undefined}
      beforeActions={<AccountEntryDetails>{status}
        {preview && <AccountEntryLine>{preview.role === 'admin' ? t('accounts.invitation.admin') : t('accounts.invitation.member')}</AccountEntryLine>}
        {preview?.allLibraries && <AccountEntryLine>{t('server.people.allLibraries')}</AccountEntryLine>}
        {preview?.libraries.map((library, index) => <AccountEntryLine key={index}>{library.name}</AccountEntryLine>)}
      </AccountEntryDetails>}
      primaryAction={{id: 'sign-in-to-join', label: t('auth.signInWithAccount'), disabled: busy || !preview, loading: busy, onPress: () => begin(false)}}
      secondaryAction={{id: 'create-account-to-join', label: t('auth.createAccount'), disabled: busy || !preview, onPress: () => begin(true)}}
      detailsAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }

  if (stage === 'invitation-sign-out') {
    const ticket = invitationSwitch.current;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && !!ticket.scope && lifetime.current && stageRef.current === 'invitation-sign-out';
    const cancel = () => {
      if (!current() || busyRef.current) return;
      retireInvitationSwitch(); stageRef.current = 'address'; setStage('address');
      void run(async () => {await inspect(ticket!.entry.server);});
    };
    return <FormLayout testID="accounts-invitation-sign-out" title={t('accounts.signOut.portico')} body={ticket?.username}
      beforeActions={status}
      primaryAction={{id: 'switch-invitation-account', label: t('action.signOut'), disabled: busy, loading: busy, onPress: () => {
        if (!current()) return;
        void run(async () => {
          if (!current()) return; await nativeAccounts.signOutPorticoAccount(ticket!.scope!);
          if (!current()) return;
          setUsername(''); setPassword(''); setCode('');
          preparedAuthSource.current = undefined; account.current?.dispose();
          const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
            && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
          const available = await source.prepare();
          if (!current() || account.current !== source) return;
          if (!publishPreparedAccount(source, available, current)) return;
          stageRef.current = 'portico'; setStage('portico');
        });
      }}}
      secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: busy, onPress: cancel}}/>;
  }

  if (stage === 'invitation-code' || stage === 'invitation-local' || stage === 'invitation-portico') {
    const ticket = invitationOpening.current, renderedStage = stage, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationOpening.current === ticket && operation.current === ticket.source
      && lifetime.current && stageRef.current === renderedStage;
    const change = (set: (value: string) => void) => (value: string) => {if (current()) set(value);};
    const goBack = () => {if (current() && !busyRef.current) backRef.current();};
    const invitationFailure = ticket?.controller.failure(error);
    const invitationStatus = invitationFailure === 'unavailable' || invitationFailure === 'mistyped'
      ? <FormError placement="block">{t(invitationFailure === 'mistyped' ? 'web.directJoin.codeNotRight' : 'web.directJoin.entryUnavailable')}</FormError> : status;
    const differentAccount = () => {
      if (!current() || busyRef.current || ticket?.kind !== 'portico') return;
      try {
        const captured = ticket.controller.captureAccountSwitch(), handoff = {...captured, active: true};
        retireInvitation(); ticket.source.dispose(); operation.current = undefined;
        invitationIntent.current = captured.entry; invitationSwitch.current = handoff; setError(undefined); setPassword(''); setCode('');
        stageRef.current = 'invitation-sign-out'; setStage('invitation-sign-out');
      } catch (err) {if (current()) setError(err);}
    };

    if (invitationFailure === 'expired') {
      return <StateLayout testID="accounts-invitation-expired" icon="i-warn" title={t('accounts.invitation.expiredTitle')} body={t('accounts.invitation.expiredBody')}
        primaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: goBack}}/>;
    }
    if (stage === 'invitation-code') {
      return <FormLayout testID="accounts-invitation-code" title={t('web.directJoin.title')}
        body={t('services.start.joinBody')}
        beforeActions={<>{invitationStatus}<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('services.start.invitation')} value={code}
          onChangeText={change(setCode)} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/></>}
        primaryAction={{id: 'preview-invitation', label: t('action.continue'), disabled: busy || !code.trim(), loading: busy, onPress: () => {
          if (!current()) return;
          void run(async () => {
            if (!current()) return;
            const value = await ticket!.controller.preview(code);
            if (!current()) return;
            invitationIntent.current = undefined; setInvitationPreview(value); setCode('');
            const destination = ticket!.kind === 'portico' ? 'invitation-portico' : 'invitation-local';
            stageRef.current = destination; setStage(destination);
          });
        }}}
        secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: goBack}}/>;
    }
    if (invitationFailure === 'other-account') {
      return <StateLayout testID="accounts-invitation-other-account" icon="i-person"
        title={t('accounts.invitation.otherAccountTitle')} body={t('web.signIn.error.invitationOtherAccount', {server: preview?.serverName ?? server!.name})}
        primaryAction={{id: 'different-account', label: t('accounts.invitation.notYou'), disabled: busy, onPress: differentAccount}}
        secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: goBack}}/>;
    }
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
          });}}}
        secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: goBack}}/>;
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
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.username')} value={username} error={invitationFailure === 'username' ? t('web.directJoin.error.taken') : undefined} onChangeText={change(setUsername)} autoCapitalize="none" autoCorrect={false} editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('auth.password')} value={password} onChangeText={change(setPassword)} secureTextEntry autoComplete="new-password" editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.confirm')} value={setupPasswordConfirm} onChangeText={change(setSetupPasswordConfirm)} secureTextEntry autoComplete="new-password" editable={!busy}/>
      </AccountEntryDetails>}
      primaryAction={{id: 'join-local-invitation', label: t('web.directJoin.submit'), loading: busy,
        disabled: busy || !/^[A-Za-z0-9_.-]{3,64}$/.test(username.trim()) || !passwordStrength(password).acceptable || password !== setupPasswordConfirm,
        onPress: () => {if (!current()) return; void run(async () => {
          if (!current()) return; const result = await localController.join(preview, username, password, name);
          if (!current()) return; setPassword(''); setSetupPasswordConfirm(''); consume(result);
        });}}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: goBack}}/>;
  }

  return undefined;
}
