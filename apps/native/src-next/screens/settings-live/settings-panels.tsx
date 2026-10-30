import {ProfileRoute} from '../accounts/AccountRoutes';
import {NativeAccessTokensPanel, NativeAccountSecurityPanel} from './NativeAccountSecurityPanels';
import {DeviceAboutPanel} from './DeviceAboutPanel';
import {NativeConnectionAddressRow, NativeConnectionStatusRow, NativeInsecureConnectionsRow, NativeTestConnectionRow} from './NativeConnectionRows';
import {IntroCreditsReview} from './owner-libraries/IntroCreditsReview';
import React, {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {NativeModules, Text, View} from 'react-native';
import {isStaleAnswer} from '../../../../../packages/client-core/src/freshness-actions';
import {idempotencyKey} from '../../../../../packages/client-core/src/playback-v1/http';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import {SessionsClient} from '../../../../../packages/client-core/src/playback-v1/sessions';
import {playbackControlsService, type RemotePlaybackControls} from '../../../../../packages/client-core/src/playback-controls';
import {seekTarget, transportOffers, type TransportCommand} from '../../../../../packages/client-core/src/server-admin/now-playing';
import type {SettingsCustomId} from '../../../../../packages/client-core/src/presentation/settings-structure';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {defaultI18n, type MessageId, type MessageValues} from '../../../../../packages/i18n/src';
import type {NativeScreenLifetime} from './lifetime';
import type {NativeSettingsDownloads} from './ports';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {Button} from '../../kit/controls';

/** The full catalogue translator; the page passes it down with every other word. */
export type Translate = (id: MessageId, values?: MessageValues) => string;

export type NativeSettingsPanelDeps = Readonly<{
  context: NativeShellBootstrapContext;
  lifetime: NativeScreenLifetime;
  t: Translate;
  openRoute: (route: NativeShellRoute) => void;
  downloads?: NativeSettingsDownloads;
}>;

const roleWord = (role: 'owner' | 'admin' | 'member'): MessageId => role === 'owner' ? 'next.shell.owner' : role === 'admin' ? 'next.shell.administrator' : 'next.shell.member';

function IdentityPanel({context, t}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  const account = context.account.account;
  const profile = context.account.profiles.find(value => value.id === context.session()?.viewer.profileId);
  const description = theme.isTV ? profile?.name ? t('account.usingProfile', {name: profile.name}) : undefined
    : [profile?.name, t(roleWord(account.role))].filter(Boolean).join(' · ');
  return <SettingsRow first testID="settings-panel-identity" label={t(theme.isTV ? 'account.signedInAs' : 'settings.row.identity')} value={account.username} description={description}/>;
}

function PorticoAccountPanel({context, t}: NativeSettingsPanelDeps) {
  const hosted = !!context.account.account.hostedAccountId;
  return <SettingsRow first testID="settings-panel-portico-account" label={t('settings.row.porticoAccount')}
    value={t(hosted ? 'accounts.session.portico' : 'accounts.session.local')}
    description={t(hosted ? 'accounts.settings.porticoPrivacy' : 'accounts.settings.localPrivacy')}/>;
}

function ProfilesPanel({context, t, openRoute}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  return theme.isTV ? <SettingsRow first testID="settings-panel-profiles" label={t('settings.section.profiles')} onPress={() => openRoute({kind: 'profiles'})}/>
    : <ProfileRoute context={context} route={context.navigation.getSnapshot().route} onBack={() => {}} embedded/>;
}

function ServersPanel({context, t, openRoute}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  return <SettingsRow first testID="settings-panel-servers" label={t(theme.isTV ? 'settings.switchServer' : 'settings.section.servers')} value={context.serverName} onPress={() => openRoute({kind: 'servers'})}/>;
}

function PrivacyNotePanel({t}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  return <View style={theme.isTV ? {paddingLeft: 24, marginBottom: 26} : {paddingHorizontal: 14, paddingVertical: 12}}>
    <Text testID="settings-panel-privacy-note" style={[shellType(theme.isTV ? 22 : 13, 400, theme.isTV ? 31.9 : 'normal'), {color: theme.colors.textTertiary}]}>{t('pref.note.privacy.ownerSeesPlays')}</Text>
  </View>;
}


/** The one viewer-scoped downloads store, read for its real used and free bytes;
 * the row opens the real Downloads route. No writable controls live here. */
type DownloadsStorageLease = Readonly<{
  route: NativeShellRoute; revision: number; downloads: NativeSettingsDownloads;
  lifetime: NativeScreenLifetime; live: {value: boolean};
}>;

function DownloadsStoragePanel({downloads, t, openRoute, context, lifetime}: NativeSettingsPanelDeps & {downloads: NativeSettingsDownloads}) {
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot, context.navigation.getSnapshot);
  const snapshot = useSyncExternalStore(downloads.subscribe, downloads.getSnapshot, downloads.getSnapshot);
  const [lease, setLease] = useState<DownloadsStorageLease>();
  const settingsRoute = navigation.route.kind === 'shell' && navigation.route.destination.kind === 'settings';
  // The committed layout publishes an immutable lease for the exact Settings
  // route/revision, this summary and this lifetime. A route change re-publishes
  // and retires the old lease, so a retained row can never press from a stale one.
  useLayoutEffect(() => {
    if (!settingsRoute) {setLease(undefined); return;}
    const live = {value: true};
    const next = Object.freeze({route: navigation.route, revision: navigation.revision, downloads, lifetime, live});
    setLease(next);
    return () => {
      live.value = false;
      setLease(current => current === next ? undefined : current);
    };
  }, [downloads, lifetime, navigation.revision, navigation.route, settingsRoute]);
  if (!settingsRoute || !lease || lease.downloads !== downloads || !snapshot?.supported || !snapshot.ready) return null;
  const held = lease;
  const openDownloads = () => {
    if (!held.live.value || held.downloads !== downloads || !held.lifetime.current()) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== held.revision || now.route !== held.route) return;
    if (now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    openRoute({kind: 'downloads'});
  };
  return <SettingsRow first testID="settings-panel-downloads-storage" label={t('settings.row.downloadsStorage')}
    value={defaultI18n.bytes(snapshot.usedBytes)} description={t('settings.downloadsStorage.free', {free: defaultI18n.bytes(snapshot.freeBytes)})}
    onPress={openDownloads}/>;
}

