import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import type {NativeSettingsLivePorts} from '../../settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Button} from '../../../kit/controls/Button';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {NativeRemoteSourcesController} from './controller';
import {AddSourceFlow, FolderStep, ManageSource, Problem, remoteSourceError, stateWords, type ChosenSourceFolder} from './RemoteSourceForms';
import {SourceFolderBrowser} from './SourceFolderBrowser';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onBackHandlerChange?: (handler: (() => boolean) | undefined) => void;
  initialCreate?: boolean; multipleFolders?: boolean; onDismiss?: () => void; onChooseFolders?: (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => void | Promise<void>};
type Mode = {kind: 'list'} | {kind: 'add'} | {kind: 'manage'; source: RemoteSource} | {kind: 'folders'; source: RemoteSource};

/** Server › Remote sources on the new server (definition 03, rules 7 to 14). Only the owner reaches it (rule 7). */
export function NativeRemoteSourcesScreen(props: Props) {
  const currentPorts = useRef(props.ports);
  currentPorts.current = props.ports;
  const [controller, setController] = useState<NativeRemoteSourcesController>();
  useEffect(() => {
    const owned = new NativeRemoteSourcesController(props.ports, () => currentPorts.current === props.ports);
    setController(owned);
    void owned.load();
    return () => owned.dispose();
  }, [props.ports]);
  return controller ? <Viewport {...props} controller={controller}/> : <StateView title={props.runtime.text.t('next.remoteSources.loading')}/>;
}

function Viewport({controller, runtime, onBackHandlerChange, initialCreate, onDismiss, onChooseFolders, multipleFolders}: Props & {controller: NativeRemoteSourcesController}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [mode, setMode] = useState<Mode>(initialCreate ? {kind: 'add'} : {kind: 'list'});
  const current = () => controller.getSnapshot().allowed;
  const back = () => {
    if (mode.kind === 'list') return false;
    if (initialCreate && mode.kind === 'add') onDismiss?.(); else setMode({kind: 'list'});
    return true;
  };
  useEffect(() => {onBackHandlerChange?.(back); return () => onBackHandlerChange?.(undefined);});
  if (!snapshot.allowed && !snapshot.loading) return <StateView title={t('next.remoteSources.error.forbidden')}/>;
  if (mode.kind === 'add') return <AddSourceFlow controller={controller} t={t} isCurrent={current} onCancel={() => {back();}}
    onSaved={source => setMode(onChooseFolders ? {kind: 'folders', source} : {kind: 'folders', source})}/>;
  if (mode.kind === 'manage') return <ManageSource controller={controller} t={t} source={mode.source} isCurrent={current} onClose={() => setMode({kind: 'list'})}
    onContinue={source => setMode({kind: 'folders', source})}/>;
  if (mode.kind === 'folders') return onChooseFolders
    // Opened from Add library: the chosen folders go back to that page.
    ? <SourceFolderBrowser controller={controller} source={mode.source} t={t} isCurrent={current} multiple={multipleFolders} onCancel={() => setMode({kind: 'list'})}
      onChoose={folders => onChooseFolders(mode.source, folders)}/>
    : <FolderStep controller={controller} t={t} source={mode.source} isCurrent={current} onCancel={() => setMode({kind: 'list'})} onDone={() => setMode({kind: 'list'})}/>;
  return <ScrollView contentContainerStyle={{padding: 20, gap: 12}}>
    <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{t('next.settingsSpec.server.remote-sources.groups.0.title')}</Text>
    <Button variant="primary" onPress={() => setMode({kind: 'add'})}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.addLabel')}</Button>
    {snapshot.error !== undefined && <><Problem message={remoteSourceError(t, snapshot.error)}/><Button variant="glass" onPress={() => {void controller.load();}}>{t('action.tryAgain')}</Button></>}
    {snapshot.loading && !snapshot.sources.length && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.loading')}</Text>}
    {!snapshot.loading && !snapshot.sources.length && snapshot.error === undefined && <StateView title={t('next.remoteSources.empty')} body={t('next.remoteSources.emptyHelp')}/>}
    {snapshot.sources.map(source => <View key={source.id} style={{gap: 6, padding: 14, borderRadius: 14, backgroundColor: theme.colors.raised}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{source.name}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(stateWords[source.state] ?? 'next.remoteSources.state.problem')}{source.problem ? ` · ${source.problem}` : ''}</Text>
      <View style={{flexDirection: 'row', gap: 8}}>
        {source.state === 'setup' && <Button variant="primary" size="small" onPress={() => setMode({kind: 'folders', source})}>{t('next.remoteSources.continueSetup')}</Button>}
        <Button variant="glass" size="small" onPress={() => setMode({kind: 'manage', source})}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.itemActions.0.label')}</Button>
      </View>
    </View>)}
  </ScrollView>;
}
