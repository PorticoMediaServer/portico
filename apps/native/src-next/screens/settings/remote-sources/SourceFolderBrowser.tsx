import React, {useContext, useEffect, useRef, useState} from 'react';
import {FlatList, Text, View} from 'react-native';
import type {RemoteFolderPage, RemoteSource} from '../../../../../../packages/client-core/src/remote-sources';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Switch} from '../../../kit/controls/Switch';
import {Icon} from '../../../kit/Icon';
import {Focusable} from '../../../kit/controls/Focusable';
import {useTheme} from '../../../kit/theme';
import type {ChosenSourceFolder} from './SourceLibraryAssignment';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, remoteSourceError, SourceWizardContext, useAttempt} from './RemoteSourceForms';

/** Saved-source directory producer only; no local filesystem discovery or invented folders. */
export function SourceFolderBrowser({controller, source, t, isCurrent, onCancel, onChoose, initial, onLater, onSelectionChange, multiple = true}: {
  controller: NativeRemoteSourcesController; source: RemoteSource; t: typeof defaultI18n.t; isCurrent: () => boolean; onCancel: () => void;
  onChoose: (folders: readonly ChosenSourceFolder[]) => void | Promise<void>; initial?: readonly ChosenSourceFolder[]; onLater?: () => void; multiple?: boolean; onSelectionChange?: (folders: readonly ChosenSourceFolder[]) => void;
}) {
  const theme = useTheme(), wizard = useContext(SourceWizardContext), current = useAttempt(isCurrent), flight = useRef(false), selectionRevision = useRef(0);
  const renderedSelection = selectionRevision.current;
  // The current assignment's chosen set, preserved exactly; a set over the maximum is
  // kept whole and the choose action stays refused rather than silently dropping one.
  const [chosen, setChosen] = useState<readonly ChosenSourceFolder[]>(() => initial ? [...initial] : []);
  const overflow = chosen.length > (multiple ? 64 : 1);
  const toggle = (folder: ChosenSourceFolder) => {
    if (flight.current || !current()) return;
    const next = chosen.some(row => row.relative === folder.relative) ? chosen.filter(row => row.relative !== folder.relative)
      : !multiple ? [folder] : chosen.length < 64 ? [...chosen, folder] : chosen;
    selectionRevision.current++; setChosen(next); onSelectionChange?.(next);
  };
  const [page, setPage] = useState<RemoteFolderPage>(), [hidden, setHidden] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const read = async (path = '', showHidden = hidden, more = false) => {
    if (flight.current || !current() || more && (!page?.nextCursor || page.entries.length >= 500)) return;
    const base = more ? page : undefined;
    flight.current = true; setBusy(true); setError(undefined);
    if (!more) setPage(undefined);
    try {
      const next = await controller.sourceFolders(source, {...(path ? {path} : {}), showHidden, ...(base ? {cursor: base.nextCursor} : {})}, current);
      if (!current()) return;
      if (base && (next.relative !== base.relative || next.path !== base.path || next.generation !== base.generation || next.nextCursor && next.nextCursor === base.nextCursor))
        throw Object.assign(new Error('remote_source_changed'), {code: 'remote_source_changed'});
      const entries = base ? [...new Map([...base.entries, ...next.entries].map(value => [value.relative, value])).values()] : next.entries;
      setPage({...next, entries: entries.slice(0, 500), truncated: next.truncated || entries.length > 500}); setHidden(showHidden);
    } catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  useEffect(() => {void read();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const choose = async () => {
    if (flight.current || !current() || selectionRevision.current !== renderedSelection || !chosen.length || overflow) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {await onChoose(chosen);}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  const parent = page?.relative.split('/').slice(0, -1).join('/') ?? '';
  const trail = (page?.relative ?? '').split('/').filter(Boolean);
  return <Page title={t('next.remoteSources.foldersSection')} t={t} busy={busy} onCancel={onCancel} scrollable={false} footer={<Button disabled={busy || !chosen.length || overflow} onPress={() => {void choose();}}>{wizard ? t('next.remoteSources.continue') : t(chosen.length === 1 ? 'next.remoteSources.useOneFolder' : 'next.remoteSources.useFolders', {count: chosen.length})}</Button>}>
    <FlatList data={page?.entries ?? []} keyExtractor={folder => folder.relative} extraData={{chosen, busy}}
      style={{flex: 1}} initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} keyboardShouldPersistTaps="handled"
      ListHeaderComponent={<View style={{gap: 16, paddingBottom: 16}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{source.name}</Text>
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.foldersOnlyHint')}</Text>
    {page && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 6}}>
      <Button variant="text" size="small" disabled={busy} onPress={() => {void read('');}}>{source.name}</Button>
      {trail.map((part, index) => {
        const prefix = trail.slice(0, index + 1).join('/');
        return <View key={prefix} style={{flexDirection: 'row', alignItems: 'center', gap: 6}}>
          <Icon name="i-right" size={14} color={theme.colors.textTertiary}/>
          <Button variant="text" size="small" disabled={busy || index === trail.length - 1} onPress={() => {void read(prefix);}}>{part}</Button>
        </View>;
      })}
    </View>}
    {page && <Button variant="glass" disabled={busy} onPress={() => toggle({name: page.relative.split('/').pop() || source.name, path: page.path, relative: page.relative})}>{t(chosen.some(row => row.relative === page.relative) ? 'next.remoteSources.chosen' : 'next.remoteSources.chooseThisFolder')}</Button>}
    {!!chosen.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
      {chosen.map(folder => <Button key={folder.relative} variant="text" disabled={busy} onPress={() => toggle(folder)}>{t('next.remoteSources.removeFolder', {name: folder.name})}</Button>)}
    </View>}
    <Switch checked={hidden} disabled={busy} onCheckedChange={value => {void read(page?.relative ?? '', value);}}>{t('next.remoteSources.showHiddenFolders')}</Switch>
    {!!page?.relative && <Button disabled={busy} variant="text" onPress={() => {void read(parent);}}>{t('next.remoteSources.upOneFolder')}</Button>}
    {busy && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.folderListing')}</Text>}
    {page && !page.entries.length && !busy && <>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.noFoldersHere')}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.noFoldersHereHelp')}</Text>
    </>}
    </View>}
    renderItem={({item: folder}) => <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Focusable accessibilityRole="checkbox" accessibilityLabel={folder.name} accessibilityState={{checked: chosen.some(row => row.relative === folder.relative), disabled: busy || multiple && chosen.length >= 64 && !chosen.some(row => row.relative === folder.relative)}}
        disabled={busy || multiple && chosen.length >= 64 && !chosen.some(row => row.relative === folder.relative)} onPress={() => toggle(folder)} focusKind="chip" focusRadius={6}
        style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, borderColor: chosen.some(row => row.relative === folder.relative) ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: chosen.some(row => row.relative === folder.relative) ? theme.colors.accentStrong : 'transparent', alignItems: 'center', justifyContent: 'center'}}>
        {chosen.some(row => row.relative === folder.relative) && <Icon name="i-check" size={14} color={theme.colors.ink}/>}
      </Focusable>
      <Button disabled={busy} variant="text" style={{flex: 1}} accessibilityLabel={t('next.remoteSources.openFolder', {name: folder.name})}
        onPress={() => {void read(folder.relative);}}>{folder.name}</Button>
      <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </View>}
    ListFooterComponent={<View style={{gap: 16, paddingTop: 16}}>
    {!!page?.nextCursor && page.entries.length < 500 && <Button variant="text" disabled={busy} onPress={() => {void read(page.relative, hidden, true);}}>{t('next.remoteSources.showMoreFolders')}</Button>}
    {(page?.truncated || !!page?.nextCursor && page.entries.length >= 500) && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.folderTruncated')}</Text>}
    {onLater && <Button variant="text" disabled={busy} onPress={onLater}>{t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.2.groups.0.rows.0.control.options.0.label')}</Button>}
    <Problem message={error}/>
    <Button variant="text" disabled={busy} onPress={() => {void read(page?.relative ?? '');}}>{t('next.remoteSources.refresh')}</Button>
    </View>}/>
  </Page>;
}
