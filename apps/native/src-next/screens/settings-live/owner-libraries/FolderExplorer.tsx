import React, {useEffect, useMemo, useRef, useState} from 'react';
import {FlatList, Text, View} from 'react-native';
import {browseServerFilesystem} from '../../../../../../packages/client-core/src/library-management';
import type {FilesystemEntry, FilesystemPage} from '../../../../../../packages/client-core/src/administration';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../../kit/controls/Button';
import {Focusable} from '../../../kit/controls/Focusable';
import {Switch} from '../../../kit/controls/Switch';
import {TextField} from '../../../kit/controls/TextField';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {Icon} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import {LibraryGroup, LibraryRow} from './controls';

import {appendExplorerPage, explorerEntries, toggleExplorerSelection, type ExplorerFolder, type ExplorerSelectionMode} from './folder-explorer-model';
export type {ExplorerFolder} from './folder-explorer-model';

export type FolderExplorerProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  api: LibraryContentApi;
  title: string;
  subtitle?: string;
  chosen: readonly ExplorerFolder[];
  onUse: (folders: readonly ExplorerFolder[]) => void;
  multiple?: boolean;
  selectionMode?: ExplorerSelectionMode;
  useLabel?: string;
  testID?: string;
}>;

/** The server's own disks. Restore may explicitly include readable files. */
export function FolderExplorer({open, onOpenChange, api, title, subtitle, chosen, onUse, multiple = true, selectionMode = 'folders', useLabel, testID}: FolderExplorerProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const [path, setPath] = useState(''), [page, setPage] = useState<FilesystemPage>(), [error, setError] = useState<unknown>(),
    [loading, setLoading] = useState(false), [hidden, setHidden] = useState(false), [picked, setPicked] = useState<readonly ExplorerFolder[]>(chosen), [editing, setEditing] = useState(false), [typed, setTyped] = useState(''), [retry, setRetry] = useState(0);

  const chosenRef = useRef(chosen); chosenRef.current = chosen;
  const request = useRef<AbortController | undefined>(undefined), pending = useRef(false);
  useEffect(() => {if (open) {setPicked(chosenRef.current); setPath(''); setError(undefined);}}, [open]);

  useEffect(() => {
    if (!open) return;
    const owned = new AbortController(); request.current = owned; pending.current = true;
    setLoading(true); setError(undefined); setPage(undefined);
    browseServerFilesystem(api, {path: path || undefined, showHidden: hidden, includeFiles: selectionMode === 'file-or-folder', limit: 200}, owned.signal)
      .then(value => {if (!owned.signal.aborted && request.current === owned) {setPage(value); setTyped(value.path);}})
      .catch(reason => {if (!owned.signal.aborted && request.current === owned) setError(reason);})
      .finally(() => {if (!owned.signal.aborted && request.current === owned) {pending.current = false; setLoading(false);}});
    return () => {owned.abort(); if (request.current === owned) request.current = undefined;};
  }, [open, path, hidden, api, retry, selectionMode]);

  const loadMore = () => {
    const owned = request.current, base = page, cursor = base?.nextCursor;
    if (!owned || owned.signal.aborted || pending.current || !base || !cursor) return;
    pending.current = true; setLoading(true); setError(undefined);
    browseServerFilesystem(api, {path: path || undefined, showHidden: hidden, includeFiles: selectionMode === 'file-or-folder', cursor, limit: 200}, owned.signal)
      .then(value => {
        if (owned.signal.aborted || request.current !== owned) return;
        const next = appendExplorerPage(base, value);
        setPage(current => current === base ? next : current);
      })
      .catch(reason => {if (!owned.signal.aborted && request.current === owned) setError(reason);})
      .finally(() => {if (!owned.signal.aborted && request.current === owned) {pending.current = false; setLoading(false);}});
  };

  const folders = useMemo(() => explorerEntries(page, selectionMode), [page, selectionMode]);
  const has = (folder: ExplorerFolder) => picked.some(value => value.path === folder.path);
  const add = (folder: ExplorerFolder) => setPicked(current => toggleExplorerSelection(current, folder, multiple));
  const here = page?.path ? {path: page.path, name: page.path.split(page.separator).filter(Boolean).pop() ?? page.path} : undefined;
  const choose = (next: string) => {setEditing(false); setPath(next);};
  const use = () => {onUse(picked); onOpenChange(false);};

  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={title} description={subtitle} testID={testID}
    detents={[Math.round(theme.width * 1.55) > 760 ? 760 : Math.round(theme.width * 1.55)]} scrollable={false}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button size="small" disabled={!picked.length} onPress={use} style={{flex: 1}}>{useLabel ?? (selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.usePath') : t(picked.length === 1 ? 'next.ownerLibraries.explorer.useFolder' : 'next.ownerLibraries.explorer.useFolders', {count: picked.length}))}</Button></View>}>
    <FlatList data={folders} keyExtractor={entry => entry.path} extraData={picked}
      initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} keyboardShouldPersistTaps="handled"
      contentContainerStyle={{paddingBottom: 8}}
      renderItem={({item: entry, index}) => <ExplorerFolderRow entry={entry} first={index === 0} last={index === folders.length - 1}
        checked={has(entry)} multiple={multiple} onOpen={() => entry.kind === 'directory' ? choose(entry.path) : add(entry)} onCheck={() => add(entry)}/>}
      ListHeaderComponent={<View style={{gap: 12, paddingBottom: 8}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <Button variant="icon" size="xs" icon="i-left" accessibilityLabel={t('next.ownerLibraries.explorer.up')} disabled={!page?.parent || page.parent === page.path} onPress={() => page?.parent && choose(page.parent)}/>
        {editing ? <TextField focusId="owner-libraries-explorer-path" value={typed} onChangeText={setTyped} autoFocus returnKeyType="go" onSubmitEditing={() => {if (typed.trim()) choose(typed.trim());}} containerStyle={{flex: 1}} accessibilityLabel={t('next.ownerLibraries.explorer.path')} showSoftInputOnFocus/>
          : <Focusable accessibilityRole="button" accessibilityLabel={t('next.ownerLibraries.explorer.path')} onPress={() => {setEditing(true); setTyped(page?.path ?? '');}} focusKind="row" focusRadius={10} style={{flex: 1, minHeight: 38, justifyContent: 'center', paddingHorizontal: 10, borderRadius: 10, backgroundColor: theme.colors.raised}}>
            <Text numberOfLines={1} style={[shellType(13.5, 600), {color: theme.colors.text}]}>{page?.path || t('next.ownerLibraries.explorer.places')}</Text>
          </Focusable>}
        <Button variant="icon" size="xs" icon="i-pencil" accessibilityLabel={t('next.ownerLibraries.explorer.typePath')} onPress={() => {setEditing(value => !value); setTyped(page?.path ?? '');}}/>
        <Button variant="icon" size="xs" icon="i-disk" accessibilityLabel={t('next.ownerLibraries.explorer.places')} onPress={() => choose('')}/>
      </View>

      {error !== undefined && <LibraryGroup><LibraryRow icon="i-alert" label={t('next.ownerLibraries.explorer.failed')} help={t('next.ownerLibraries.explorer.failedHelp')} onPress={() => setRetry(value => value + 1)} trailing={<Text style={[shellType(13.5, 700), {color: theme.colors.accentStrong}]}>{t('action.tryAgain')}</Text>} first/></LibraryGroup>}

      {!page?.path && (page?.roots?.length ?? 0) > 0 && <LibraryGroup>
        {page!.roots.map((root, index) => <LibraryRow key={root.path} first={index === 0} icon="i-disk" label={root.name || root.path} help={root.description || root.path} onPress={() => choose(root.path)}/>)}
      </LibraryGroup>}

      {page?.path && here && <LibraryGroup>
        <LibraryRow first icon="i-folder" label={t('next.ownerLibraries.explorer.addThisFolder')} help={page.path}
          trailing={<ExplorerCheck checked={has(here)} onPress={() => add(here)} label={t('next.ownerLibraries.explorer.addThisFolder')}/>}/>
      </LibraryGroup>}

      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 2}}>
        <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, flex: 1}]}>{selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.filesAndFolders') : t('next.ownerLibraries.explorer.folders')}</Text>
        <Text style={[shellType(12.5, 600), {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.explorer.showHidden')}</Text>
        <Switch testID={testID ? `${testID}-hidden` : undefined} checked={hidden} accessibilityLabel={t('next.ownerLibraries.explorer.showHidden')} onCheckedChange={setHidden}/>
      </View>

      </View>}
      ListEmptyComponent={page || loading ? <LibraryGroup><LibraryRow first label={loading ? t('next.ownerLibraries.explorer.reading') : selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.noFilesOrFolders') : t('next.ownerLibraries.explorer.noFolders')}/></LibraryGroup> : null}
      ListFooterComponent={<View style={{gap: 12, paddingTop: 8}}>
        {page?.nextCursor ? <LibraryGroup><LibraryRow first label={t(loading ? 'next.ownerLibraries.explorer.reading' : 'next.ownerLibraries.explorer.more')} onPress={loading ? undefined : loadMore}/></LibraryGroup> : null}

      {page?.truncated && <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, paddingHorizontal: 4}]}>{t('next.ownerLibraries.explorer.truncated')}</Text>}

      {picked.length > 0 && <View style={{gap: 8}}>
        <Text style={[shellType(12.5, 700), {color: theme.colors.textTertiary, paddingHorizontal: 4}]}>{t('next.ownerLibraries.explorer.chosen')}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{picked.map(folder => <Focusable key={folder.path} accessibilityRole="button" accessibilityLabel={`${folder.name}, ${t('next.ownerLibraries.explorer.removeChosen')}`} onPress={() => setPicked(current => current.filter(value => value.path !== folder.path))} focusKind="chip" focusRadius={14}
          style={{flexDirection: 'row', alignItems: 'center', gap: 6, maxWidth: '100%', paddingVertical: 6, paddingHorizontal: 10, borderRadius: 14, backgroundColor: alpha(theme.colors.text, .08)}}>
          <Icon name="i-folder" size={14}/><Text numberOfLines={1} style={[shellType(12.5, 600), {color: theme.colors.text, flexShrink: 1}]}>{folder.path}</Text><Icon name="i-x" size={12}/>
        </Focusable>)}</View>
      </View>}
      {selectionMode === 'folders' && <Text style={[shellType(12, 500, 17), {color: theme.colors.textTertiary, paddingHorizontal: 4}]}>{t('next.ownerLibraries.explorer.disksOnly')}</Text>}
    </View>}/>
  </BottomSheet>;
}

function ExplorerCheck({checked, onPress, label}: {checked: boolean; onPress: () => void; label: string}) {
  const theme = useTheme();
  return <Focusable accessibilityRole="checkbox" accessibilityLabel={label} accessibilityState={{checked}} onPress={onPress} focusKind="chip" focusRadius={6} style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: checked ? theme.colors.accentStrong : 'transparent', alignItems: 'center', justifyContent: 'center'}}>
    {checked && <Icon name="i-check" size={14} color={theme.colors.ink}/>}
  </Focusable>;
}

