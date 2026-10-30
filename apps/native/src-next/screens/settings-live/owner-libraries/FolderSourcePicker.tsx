import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View} from 'react-native';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-sources';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {NativeRemoteSourcesController} from '../../settings/remote-sources/controller';
import {NativeRemoteSourcesScreen} from '../../settings/remote-sources/NativeRemoteSourcesScreen';
import {SourceFolderBrowser} from '../../settings/remote-sources/SourceFolderBrowser';
import type {ChosenSourceFolder} from '../../settings/remote-sources/SourceLibraryAssignment';
import {Problem, remoteSourceError} from '../../settings/remote-sources/RemoteSourceForms';
import {FolderExplorer, type ExplorerFolder} from './FolderExplorer';
import {LibraryGroup, LibraryRow} from './controls';

export type LibraryFolderChoice = Readonly<{folders: readonly ExplorerFolder[]; source?: RemoteSource; remoteFolders?: readonly ChosenSourceFolder[]}>;
type Props = {open: boolean; onOpenChange: (open: boolean) => void; api: LibraryContentApi; serverName: string; chosen: LibraryFolderChoice;
  onUse: (choice: LibraryFolderChoice) => void; multiple?: boolean; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime};

/** Setup stays a local explorer; the authenticated owner may choose a saved remote. */
export function FolderSourcePicker(props: Props) {
  const {ports, runtime, open} = props;
  const [controller, setController] = useState<NativeRemoteSourcesController>();
  const currentPorts = useRef(ports); currentPorts.current = ports;
  const isOpen = useRef(open); isOpen.current = open;
  useEffect(() => {
    if (!open || !ports || !runtime) {setController(undefined); return;}
    const owned = new NativeRemoteSourcesController(ports, () => isOpen.current && currentPorts.current === ports);
    setController(owned); void owned.load();
    return () => {owned.dispose();};
  }, [open, ports, runtime]);
  if (!ports || !runtime) return <FolderExplorer open={open} onOpenChange={props.onOpenChange} api={props.api} title={defaultI18n.t('next.ownerLibraries.explorer.choose')}
    subtitle={defaultI18n.t('next.ownerLibraries.explorer.subtitle', {server: props.serverName})} multiple={props.multiple} chosen={props.chosen.folders} onUse={folders => props.onUse({folders})}/>;
  return open && controller ? <SourcePicker key={open ? 'open' : 'closed'} {...props} ports={ports} runtime={runtime} controller={controller}/> : null;
}

type Mode = {kind: 'choose' | 'local' | 'create'} | {kind: 'remote'; source: RemoteSource};
function SourcePicker({controller, ports, runtime, ...props}: Props & {controller: NativeRemoteSourcesController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [mode, setMode] = useState<Mode>({kind: 'choose'});
  const active = useRef(mode); active.current = mode;
  const alive = useRef(true), flight = useRef(false);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const current = () => alive.current && props.open && active.current === mode && controller.getSnapshot().allowed;
  const chooseRemote = async (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => {
    if (flight.current || !current() || !folders.length || props.multiple === false && folders.length !== 1) return;
    flight.current = true;
    try {
      await controller.validateAssignmentFolders(source, folders, current);
      if (current()) {props.onUse({source, folders, remoteFolders: folders}); props.onOpenChange(false);}
    } finally {flight.current = false;}
  };
  const menu = () => {setMode({kind: 'choose'});};
  if (mode.kind === 'local') return <FolderExplorer open onOpenChange={open => {if (!open) menu();}} api={props.api} title={t('next.ownerLibraries.explorer.choose')}
    subtitle={t('next.ownerLibraries.explorer.subtitle', {server: props.serverName})} multiple={props.multiple} chosen={props.chosen.source ? [] : props.chosen.folders}
    onUse={folders => {props.onUse({folders}); props.onOpenChange(false);}}/>;
  return <BottomSheet open onOpenChange={props.onOpenChange} title={t('web.libraries.addFolder')} scrollable={false} detents={[760]}>
    {mode.kind === 'remote' ? <View style={{flex: 1}}><SourceFolderBrowser controller={controller} source={mode.source} t={t} isCurrent={current} multiple={props.multiple}
      initial={props.chosen.source?.id === mode.source.id && props.chosen.source.generation === mode.source.generation ? props.chosen.remoteFolders : undefined}
      onCancel={menu} onChoose={folders => chooseRemote(mode.source, folders)}/></View>
    : mode.kind === 'create' ? <NativeRemoteSourcesScreen ports={ports} runtime={runtime} initialCreate multipleFolders={props.multiple} onDismiss={() => {menu(); void controller.load();}}
      onChooseFolders={chooseRemote}/>
    : <FlatList data={snapshot.allowed ? snapshot.sources.sources : []} keyExtractor={source => source.id}
      initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} contentContainerStyle={{gap: 12}}
      ListHeaderComponent={<View style={{gap: 12}}>
        {!!props.chosen.folders.length && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.ownerLibraries.explorer.replacesSelection')}</Text>}
        <LibraryGroup><LibraryRow first icon="i-folder" label={t('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addMenu.0.label')} onPress={() => setMode({kind: 'local'})}/></LibraryGroup>
      </View>}
      renderItem={({item: source}) => <LibraryGroup><LibraryRow first icon="i-disk" label={source.name} help={source.origin} onPress={() => setMode({kind: 'remote', source})}/></LibraryGroup>}
      ListFooterComponent={<View style={{gap: 12}}>
        {snapshot.allowed && <LibraryGroup><LibraryRow first icon="i-plus" label={t('next.remoteSources.add')} onPress={() => setMode({kind: 'create'})}/></LibraryGroup>}
        {snapshot.sources.loading && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.loading')}</Text>}
        {snapshot.sources.error && <><Problem message={remoteSourceError(t, {code: snapshot.sources.errorCode})}/><Button variant="glass" onPress={() => {void controller.load();}}>{t('action.tryAgain')}</Button></>}
      </View>}/>
    }
  </BottomSheet>;
}
