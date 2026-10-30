import React, {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import {ColourBackdrop} from '../../kit/artwork';
import type {NativeAccountWorkspace, AccountWorkspacePage, AccountWorkspaceSnapshot} from '../../platform/account-workspace';
import {accountSessionDevice, type AccountSessionRow} from '../../../../../packages/client-core/src/account-sessions';
import type {HostedServer} from '../../../../../packages/client-core/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {PhoneAccountSecurity} from './PhoneAccountSecurity';
import {Quiet} from '../../kit/states';

type CommittedOpening = {source?: NativeAccountWorkspace; active: boolean; failed: boolean};
const waiting: AccountWorkspaceSnapshot = Object.freeze({page: 'menu', phase: 'loading'});
const noSubscribe = () => () => {};
const waitingSnapshot = () => waiting;

/** Account-only settings remain available when no server is connected. */
export function PhoneAccountWorkspace({createSource, username, onDone, onFind, onJoin, onSignOut}: {
  createSource: () => NativeAccountWorkspace; username: string; onDone: () => void; onFind: () => void; onJoin: () => void; onSignOut: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const opening = useRef<CommittedOpening | undefined>(undefined);
  const scroll = useRef<ScrollView>(null);
  const [ticket, setTicket] = useState<CommittedOpening>();
  const [confirm, setConfirm] = useState<{kind: 'session'; row: AccountSessionRow} | {kind: 'others'} | {kind: 'server'; row: HostedServer}>();
  const [commandFailure, setCommandFailure] = useState<string>();
  useLayoutEffect(() => {
    if (opening.current) {opening.current.active = false; opening.current.source?.dispose();}
    const committed: CommittedOpening = {active: true, failed: false}; opening.current = committed;
    try {committed.source = createSource();} catch {committed.failed = true;}
    setTicket(committed);
    setConfirm(undefined); setCommandFailure(undefined);
    return () => {committed.active = false; if (opening.current === committed) opening.current = undefined; committed.source?.dispose();};
  }, [createSource]);
  const source = ticket?.source;
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? waitingSnapshot);
  // H14: a new account page opens at its top, not at the menu's scroll offset.
  useLayoutEffect(() => {scroll.current?.scrollTo({y: 0, animated: false});}, [state.page]);
  const live = () => !!ticket?.active && opening.current === ticket && (!source || source.getSnapshot() === state);
  const current = () => live() && !!source && source.current();
  const go = (action: () => void) => () => {if (!current() || !ticket) return; ticket.active = false; source?.dispose(); action();};
  const page = (value: Exclude<AccountWorkspacePage, 'menu'>) => () => {if (current()) {setConfirm(undefined); setCommandFailure(undefined); void source?.open(value);}};
  const heading = state.page === 'security' ? t('account.security') : state.page === 'devices' ? t('devices.title')
    : state.page === 'servers' ? t('web.servers.title') : state.page === 'notifications' ? t('web.account.notifications') : t('settings.title');
  const label = (text: string, key?: string) => <Text key={key} style={[theme.type.body, {color: theme.colors.text}]}>{text}</Text>;
  const caption = (text: string, key?: string) => <Text key={key} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{text}</Text>;
  const ending = !!state.pending || !!state.endingOthers, unresolved = !!state.retrySession || !!state.retryOthers;
  const command = (work: () => Promise<void>) => {
    if (!current() || ending) return;
    setCommandFailure(undefined);
    void work().then(() => {if (ticket?.active && source?.current()) setConfirm(undefined);}, error => {
      if (ticket?.active && source?.current()) setCommandFailure(presentError(error, 'portico-account').body);
    });
  };
  return <View testID="phone-serverless-settings" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <ScrollView ref={scroll} contentContainerStyle={{paddingTop: insets.top + 20, paddingBottom: Math.max(24, insets.bottom + 16), paddingHorizontal: 24, gap: 18}}>
      <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>
        <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{heading}</Text>
        <Button variant="text" disabled={!!state.pending} onPress={() => {if (!current()) {if (ticket && live() && (ticket.failed || state.phase === 'ended')) {ticket.active = false; source?.dispose(); onDone();} return;}
          setConfirm(undefined); setCommandFailure(undefined); if (state.page === 'menu') go(onDone)(); else source?.menu();}}>{t(state.page === 'menu' || state.phase === 'ended' ? 'action.done' : 'action.back')}</Button>
      </View>
      {ticket?.failed ? label(t('error.unknown')) : state.phase === 'ended' ? label(t('web.hostedAccount.signInAgain')) : <>
        {state.page === 'menu' && source && <>
          {label(username)}
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.session.portico')}</Text>
          <Button variant="glass" onPress={page('security')}>{t('account.security')}</Button>
          <Button variant="glass" onPress={page('devices')}>{t('devices.title')}</Button>
          <Button variant="glass" onPress={page('servers')}>{t('web.servers.title')}</Button>
          <Button variant="glass" onPress={page('notifications')}>{t('web.account.notifications')}</Button>
          <Button variant="glass" onPress={go(onJoin)}>{t('services.start.join')}</Button>
          <Button variant="glass" onPress={go(onFind)}>{t('connection.discovery.find')}</Button>
          <Button variant="text" onPress={go(onSignOut)}>{t('action.signOut')}</Button>
        </>}
        {state.phase === 'loading' && <Quiet>{label(t('status.loading'))}</Quiet>}
        {state.phase === 'failed' && <>
          <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{state.failure}</Text>
          <Button onPress={() => {if (current() && state.page !== 'menu') void source?.open(state.page);}}>{t('action.tryAgain')}</Button>
        </>}
        {state.security && source && <PhoneAccountSecurity workspace={source} security={state.security}/>}
        {(commandFailure || state.mutationFailure) && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{commandFailure || state.mutationFailure}</Text>}
        {state.notice && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.text}]}>{state.notice}</Text>}
        {state.sessions?.map(row => {
          // Name, app and last use are what the device's app reported; an unknown fact is left out, never guessed.
          const device = accountSessionDevice(row);
          return <View key={row.id} testID={`account-device-${row.id}`} style={{padding: 16, gap: 8, borderRadius: 12, backgroundColor: theme.colors.raised}}>
            {label(device.name)}
            {row.current && caption(t('device.thisDevice'))}
            {device.app && caption(device.app)}
            {caption(t('web.hostedAccount.signedInOn', {date: defaultI18n.date(row.createdAt)}))}
            {row.lastUsedAt && caption(t('web.hostedAccount.lastUsed', {when: defaultI18n.relativeTime(row.lastUsedAt)}))}
            {row.status !== 'active' && label(t('web.hostedAccount.ended'))}
            {row.status === 'active' && !row.current && <Button testID={`account-device-sign-out-${row.id}`} variant="glass" disabled={ending || unresolved} onPress={() => {if (current()) setConfirm({kind: 'session', row});}}>{t('action.signOut')}</Button>}
          </View>;
        })}
        {state.retrySession && <Button disabled={ending} onPress={() => command(() => source!.revokeSession(state.retrySession!))}>{t('action.tryAgain')}</Button>}
        {state.retryOthers && <Button disabled={ending} onPress={() => command(() => source!.revokeOtherSessions())}>{t('action.tryAgain')}</Button>}
        {state.page === 'devices' && state.phase === 'ready' && <>
          <Button testID="account-devices-sign-out-others" variant="glass" loading={!!state.endingOthers} disabled={ending || unresolved || !state.sessions?.some(row => row.status === 'active' && !row.current) && !state.sessionsNext}
            onPress={() => {if (current()) setConfirm({kind: 'others'});}}>{t('web.hostedAccount.signOutOthers')}</Button>
          <View style={{flexDirection: 'row', gap: 12}}>
            <Button variant="glass" disabled={ending || unresolved || !state.sessionsPrevious} onPress={() => {if (current()) void source?.pageSessions('previous');}}>{t('web.hostedAccount.newer')}</Button>
            <Button variant="glass" disabled={ending || unresolved || !state.sessionsNext} onPress={() => {if (current()) void source?.pageSessions('next');}}>{t('web.hostedAccount.older')}</Button>
          </View>
        </>}
        {state.servers?.map(row => <View key={row.id} style={{padding: 16, gap: 8, borderRadius: 12, backgroundColor: theme.colors.raised, opacity: row.presence?.online === false ? .62 : 1}}>
          {label(row.name)}
          {row.ownedByMe === true && label(t('web.hostedAccount.serverOwner'))}
          {/* O-163: a server still getting its secure connection says so in one plain line, never an address. */}
          {(row as {secureConnection?: unknown}).secureConnection === 'pending'
            ? label(t(row.presence?.online === false ? 'web.hostedAccount.secureConnectionOffline' : 'web.hostedAccount.secureConnectionPending'))
            : row.presence && label(row.presence.online ? t('web.hostedAccount.serverOnline') : row.presence.lastSeenAt ? t('web.hostedAccount.serverLastSeen', {when: defaultI18n.date(row.presence.lastSeenAt)}) : t('web.servers.offline'))}
          {row.ownedByMe === true ? label(t('web.hostedAccount.leaveOwner')) : row.ownedByMe === false && ['admin', 'member'].includes(row.role ?? '')
            ? <Button testID={`account-server-leave-${row.id}`} variant="glass" disabled={!!state.pending} onPress={() => {if (current()) setConfirm({kind: 'server', row});}}>{t('web.hostedAccount.leaveServer')}</Button> : null}
        </View>)}
        {state.servers?.length === 0 && <>{label(t('web.hostedAccount.noServers'))}<Button variant="glass" onPress={go(onJoin)}>{t('services.start.join')}</Button><Button variant="glass" onPress={go(onFind)}>{t('connection.discovery.find')}</Button></>}
        {state.page === 'notifications' && state.phase === 'ready' && <View style={{flexDirection: 'row', gap: 12}}>
          <Button variant="glass" disabled={!state.notificationsPrevious} onPress={() => {if (current()) void source?.pageNotifications('previous');}}>{t('web.hostedAccount.newer')}</Button>
          <Button variant="glass" disabled={!state.notificationsNext} onPress={() => {if (current()) void source?.pageNotifications('next');}}>{t('web.account.notif.older')}</Button>
        </View>}
        {state.notifications?.length === 0 && label(t('web.account.notif.empty'))}
        {state.notifications?.filter(row => ['server_offline', 'new_device', 'account_export_ready', 'security_event', 'signing_key_expiring'].includes(row.kind) && (row.kind !== 'server_offline' || !!row.server?.name)
          && (row.kind !== 'new_device' || !!row.detail.name) && (row.kind !== 'account_export_ready' || !!row.detail.exportId)).map(row => <View key={row.id} style={{padding: 16, gap: 8, borderRadius: 12, backgroundColor: theme.colors.raised}}>
          {label(row.kind === 'server_offline' && row.server ? t('web.account.notif.serverOffline', {server: row.server.name})
            : row.kind === 'new_device' ? t('web.account.notif.newDevice', {name: row.detail.name ?? ''})
            : row.kind === 'account_export_ready' ? t('web.account.notif.exportReady')
            : row.kind === 'signing_key_expiring' ? t('web.account.notif.keyExpiring') : t('web.account.notif.security'))}
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{defaultI18n.date(row.occurredAt)}</Text>
        </View>)}
      </>}
    </ScrollView>
    <BottomSheet testID="account-workspace-confirm" open={!!confirm} onOpenChange={open => {if (!open && !ending) setConfirm(undefined);}}
      title={confirm?.kind === 'server' ? t('web.hostedAccount.leaveTitle', {server: confirm.row.name}) : confirm?.kind === 'others' ? t('web.hostedAccount.signOutOthersTitle')
        : t('web.hostedAccount.signOutDeviceTitle', {device: confirm?.kind === 'session' ? accountSessionDevice(confirm.row).name : ''})}
      footer={<View style={{gap: 10}}><Button variant="glass" disabled={ending} onPress={() => setConfirm(undefined)}>{t('action.cancel')}</Button>
        <Button testID="account-workspace-confirm-action" variant="glass" loading={ending} style={{backgroundColor: `${theme.colors.danger}24`}} textStyle={{color: theme.colors.danger}} onPress={() => {const captured = confirm; if (!captured) return;
          command(() => captured.kind === 'server' ? source!.leaveServer(captured.row) : captured.kind === 'others' ? source!.revokeOtherSessions() : source!.revokeSession(captured.row));}}>
          {confirm?.kind === 'server' ? t('web.hostedAccount.leaveServer') : confirm?.kind === 'others' ? t('web.hostedAccount.signOutOthers') : t('action.signOut')}</Button></View>}>
      {label(confirm?.kind === 'server' ? t('web.hostedAccount.leaveBody', {server: confirm.row.name}) : confirm?.kind === 'others' ? t('services.workspace.signOutOthersAppBody') : t('web.hostedAccount.signOutBody'))}
      {(commandFailure || state.mutationFailure) && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{commandFailure || state.mutationFailure}</Text>}
    </BottomSheet>
  </View>;
}
