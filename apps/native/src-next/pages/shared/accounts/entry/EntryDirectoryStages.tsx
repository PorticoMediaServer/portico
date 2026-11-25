import React from 'react';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {StateLayout} from '../../../../kit/states';
import {AccountEntryServerList, AccountEntryServerRow} from '../../../../kit/accounts/AccountEntryParts';
import {DirectoryAccessRefused} from '../../../../kit/accounts/DirectoryAccessRefused';
import {nativeAccounts} from '../../../../platform/accounts';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import {directoryOfflineBody, directoryRows} from '../../../../screens/accounts/directory-chooser';
import {directoryAccessRefused, directoryClockSkew, directoryIdentityUnconfirmed} from '../../../../screens/accounts/directory-refusal';
import {NoServersPage as PhoneNoServersPage} from '../../../touch/accounts/NoServersPage';
import {OfflineServersPage} from '../../../touch/accounts/OfflineServersPage';
import {NoServersPage as TVNoServersPage} from '../../../tv/accounts/NoServersPage';
import {OfflineServersPage as TVOfflineServersPage} from '../../../tv/accounts/OfflineServersPage';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders the Portico server directory stage across list-failed, refusal, offline, waiting, empty, and chooser states. */
export function renderEntryDirectoryStage({view, props, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {downloads} = props;
  const {
    stage, setStage, busy, error, setError, setAddress, setPassword, setCode,
    server, invitationIntent, servers, target, retryIn, directoryList,
    lifetime, directoryAttempt, retryDelay, operation, targetRef, busyRef, stageRef,
    downloadsOwner, downloadValue, accountScope, acting,
    run, cancelDirectory, openAccountWorkspace, openInvitationEntry, back, directory, connectDirectory, chooseAnother,
    inspect, directoryView, hostedWaiting, offlineRows, allDirectoryOffline, tvDevice, shareDownloadPage,
  } = view;
  const {status, layout} = kit;

  if (stage !== 'servers') return undefined;

  if (directoryList === 'failed') {
    return <StateLayout testID="accounts-server-list-failed" icon="i-warn" tone="error"
      title={t('web.chooser.loadFailed')} body={t('web.chooser.loadFailedBody')}
      primaryAction={{id: 'retry-server-list', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, preferredFocus: true, onPress: () => {void run(directory);}}}/>;
  }

  if (target && directoryIdentityUnconfirmed(error)) {
    return <StateLayout testID="accounts-server-unconfirmed" icon="i-question"
      title={t('web.chooser.unconfirmedTitle', {server: target.name})} body={t('web.chooser.unconfirmedBody')}
      primaryAction={{id: 'retry-identity', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  }

  const singleTVFailure = tvDevice && servers.length === 1 && !!target && directoryAccessRefused(error);
  const singlePhoneRefusal = !tvDevice && directoryList === 'ready' && servers.length === 1 && !!target
    && servers[0].id === target.id && directoryAccessRefused(error);

  if (target && directoryClockSkew(error)) {
    return <StateLayout testID="accounts-server-clock" icon="i-clock"
      title={t('web.chooser.clockTitle', {server: target.name})} body={t('web.chooser.clockBody')}
      primaryAction={{id: 'retry-clock', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  }

  if (target && directoryAccessRefused(error) && !singleTVFailure && !singlePhoneRefusal) {
    return <DirectoryAccessRefused serverName={target.name} busy={busy}
      onOther={chooseAnother} onRefresh={() => {cancelDirectory(); void run(directory);}}/>;
  }

  if (target && directoryView === 'opening' && !allDirectoryOffline) {
    return <StateLayout testID="accounts-server-opening" title={t('web.chooser.connecting', {server: target.name})}
      primaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  }

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
    return <Offline onDownloads={!!capturedDownload && capturedDownload.source === downloads && capturedDownload.active && downloadValue?.available && downloads?.current() ? openDownloads : undefined}
      username={nativeAccounts.accountCredentials().getSnapshot().session?.account.username}
      onSettings={() => openAccountWorkspace(scope, current)}
      onJoin={() => {if (current() && !busyRef.current) openInvitationEntry();}}
      onFind={() => {if (!current() || busyRef.current) return; cancelDirectory(); setAddress(''); setPassword(''); setCode(''); stageRef.current = 'address'; setStage('address');}}
      rows={offlineRows} scope={scope} revision={attempt} isCurrent={current} busy={busy} activeId={target?.id}
      retryIn={target ? retryIn : undefined} retryProgress={target ? (retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1)) : undefined}
      onRetry={id => {if (!current()) return; const row = captured.find(value => value.id === id); if (row) connectDirectory(row);}}
      onBack={() => {if (current()) back();}} onRefresh={() => {if (current()) {cancelDirectory(); void run(directory);}}}/>;
  }

  if (target && directoryView === 'unreachable' && !singleTVFailure) {
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
      return lifetime.current && stageRef.current === 'servers' && targetRef.current === captured
        && operation.current === source && directoryAttempt.current === attempt && (tvDevice
          || accountScope.current === scope && (!source || source.ownerRecoveryCurrent())
            && captured.baseUrl === original.baseUrl && captured.id === original.serverId && captured.publicKey === original.publicKey
            && (!scope || live?.account.id === scope.accountId && live.familyId === scope.familyId));
    };
    return <StateLayout testID="accounts-hosted-waiting" icon="i-cloud-off" title={t('web.chooser.accountWaitingTitle')}
      body={t('web.chooser.accountWaitingBody', {server: captured.name})}
      retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
      primaryAction={{id: 'check-hosted-now', label: t('web.chooser.checkNow'), loading: busy, disabled: busy, preferredFocus: true, onPress: () => {if (current()) connectDirectory(captured, true);}}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: () => {if (current()) chooseAnother();}}}/>;
  }

  if (target && directoryView === 'waiting') {
    return <StateLayout testID="accounts-server-waiting" title={t('web.chooser.waitingTitle', {server: target.name})}
      body={t('web.chooser.waitingBody')}
      retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
      primaryAction={{id: 'check-now', label: t('web.chooser.checkNow'), loading: busy, onPress: () => connectDirectory(target, true)}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  }

  if (directoryList === 'ready' && servers.length === 0 || singleTVFailure || singlePhoneRefusal) {
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
      onShare={() => {if (!current() || busyRef.current) return; void run(async () => {if (!current()) return; await shareDownloadPage();});}}
      onSignOut={() => {if (current() && !busyRef.current) {stageRef.current = 'account-sign-out'; setStage('account-sign-out');}}}/>;
  }

  return layout(
    t('portico.landing.pickTitle'),
    servers.length ? t('accounts.session.portico') : t('web.hostedAccount.noServersBody'),
    <AccountEntryServerList>{directoryRows(server && server.signIn === 'portico' ? servers.filter(value => value.id === server.id) : servers).map((row, index) => {
      const value = servers.find(item => item.id === row.id)!;
      return <AccountEntryServerRow key={row.id} name={row.name} owner={row.owner ? t('accounts.settings.owner') : undefined}
        subtitle={row.subtitle ? t(row.subtitle.key, row.subtitle.values) : undefined} offline={row.offline} disabled={busy} preferredFocus={tvDevice && index === 0 && !busy}
        accessibilityLabel={`${row.name}${row.owner ? `, ${t('accounts.settings.owner')}` : ''}${row.subtitle ? `, ${t(row.subtitle.key, row.subtitle.values)}` : ''}`}
        onPress={() => connectDirectory(value)}/>;
    })}</AccountEntryServerList>,
    undefined,
    <Button variant="link" disabled={busy} onPress={() => setStage('account-sign-out')}>{t('accounts.signOut.portico')}</Button>,
  );
}
