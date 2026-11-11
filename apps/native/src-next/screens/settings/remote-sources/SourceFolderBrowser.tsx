import React, {useEffect, useState} from 'react';
import {Text} from 'react-native';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RemoteFolder, RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import {Button} from '../../../kit/controls/Button';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, remoteSourceError, type ChosenSourceFolder} from './RemoteSourceForms';

/** A source's folders, opened one at a time, for Add library: the chosen folders go back to that page. */
export function SourceFolderBrowser({controller, source, t, isCurrent, onCancel, onChoose, multiple = true}: {
  controller: NativeRemoteSourcesController; source: RemoteSource; t: typeof defaultI18n.t; isCurrent: () => boolean; onCancel: () => void;
  onChoose: (folders: readonly ChosenSourceFolder[]) => void | Promise<void>; multiple?: boolean}) {
  const [path, setPath] = useState(''), [folders, setFolders] = useState<readonly RemoteFolder[]>(), [chosen, setChosen] = useState<readonly ChosenSourceFolder[]>([]), [error, setError] = useState<string>();
  useEffect(() => {
    setFolders(undefined);
    void controller.folders(source.id, path).then(list => {if (isCurrent()) setFolders(list);}, failure => {if (isCurrent()) setError(remoteSourceError(t, failure));});
    // isCurrent and t are read when the answer arrives; a new one each render must not read the folders again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, source.id, path]);
  const here = {name: path.split('/').pop() || source.name, path};
  const picked = chosen.some(one => one.path === path);
  return <Page title={t('next.remoteSources.foldersHeader', {name: source.name})} t={t} busy={false} onCancel={onCancel}
    footer={<Button variant="primary" disabled={!chosen.length} onPress={() => {void onChoose(chosen);}}>{t('next.remoteSources.continue')}</Button>}>
    <Problem message={error}/>
    <Text>{t('next.remoteSources.folderPath')} /{path}</Text>
    <Button variant={picked ? 'primary' : 'glass'} onPress={() => setChosen(current => picked ? current.filter(one => one.path !== path) : multiple ? [...current, here] : [here])}>{here.name}</Button>
    {!!path && <Button variant="text" size="small" onPress={() => setPath(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')}>{t('next.remoteSources.back')}</Button>}
    {folders === undefined && !error ? <Text>{t('next.remoteSources.folderListing')}</Text> : folders?.map(folder => <Button key={folder.path} variant="glass" onPress={() => setPath(folder.path)}>{folder.name}</Button>)}
  </Page>;
}