const transportIcon: Record<TransportCommand, 'i-pause' | 'i-play' | 'i-rew' | 'i-ffw' | 'i-restart'> = {pause: 'i-pause', resume: 'i-play', back: 'i-rew', forward: 'i-ffw', startOver: 'i-restart'};

/** What this profile is playing elsewhere, read from the server. Nothing is drawn while nothing is. */
function RemoteControlsPanel({context, lifetime, t}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  const [items, setItems] = useState<readonly RemotePlaybackControls[]>([]);
  const [menu, setMenu] = useState<RemotePlaybackControls>();
  const controls = useMemo(() => playbackControlsService(<T,>(path: string, method?: string, body?: unknown) => lifetime.request(signal => context.api.request<T>(path, method, body, signal))), [context, lifetime]);
  const sessions = useMemo(() => new SessionsClient(localV1Http(context.api)), [context]);
  useEffect(() => {
    let live = true;
    const read = () => {void controls.remote().then(value => {if (live) setItems(value.items);}, () => {});};
    read();
    const timer = setInterval(read, 10_000);
    return () => {live = false; clearInterval(timer);};
  }, [controls]);
  if (!items.length) return null;
  const words: Record<TransportCommand, string> = {pause: t('action.pause'), resume: t('entry.resume'), back: t('player.skipBackSeconds', {seconds: 30}), forward: t('player.skipForwardSeconds', {seconds: 30}), startOver: t('action.startOver')};
  const send = (target: RemotePlaybackControls, what: TransportCommand | 'stop') => {
    lifetime.assert();
    const work = what === 'stop' ? sessions.stop(target.sessionId) : (async () => {
      const change = (from: Readonly<{revision: string; positionMs: number}>) => sessions.patch(target.sessionId, from.revision, what === 'pause' || what === 'resume' ? {state: what === 'pause' ? 'paused' : 'playing'} : {seek: {positionMs: seekTarget(what, from.positionMs) ?? 0, id: idempotencyKey(() => nativeRequestId())}});
      try {await change(target);} catch (error) {
        const current = (error as {current?: Readonly<{revision?: unknown; positionMs?: unknown}>} | null)?.current;
        if (!isStaleAnswer(error) || typeof current?.revision !== 'string') throw error;
        await change({revision: current.revision, positionMs: typeof current.positionMs === 'number' ? current.positionMs : target.positionMs});
      }
    })();
    void work.then(() => controls.remote().then(value => setItems(value.items), () => {}), () => {});
  };
  const entries = (target: RemotePlaybackControls): readonly MenuEntry[] => [
    ...transportOffers(target, target.actions).map((command): MenuEntry => ({id: command, label: words[command], icon: transportIcon[command], onSelect: () => send(target, command)})),
    ...(target.actions.includes('stop') ? [{id: 'stop', label: t('action.stop'), icon: 'i-stop' as const, destructive: true, onSelect: () => send(target, 'stop')}] : []),
  ];
  const stateWord = (target: RemotePlaybackControls) => target.state === 'paused' ? t('remote.state.paused') : target.state === 'playing' ? t('remote.state.playing') : t('remote.state.starting');
  return <View testID="settings-panel-remote-controls">
    {items.map((target, index) => {
      if (theme.isTV) {
        const offered = transportOffers(target, target.actions);
        const command = offered.includes('pause') ? 'pause' : offered.includes('resume') ? 'resume' : undefined;
        const progress = target.durationMs ? t('player.clockOf', {position: clockText(Math.min(target.positionMs, target.durationMs) / 1000), duration: clockText(target.durationMs / 1000)}) : undefined;
        return <SettingsRow key={target.sessionId} first={index === 0} testID={`settings-remote-${target.sessionId}`}
          label={target.item?.title ?? t('remote.hiddenTitle')}
          description={[target.device.name || t('remote.device'), progress].filter(Boolean).join(', ')}
          value={command ? undefined : stateWord(target)}
          control={command ? <Button testID={`settings-remote-${target.sessionId}-${command}`} variant="glass" size="small" onPress={() => send(target, command)}>{words[command]}</Button> : undefined}/>;
      }
      return <SettingsRow key={target.sessionId} first={index === 0} testID={`settings-remote-${target.sessionId}`}
      label={target.device.name || t('remote.device')}
      description={target.item?.title ?? t('remote.hiddenTitle')}
      value={stateWord(target)}
      onPress={entries(target).length ? () => setMenu(target) : undefined}/>;
    })}
    <Text style={[shellType(12.5, 400, 'normal'), {color: theme.colors.textTertiary, paddingHorizontal: 14, paddingVertical: 10}]}>{t('remote.lede')}</Text>
    <Menu open={Boolean(menu)} onOpenChange={open => {if (!open) setMenu(undefined);}} presentation="sheet" testID="settings-remote-menu"
      title={menu ? menu.device.name || t('remote.device') : undefined}
      description={menu?.item?.title}
      items={menu ? entries(menu) : []}/>
  </View>;
}

