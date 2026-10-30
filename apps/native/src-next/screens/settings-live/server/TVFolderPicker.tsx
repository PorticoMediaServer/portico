import React, {useEffect, useRef, useState} from 'react';
import {Text} from 'react-native';
import type {FilesystemPage} from '../../../../../../packages/client-core/src/administration';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {browseServerFilesystem} from '../../../../../../packages/client-core/src/library-management';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {SidePanel} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {appendExplorerPage, explorerEntries} from '../owner-libraries/folder-explorer-model';

type Translate = typeof defaultI18n.t;

/** A folder row on TV: shows the path (paths are drawn on TV, P2-39) and opens the picker. */
export function TVFolderRow({testID, label, description, value, api, disabled, dependent, t, onChoose}: {
  testID: string; label: string; description?: string; value: string; api: LibraryContentApi;
  disabled?: boolean; dependent?: boolean; t: Translate; onChoose: (path: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return <>
    <SettingsRow testID={testID} label={label} description={description} value={value || undefined} dependent={dependent}
      onPress={disabled ? undefined : () => setOpen(true)} readable={disabled}/>
    <TVFolderPicker open={open && !disabled} testID={`${testID}-picker`} title={label} start={value} api={api} t={t}
      onClose={() => setOpen(false)} onChoose={path => {setOpen(false); if (path !== value) onChoose(path);}}/>
  </>;
}

/** One folder at a time in the side panel, as the remote moves: Up, the folders inside (OK opens
 * one), and Use this folder. Only folders are listed, never files. */
export function TVFolderPicker({open, testID, title, start, api, t, onClose, onChoose}: {
  open: boolean; testID: string; title: string; start: string; api: LibraryContentApi; t: Translate;
  onClose: () => void; onChoose: (path: string) => void;
}) {
  const theme = useTheme(), instance = React.useId(), id = `${testID}.${instance}`;
  const [path, setPath] = useState(start), [page, setPage] = useState<FilesystemPage>(), [error, setError] = useState<unknown>(), [loading, setLoading] = useState(false), [retry, setRetry] = useState(0);
  const request = useRef<AbortController | undefined>(undefined);
  useEffect(() => {if (open) setPath(start);}, [open, start]);
  useEffect(() => {
    if (!open) return;
    const owned = new AbortController(); request.current = owned;
    setLoading(true); setError(undefined); setPage(undefined);
    browseServerFilesystem(api, {path: path || undefined, checkWritable: true, limit: 200}, owned.signal)
      .then(value => {if (request.current === owned && !owned.signal.aborted) setPage(value);})
      .catch(reason => {if (request.current === owned && !owned.signal.aborted) setError(reason);})
      .finally(() => {if (request.current === owned && !owned.signal.aborted) setLoading(false);});
    return () => {owned.abort(); if (request.current === owned) request.current = undefined;};
  }, [open, path, api, retry]);
  const more = () => {
    const owned = request.current, base = page;
    if (!owned || !base?.nextCursor || loading) return;
    setLoading(true);
    browseServerFilesystem(api, {path: path || undefined, cursor: base.nextCursor, checkWritable: true, limit: 200}, owned.signal)
      .then(value => {if (request.current === owned && !owned.signal.aborted) setPage(current => current === base ? appendExplorerPage(base, value) : current);})
      .catch(reason => {if (request.current === owned && !owned.signal.aborted) setError(reason);})
      .finally(() => {if (request.current === owned && !owned.signal.aborted) setLoading(false);});
  };
  const folders = explorerEntries(page, 'folders'), list = `${id}.list`, footer = `${id}.footer`;
  return <SidePanel open={open} testID={id} width={760} topInset={0} showClose={false} title={title} description={page?.path || undefined}
    onOpenChange={next => {if (!next) onClose();}} titleTextStyle={shellType(44, 800, 'normal', -.025)} style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}
    footer={<FocusRegion id={footer} orientation="horizontal" entry={page?.path ? 'use' : 'cancel'} edges={{up: list}} style={{flexDirection: 'row', gap: 16}}>
      <Button focusId="cancel" testID={`${testID}-cancel`} variant="glass" onPress={onClose} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button focusId="use" testID={`${testID}-use`} variant="primary" disabled={!page?.path} onPress={() => {if (page?.path) onChoose(page.path);}} style={{flex: 1}}>{t('next.ownerLibraries.explorer.useFolder')}</Button>
    </FocusRegion>}>
    {error !== undefined ? <StateView variant="inline" title={t('next.ownerLibraries.explorer.failed')} body={t('next.ownerLibraries.explorer.failedHelp')}
      primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: () => setRetry(value => value + 1)}}/> : null}
    <FocusRegion id={list} orientation="vertical" edges={{down: footer}} style={{backgroundColor: theme.colors.raised, borderRadius: 18}}>
      {page?.parent ? <SettingsRow testID={`${testID}-up`} label={t('next.ownerLibraries.explorer.up')} onPress={() => setPath(page.parent)}/> : null}
      {folders.map(folder => <SettingsRow key={folder.path} testID={`${testID}-folder`} label={folder.name} onPress={folder.readable ? () => setPath(folder.path) : undefined} readable={!folder.readable}/>)}
      {page?.nextCursor ? <SettingsRow testID={`${testID}-more`} label={t('next.ownerLibraries.explorer.more')} onPress={more}/> : null}
    </FocusRegion>
    {loading ? <Text style={[shellType(22), {color: theme.colors.textTertiary, marginTop: 16}]}>{t('next.ownerLibraries.explorer.reading')}</Text>
      : page && !folders.length ? <Text style={[shellType(22), {color: theme.colors.textTertiary, marginTop: 16}]}>{t('next.ownerLibraries.explorer.noFolders')}</Text> : null}
    {page && !page.writable && page.writableChecked ? <Text style={[shellType(22), {color: theme.colors.amber, marginTop: 16}]}>{t('next.ownerLibraries.explorer.notWritable')}</Text> : null}
  </SidePanel>;
}
