import React from 'react';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {AddSourceFlow, FolderStep, ManageSource} from '../../../kit/settings/remote-sources/RemoteSourceForms';
import {SourceFolderBrowser} from '../../../kit/settings/remote-sources/SourceFolderBrowser';
import {SettingsRow} from '../../../kit/shell';
import {remoteSourceError, stateWords, type ChosenSourceFolder} from '../../../screens/settings/remote-sources/messages';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useRemoteSources} from './useRemoteSources';

type Props = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'> & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; retry?: React.ReactNode;
  onBackHandlerChange?: (handler: (() => boolean) | undefined) => void;
  initialCreate?: boolean; multipleFolders?: boolean; onDismiss?: () => void;
  onChooseFolders?: (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => void | Promise<void>;
};

export function RemoteSourcesPage({ports, runtime, onBackHandlerChange, initialCreate, multipleFolders, onDismiss, onChooseFolders, back, server, retry}: Props) {
  const {t} = runtime.text;
  const source = useRemoteSources(ports, initialCreate, onDismiss, onBackHandlerChange);
  const {controller, snapshot, mode, setMode, list, current} = source;
  if (controller && snapshot.allowed) {
    if (mode.kind === 'add') return <AddSourceFlow controller={controller} t={t} isCurrent={current} onCancel={source.back}
      onSaved={value => setMode({kind: 'folders', source: value})}/>;
    if (mode.kind === 'manage') return <ManageSource controller={controller} t={t} source={mode.source} isCurrent={current} onClose={list}
      onContinue={value => setMode({kind: 'folders', source: value})}/>;
    if (mode.kind === 'folders') return onChooseFolders
      ? <SourceFolderBrowser controller={controller} source={mode.source} t={t} isCurrent={current} multiple={multipleFolders}
        onCancel={list} onChoose={folders => onChooseFolders(mode.source, folders)}/>
      : <FolderStep controller={controller} t={t} source={mode.source} isCurrent={current} onCancel={list} onDone={list}/>;
  }
  const empty = snapshot.allowed && !snapshot.loading && !snapshot.sources.length && snapshot.error === undefined;
  const add = t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.addLabel');
  return <SettingsPageFrame title={t('next.settingsSpec.server.remote-sources.groups.0.title')} back={back} server={server} testID="server-remote-sources">
    {retry}
    {!snapshot.allowed && !snapshot.loading && <SectionCard><StateRow kind="error" icon="i-lock" title={t('next.remoteSources.error.forbidden')}/></SectionCard>}
    {snapshot.allowed && <SectionCard>
      <PanelBlock>
        <Button variant="primary" size="compact" icon="i-plus" disabled={!controller} onPress={() => setMode({kind: 'add'})}>{add}</Button>
      </PanelBlock>
      {snapshot.error !== undefined && <StateRow kind="error" icon="i-alert" title={remoteSourceError(t, snapshot.error)}
        action={{label: t('action.tryAgain'), onPress: () => {void controller?.load();}}}/>}
      {snapshot.loading && !snapshot.sources.length && <SettingsRow first label={t('next.remoteSources.loading')}/>}
      {empty && <SettingsRow first label={t('next.remoteSources.empty')} description={t('next.remoteSources.emptyHelp')}/>}
      {snapshot.sources.map((value, index) => <React.Fragment key={value.id}>
        <SettingsRow first={index === 0} label={value.name} icon="i-disk"
          description={[t(stateWords[value.state] ?? 'next.remoteSources.state.problem'), ...(value.problem ? [value.problem] : [])]}
          accessibilityLabel={`${value.name}, ${t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.itemActions.0.label')}`}
          navigates onPress={() => setMode({kind: 'manage', source: value})}/>
        {value.state === 'setup' && <PanelBlock>
          <Button size="compact" onPress={() => setMode({kind: 'folders', source: value})}>{t('next.remoteSources.continueSetup')}</Button>
        </PanelBlock>}
      </React.Fragment>)}
    </SectionCard>}
  </SettingsPageFrame>;
}
