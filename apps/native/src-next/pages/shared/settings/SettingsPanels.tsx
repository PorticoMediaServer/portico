import React from 'react';
import type {SettingsCustomId} from '../../../../../../packages/client-core/src/presentation/settings-structure';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {installationIdentity} from '../../../../../../packages/client-core/src/installation';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {SettingsPrivacyNote, SettingsRemoteControls} from '../../../kit/settings/SettingsPanelParts';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsDownloads} from '../../../screens/settings-live/ports';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';
import {ProfilesRoutePage as ProfileRoute} from '../accounts/ProfilesRoutePage';
import {ServersRoutePage as ServersRoute} from '../accounts/ServersRoutePage';
import {AccountSecurityPanel} from './AccountSecurityPanel';
import {NativeConnectionAddressRow, NativeConnectionStatusRow, NativeInsecureConnectionsRow, NativeTestConnectionRow} from './ConnectionRows';
import {AccessTokensPanel} from '../../touch/settings/AccessTokensPanel';
import {DeviceAboutRows} from '../../touch/settings/DeviceAboutRows';
import {AllowedDevicesRows} from '../../tv/settings/AllowedDevicesRows';
// Named through pages/shared so the screen harness loads the hooks for real when a test loads these panels.
import {useDownloadsStorage, useRemoteControls} from '../../../pages/shared/settings/useSettingsPanels';

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
  return theme.isTV ? <SettingsRow first navigates testID="settings-panel-profiles" label={t('settings.section.profiles')} onPress={() => openRoute({kind: 'profiles'})}/>
    : <ProfileRoute context={context} route={context.navigation.getSnapshot().route} onBack={() => {}} embedded/>;
}

function ServersPanel({context, t, openRoute}: NativeSettingsPanelDeps) {
  const theme = useTheme();
  // connect-24: a TV's Switch server page is the chooser itself, drawn in Settings.
  return theme.isTV ? <ServersRoute context={context} embedded openPanel={initialPanel => openRoute({kind: 'servers', initialPanel})}/>
    : <SettingsRow first testID="settings-panel-servers" label={t('settings.section.servers')} value={context.serverName}
      onPress={() => openRoute({kind: 'servers'})}/>;
}

function DownloadsStoragePanel(props: NativeSettingsPanelDeps & {downloads: NativeSettingsDownloads}) {
  const row = useDownloadsStorage(props), t = props.t;
  if (!row) return null;
  return <SettingsRow first testID="settings-panel-downloads-storage" label={t('settings.row.downloadsStorage')}
    value={row.used} description={t('settings.downloadsStorage.free', {free: row.free})} onPress={row.open}/>;
}

/** What this profile is playing elsewhere, read from the server. Nothing is drawn while nothing is. */
function RemoteControlsPanel(props: NativeSettingsPanelDeps) {
  const theme = useTheme(), t = props.t, view = useRemoteControls(props), {menu} = view;
  if (!view.items.length) return null;
  return <>
    <SettingsRemoteControls lede={t('remote.lede')}>
      {view.items.map((target, index) => {
        if (theme.isTV) {
          const {command, progress} = view.tvRow(target);
          return <SettingsRow key={target.sessionId} first={index === 0} testID={`settings-remote-${target.sessionId}`}
            label={target.item?.title ?? t('remote.hiddenTitle')}
            description={[target.device.name || t('remote.device'), progress].filter(Boolean).join(', ')}
            value={command ? undefined : view.stateWord(target)}
            control={command ? <Button testID={`settings-remote-${target.sessionId}-${command}`} variant="glass" size="small" onPress={() => view.send(target, command)}>{view.words[command]}</Button> : undefined}/>;
        }
        return <SettingsRow key={target.sessionId} first={index === 0} testID={`settings-remote-${target.sessionId}`}
          label={target.device.name || t('remote.device')}
          description={target.item?.title ?? t('remote.hiddenTitle')}
          value={view.stateWord(target)}
          onPress={view.entries(target).length ? () => view.setMenu(target) : undefined}/>;
      })}
    </SettingsRemoteControls>
    <Menu open={Boolean(menu)} onOpenChange={open => {if (!open) view.setMenu(undefined);}} presentation="sheet" testID="settings-remote-menu"
      title={menu ? menu.device.name || t('remote.device') : undefined}
      description={menu?.item?.title}
      items={menu ? view.entries(menu) : []}/>
  </>;
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
    case 'identity': return <IdentityPanel {...deps}/>;
    case 'profiles': return <ProfilesPanel {...deps}/>;
    case 'password': case 'twoStep': case 'devices': return <AccountSecurityPanel {...deps} kind={id} runtime={_runtime}/>;
    case 'accessTokens': return <AccessTokensPanel {...deps} runtime={_runtime}/>;
    case 'servers': return <ServersPanel {...deps}/>;
    case 'porticoAccount': return <PorticoAccountPanel {...deps}/>;
    case 'about': return <DeviceAboutRows {...deps} runtime={_runtime}/>;
    case 'privacyNote': return <SettingsPrivacyNote>{deps.t('pref.note.privacy.ownerSeesPlays')}</SettingsPrivacyNote>;
    case 'remoteControls': return <RemoteControlsPanel {...deps}/>;
    case 'allowedDevices': return <AllowedDevicesRows {...deps}/>;
    case 'equalizer': return <SettingsRow navigates testID="settings-panel-equalizer" label={deps.t('next.settingsSpec.account.music.groups.0.rows.3.label')} onPress={() => deps.openRoute({kind: 'settings-equalizer'})}/>;
    default: return undefined;
  }
}
