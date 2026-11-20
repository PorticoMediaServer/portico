import React from 'react';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import {useSourceFolderBrowser} from '../../../pages/shared/settings/useRemoteSourceForms';
import type {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import type {ChosenSourceFolder} from '../../../screens/settings/remote-sources/messages';
import {Button} from '../../controls';
import {SettingsHelp} from '../SettingsParts';
import {Problem, RemoteSourceForm} from './RemoteSourceForms';

/** The folder selection shared by Add library and Add remote source. */
export function SourceFolderBrowser({controller, source, t, isCurrent, onCancel, onChoose, multiple = true}: {
  controller: NativeRemoteSourcesController; source: RemoteSource; t: typeof defaultI18n.t; isCurrent: () => boolean; onCancel: () => void;
  onChoose: (folders: readonly ChosenSourceFolder[]) => void | Promise<void>; multiple?: boolean;
}) {
  const {path, setPath, folders, chosen, error, here, picked, toggle} = useSourceFolderBrowser({controller, source, t, isCurrent, multiple});
  return <RemoteSourceForm title={t('next.remoteSources.foldersHeader', {name: source.name})} t={t} busy={false} onCancel={onCancel}
    footer={<Button variant="primary" align="stretch" disabled={!chosen.length} onPress={() => {
      if (isCurrent() && chosen.length) void onChoose(chosen);
    }}>{t('next.remoteSources.continue')}</Button>}>
    <Problem message={error}/>
    <SettingsHelp>{t('next.remoteSources.folderPath')} /{path}</SettingsHelp>
    <Button variant={picked ? 'primary' : 'glass'} accessibilityState={{selected: picked}} onPress={toggle}>{here.name}</Button>
    {!!path && <Button variant="text" size="small" onPress={() => setPath(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')}>
      {t('next.remoteSources.back')}
    </Button>}
    {folders === undefined && !error && <SettingsHelp>{t('next.remoteSources.folderListing')}</SettingsHelp>}
    {folders?.map(folder => <Button key={folder.path} variant="glass" onPress={() => setPath(folder.path)}>{folder.name}</Button>)}
  </RemoteSourceForm>;
}
