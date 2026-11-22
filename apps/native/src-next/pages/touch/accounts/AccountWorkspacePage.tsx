import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {accountSessionDevice} from '../../../../../../packages/client-core/src/account-sessions';
import {Button, FormError} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {Quiet} from '../../../kit/states';
import {WorkspaceCaption, WorkspaceCard, WorkspaceDangerButton, WorkspaceFooter, WorkspaceFrame, WorkspaceNotice, WorkspacePager, WorkspaceText} from '../../../kit/accounts/WorkspaceParts';
import type {NativeAccountWorkspace} from '../../../platform/account-workspace';
import {AccountSecurityPage} from './AccountSecurityPage';
import {useAccountWorkspace} from '../../shared/accounts/useAccountWorkspace';

const shownNotifications = ['server_offline', 'new_device', 'account_export_ready', 'security_event', 'signing_key_expiring'];

/** Account-only settings remain available when no server is connected: Security, Devices, Servers and Notifications. */
export function AccountWorkspacePage({createSource, username, onDone, onFind, onJoin, onSignOut}: {
  createSource: () => NativeAccountWorkspace; username: string; onDone: () => void; onFind: () => void; onJoin: () => void; onSignOut: () => void;
}) {
  const t = defaultI18n.t, workspace = useAccountWorkspace({createSource, onDone, onFind, onJoin, onSignOut}), {state, source, confirm, ending} = workspace;
  const sheet = <BottomSheet testID="account-workspace-confirm" open={!!confirm} onOpenChange={workspace.dismissConfirm}
    title={confirm?.kind === 'server' ? t('web.hostedAccount.leaveTitle', {server: confirm.row.name}) : confirm?.kind === 'others' ? t('web.hostedAccount.signOutOthersTitle')
      : t('web.hostedAccount.signOutDeviceTitle', {device: confirm?.kind === 'session' ? accountSessionDevice(confirm.row).name : ''})}
    footer={<WorkspaceFooter><Button variant="glass" disabled={ending} onPress={workspace.cancelConfirm}>{t('action.cancel')}</Button>
      <WorkspaceDangerButton testID="account-workspace-confirm-action" variant="glass" loading={ending} onPress={workspace.confirmAction}>
        {confirm?.kind === 'server' ? t('web.hostedAccount.leaveServer') : confirm?.kind === 'others' ? t('web.hostedAccount.signOutOthers') : t('action.signOut')}</WorkspaceDangerButton></WorkspaceFooter>}>
    <WorkspaceText>{confirm?.kind === 'server' ? t('web.hostedAccount.leaveBody', {server: confirm.row.name}) : confirm?.kind === 'others' ? t('services.workspace.signOutOthersAppBody') : t('web.hostedAccount.signOutBody')}</WorkspaceText>
    {workspace.failure && <FormError placement="inline">{workspace.failure}</FormError>}
  </BottomSheet>;
  return <WorkspaceFrame scrollRef={workspace.scroll} title={t(workspace.heading)} overlay={sheet}
      action={<Button variant="text" disabled={!!state.pending} onPress={workspace.done}>{t(workspace.doneLabel)}</Button>}>
      {workspace.failedOpening ? <WorkspaceText>{t('error.unknown')}</WorkspaceText> : state.phase === 'ended' ? <WorkspaceText>{t('web.hostedAccount.signInAgain')}</WorkspaceText> : <>
        {state.page === 'menu' && source && <>
          <WorkspaceText>{username}</WorkspaceText>
          <WorkspaceCaption>{t('accounts.session.portico')}</WorkspaceCaption>
          <Button variant="glass" onPress={workspace.open('security')}>{t('account.security')}</Button>
          <Button variant="glass" onPress={workspace.open('devices')}>{t('devices.title')}</Button>
          <Button variant="glass" onPress={workspace.open('servers')}>{t('web.servers.title')}</Button>
          <Button variant="glass" onPress={workspace.open('notifications')}>{t('web.account.notifications')}</Button>
          <Button variant="glass" onPress={workspace.join}>{t('services.start.join')}</Button>
          <Button variant="glass" onPress={workspace.find}>{t('connection.discovery.find')}</Button>
          <Button variant="text" onPress={workspace.signOut}>{t('action.signOut')}</Button>
        </>}
        {state.phase === 'loading' && <Quiet><WorkspaceText>{t('status.loading')}</WorkspaceText></Quiet>}
        {state.phase === 'failed' && <>
          <FormError placement="inline">{state.failure}</FormError>
          <Button onPress={workspace.retry}>{t('action.tryAgain')}</Button>
        </>}
        {state.security && source && <AccountSecurityPage workspace={source} security={state.security}/>}
        {workspace.failure && <FormError placement="inline">{workspace.failure}</FormError>}
        {state.notice && <WorkspaceNotice>{state.notice}</WorkspaceNotice>}
        {state.sessions?.map(row => {
          // Name, app and last use are what the device's app reported; an unknown fact is left out, never guessed.
          const device = accountSessionDevice(row);
          return <WorkspaceCard key={row.id} testID={`account-device-${row.id}`}>
            <WorkspaceText>{device.name}</WorkspaceText>
            {row.current && <WorkspaceCaption>{t('device.thisDevice')}</WorkspaceCaption>}
            {device.app && <WorkspaceCaption>{device.app}</WorkspaceCaption>}
            <WorkspaceCaption>{t('web.hostedAccount.signedInOn', {date: defaultI18n.date(row.createdAt)})}</WorkspaceCaption>
            {row.lastUsedAt && <WorkspaceCaption>{t('web.hostedAccount.lastUsed', {when: defaultI18n.relativeTime(row.lastUsedAt, undefined, {inSentence: true})})}</WorkspaceCaption>}
            {!row.current && <Button testID={`account-device-sign-out-${row.id}`} variant="glass" disabled={ending} onPress={() => workspace.ask({kind: 'session', row})}>{t('action.signOut')}</Button>}
          </WorkspaceCard>;
        })}
        {state.page === 'devices' && state.phase === 'ready' && <Button testID="account-devices-sign-out-others" variant="glass" loading={!!state.endingOthers}
          disabled={ending || !state.sessions?.some(row => !row.current)} onPress={() => workspace.ask({kind: 'others'})}>{t('web.hostedAccount.signOutOthers')}</Button>}
        {state.servers?.map(row => <WorkspaceCard key={row.id} dim={row.presence?.online === false}>
          <WorkspaceText>{row.name}</WorkspaceText>
          {row.ownedByMe === true && <WorkspaceText>{t('web.hostedAccount.serverOwner')}</WorkspaceText>}
          {/* O-163: a server still getting its secure connection says so in one plain line, never an address. */}
          {(row as {secureConnection?: unknown}).secureConnection === 'pending'
            ? <WorkspaceText>{t(row.presence?.online === false ? 'web.hostedAccount.secureConnectionOffline' : 'web.hostedAccount.secureConnectionPending')}</WorkspaceText>
            : row.presence && <WorkspaceText>{row.presence.online ? t('web.hostedAccount.serverOnline') : row.presence.lastSeenAt ? t('web.hostedAccount.serverLastSeen', {when: defaultI18n.date(row.presence.lastSeenAt)}) : t('web.servers.offline')}</WorkspaceText>}
          {row.ownedByMe === true ? <WorkspaceText>{t('web.hostedAccount.leaveOwner')}</WorkspaceText> : row.ownedByMe === false && ['admin', 'member'].includes(row.role ?? '')
            ? <Button testID={`account-server-leave-${row.id}`} variant="glass" disabled={!!state.pending} onPress={() => workspace.ask({kind: 'server', row})}>{t('web.hostedAccount.leaveServer')}</Button> : null}
        </WorkspaceCard>)}
        {state.servers?.length === 0 && <><WorkspaceText>{t('web.hostedAccount.noServers')}</WorkspaceText><Button variant="glass" onPress={workspace.join}>{t('services.start.join')}</Button><Button variant="glass" onPress={workspace.find}>{t('connection.discovery.find')}</Button></>}
        {state.page === 'notifications' && state.phase === 'ready' && <WorkspacePager>
          <Button variant="glass" disabled={!state.notificationsPrevious} onPress={() => workspace.pageNotifications('previous')}>{t('web.hostedAccount.newer')}</Button>
          <Button variant="glass" disabled={!state.notificationsNext} onPress={() => workspace.pageNotifications('next')}>{t('web.account.notif.older')}</Button>
        </WorkspacePager>}
        {state.notifications?.length === 0 && <WorkspaceText>{t('web.account.notif.empty')}</WorkspaceText>}
        {state.notifications?.filter(row => shownNotifications.includes(row.kind) && (row.kind !== 'server_offline' || !!row.server?.name)
          && (row.kind !== 'new_device' || !!row.detail.name) && (row.kind !== 'account_export_ready' || !!row.detail.exportId)).map(row => <WorkspaceCard key={row.id}>
          <WorkspaceText>{row.kind === 'server_offline' && row.server ? t('web.account.notif.serverOffline', {server: row.server.name})
            : row.kind === 'new_device' ? t('web.account.notif.newDevice', {name: row.detail.name ?? ''})
            : row.kind === 'account_export_ready' ? t('web.account.notif.exportReady')
            : row.kind === 'signing_key_expiring' ? t('web.account.notif.keyExpiring') : t('web.account.notif.security')}</WorkspaceText>
          <WorkspaceCaption>{defaultI18n.date(row.occurredAt)}</WorkspaceCaption>
        </WorkspaceCard>)}
      </>}
    </WorkspaceFrame>;
}