function nativeRequestId(): string {
  const runtime = NativeModules.PorticoRuntime as undefined | {playbackRequestId?: () => string};
  if (typeof runtime?.playbackRequestId !== 'function') throw new Error('native_settings_request_id_unavailable');
  return runtime.playbackRequestId();
}

/** The account pages' custom surfaces this build draws with real data. Server pages are their own routes. */
export function renderNativeSettingsPanel(id: SettingsCustomId, deps: NativeSettingsPanelDeps, _runtime: NativeShellRuntime): React.ReactNode {
  switch (id) {
    case 'downloadsStorage': return deps.downloads ? <DownloadsStoragePanel {...deps} downloads={deps.downloads}/> : null;
    case 'deviceName': {
      deps.lifetime.assert();
      const identity = installationIdentity();
      return identity?.name ? <SettingsRow first testID="settings-device-name" label={deps.t('settings.row.deviceName')} value={identity.name}/> : null;
    }
    case 'deviceConnectionStatus': deps.lifetime.assert(); return <NativeConnectionStatusRow {...deps}/>;
    case 'deviceServerAddress': deps.lifetime.assert(); return <NativeConnectionAddressRow {...deps}/>;
    case 'deviceTestConnection': deps.lifetime.assert(); return <NativeTestConnectionRow {...deps} runtime={_runtime}/>;
    case 'deviceInsecureConnections': deps.lifetime.assert(); return <NativeInsecureConnectionsRow {...deps} runtime={_runtime}/>;
    case 'server.segments': return <IntroCreditsReview {...deps} runtime={_runtime}/>;
    case 'identity': return <IdentityPanel {...deps}/>;
    case 'profiles': return <ProfilesPanel {...deps}/>;
    case 'password': case 'twoStep': case 'devices': return <NativeAccountSecurityPanel {...deps} kind={id} runtime={_runtime}/>;
    case 'accessTokens': return <NativeAccessTokensPanel {...deps} runtime={_runtime}/>;
    case 'servers': return <ServersPanel {...deps}/>;
    case 'porticoAccount': return <PorticoAccountPanel {...deps}/>;
    case 'about': return <DeviceAboutPanel {...deps} runtime={_runtime}/>;
    case 'privacyNote': return <PrivacyNotePanel {...deps}/>;
    case 'remoteControls': return <RemoteControlsPanel {...deps}/>;
    case 'equalizer': return <SettingsRow first testID="settings-panel-equalizer" label={deps.t('next.settingsSpec.account.music.groups.0.rows.3.label')} onPress={() => deps.openRoute({kind: 'settings-equalizer'})}/>;
    default: return undefined;
  }
}