function ExplorerFolderRow({entry, first, last, checked, multiple, onOpen, onCheck}: {entry: FilesystemEntry; first: boolean; last: boolean; checked: boolean; multiple: boolean; onOpen: () => void; onCheck: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  const tag = entry.hidden ? t('next.ownerLibraries.explorer.hidden') : entry.symlink ? t('next.ownerLibraries.explorer.link') : !entry.readable ? t('next.ownerLibraries.explorer.noAccess') : undefined;
  return <View style={{flexDirection: 'row', alignItems: 'center', minHeight: 50, paddingRight: 8, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft, backgroundColor: theme.colors.raised, borderTopLeftRadius: first ? 14 : 0, borderTopRightRadius: first ? 14 : 0, borderBottomLeftRadius: last ? 14 : 0, borderBottomRightRadius: last ? 14 : 0, overflow: 'hidden', opacity: entry.readable ? 1 : .55}}>
    {(multiple || entry.kind === 'file') && entry.readable ? <View style={{paddingLeft: 12}}><ExplorerCheck checked={checked} label={entry.name} onPress={onCheck}/></View>
      : <View style={{width: 12}}/>}
    <Focusable accessibilityRole={entry.readable ? 'button' : 'text'} accessibilityLabel={entry.name} accessibilityHint={tag} disabled={!entry.readable} onPress={entry.readable ? onOpen : undefined} focusKind="row" focusRadius={8}
      style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: 10, paddingLeft: 10, paddingRight: 6, minHeight: 50}}>
      <Icon name={!entry.readable ? 'i-lock' : entry.kind === 'file' ? 'i-note' : 'i-folder'} size={20} color={theme.colors.textSecondary}/>
      <Text numberOfLines={1} style={[shellType(15, 500), {color: theme.colors.text, flex: 1}]}>{entry.name}{tag ? <Text style={[shellType(11.5, 700), {color: theme.colors.textTertiary}]}>{`  ${tag}`}</Text> : null}</Text>
      {entry.readable && entry.kind === 'directory' && <Icon name="i-right" size={16} color={theme.colors.lineStrong}/>}
    </Focusable>
  </View>;
}
