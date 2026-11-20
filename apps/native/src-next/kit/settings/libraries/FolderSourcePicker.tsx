import React from 'react';
import {FlatList, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useFolderSourcePicker, usePickFolderSource, type Props} from '../../../pages/shared/settings/useFolderSourcePicker';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import {NativeRemoteSourcesScreen} from '../../../screens/settings/remote-sources/NativeRemoteSourcesScreen';
import {SourceFolderBrowser} from '../../../screens/settings/remote-sources/SourceFolderBrowser';
import {Problem, remoteSourceError} from '../../../screens/settings/remote-sources/RemoteSourceForms';
import {Button} from '../../controls';
import {BottomSheet} from '../../overlays/BottomSheet';
import {SettingsGroup, SettingsRow} from '../../shell';
import {useTheme} from '../../theme';
import {SettingsHelp} from '../SettingsParts';
import {FolderExplorer} from './FolderExplorer';

export function FolderSourcePicker(props: Props) {
  const {ports, runtime, open} = props, controller = useFolderSourcePicker(props);
  if (!ports || !runtime) return <FolderExplorer open={open} onOpenChange={props.onOpenChange} api={props.api} title={defaultI18n.t('next.ownerLibraries.explorer.choose')}
    subtitle={defaultI18n.t('next.ownerLibraries.explorer.subtitle', {server: props.serverName})} multiple={props.multiple} chosen={props.chosen.folders} onUse={folders => props.onUse({folders})}/>;
  return open && controller ? <SourcePicker key={open ? 'open' : 'closed'} {...props} ports={ports} runtime={runtime} controller={controller}/> : null;
}

function SourcePicker({controller, ports, runtime, ...props}: Props & {
  controller: NativeRemoteSourcesController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime;
}) {
  const theme = useTheme();
  const {t, snapshot, mode, setMode, current, chooseRemote, menu} = usePickFolderSource({controller, ports, runtime, ...props});
  if (mode.kind === 'local') return <FolderExplorer open onOpenChange={open => {if (!open) menu();}} api={props.api} title={t('next.ownerLibraries.explorer.choose')}
    subtitle={t('next.ownerLibraries.explorer.subtitle', {server: props.serverName})} multiple={props.multiple} chosen={props.chosen.source ? [] : props.chosen.folders}
    onUse={folders => {props.onUse({folders}); props.onOpenChange(false);}}/>;
  return <BottomSheet open onOpenChange={props.onOpenChange} title={t('web.libraries.addFolder')} scrollable={false} detents={[760]}>
    {mode.kind === 'remote' ? <View style={{flex: 1}}><SourceFolderBrowser controller={controller} source={mode.source} t={t} isCurrent={current} multiple={props.multiple}
      onCancel={menu} onChoose={folders => chooseRemote(mode.source, folders)}/></View>
    : mode.kind === 'create' ? <NativeRemoteSourcesScreen ports={ports} runtime={runtime} initialCreate multipleFolders={props.multiple} onDismiss={() => {menu(); void controller.load();}}
      onChooseFolders={chooseRemote}/>
    : <FlatList data={snapshot.allowed ? snapshot.sources : []} keyExtractor={source => source.id}
      initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} contentContainerStyle={{gap: 12}}
      ListHeaderComponent={<View style={{gap: theme.space.controlGap}}>
        {!!props.chosen.folders.length && <SettingsHelp>{t('next.ownerLibraries.explorer.replacesSelection')}</SettingsHelp>}
        <SettingsGroup><SettingsRow navigates first icon="i-folder" label={t('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addMenu.0.label')} onPress={() => setMode({kind: 'local'})}/></SettingsGroup>
      </View>}
      renderItem={({item: source}) => <SettingsGroup><SettingsRow navigates first icon="i-disk" label={source.name} description={source.webdav?.address ?? source.rclone?.type} onPress={() => setMode({kind: 'remote', source})}/></SettingsGroup>}
      ListFooterComponent={<View style={{gap: theme.space.controlGap}}>
        {snapshot.allowed && <SettingsGroup><SettingsRow navigates first icon="i-plus" label={t('next.remoteSources.add')} onPress={() => setMode({kind: 'create'})}/></SettingsGroup>}
        {snapshot.loading && <SettingsHelp>{t('next.remoteSources.loading')}</SettingsHelp>}
        {snapshot.error !== undefined && <><Problem message={remoteSourceError(t, snapshot.error)}/><Button variant="glass" onPress={() => {void controller.load();}}>{t('action.tryAgain')}</Button></>}
      </View>}/>
    }
  </BottomSheet>;
}
