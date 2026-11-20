import React from 'react';
import {FlatList, Text, View} from 'react-native';
import type {FilesystemEntry} from '../../../../../../packages/client-core/src/administration';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../controls/Button';
import {Focusable} from '../../controls/Focusable';
import {Switch} from '../../controls/Switch';
import {TextField} from '../../controls/TextField';
import {BottomSheet} from '../../overlays/BottomSheet';
import {Icon} from '../../Icon';
import {useTheme} from '../../theme';
import {alpha} from '../../shell/visual';
import {SettingsGroup, SettingsRow} from '../../shell';
import {useFolderExplorer, type FolderExplorerProps} from '../../../pages/shared/settings/useFolderExplorer';
export type {ExplorerFolder} from '../../../screens/settings-live/owner-libraries/folder-explorer-model';

export function FolderExplorer(props: FolderExplorerProps) {
  const {open, onOpenChange, title, subtitle, multiple = true, selectionMode = 'folders', useLabel, testID} = props;
  const theme = useTheme(), t = defaultI18n.t;
  const {page, error, loading, hidden, setHidden, picked, setPicked, editing, setEditing, typed, setTyped,
    setRetry, folders, has, add, here, choose, use, loadMore} = useFolderExplorer(props);
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={title} description={subtitle} testID={testID}
    detents={[Math.round(theme.width * 1.55) > 760 ? 760 : Math.round(theme.width * 1.55)]} scrollable={false}
    footer={<><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button size="small" disabled={!picked.length} onPress={use} align="stretch">{useLabel ?? (selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.usePath') : t(picked.length === 1 ? 'next.ownerLibraries.explorer.useFolder' : 'next.ownerLibraries.explorer.useFolders', {count: picked.length}))}</Button></>}>
    <FlatList data={folders} keyExtractor={entry => entry.path} extraData={picked}
      initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} keyboardShouldPersistTaps="handled"
      contentContainerStyle={{paddingBottom: 8}}
      renderItem={({item: entry, index}) => <ExplorerFolderRow entry={entry} first={index === 0} last={index === folders.length - 1}
        checked={has(entry)} multiple={multiple} onOpen={() => entry.kind === 'directory' ? choose(entry.path) : add(entry)} onCheck={() => add(entry)}/>}
      ListHeaderComponent={<View style={{gap: 12, paddingBottom: 8}}>
      {/* The sheet draws its body edge to edge and each row pads itself; the lines between rows take the rows' 14 pt. */}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 14}}>
        <Button variant="icon" size="xs" icon="i-left" accessibilityLabel={t('next.ownerLibraries.explorer.up')} disabled={!page?.parent || page.parent === page.path} onPress={() => page?.parent && choose(page.parent)}/>
        {editing ? <TextField focusId="owner-libraries-explorer-path" value={typed} onChangeText={setTyped} autoFocus returnKeyType="go" onSubmitEditing={() => {if (typed.trim()) choose(typed.trim());}} containerStyle={{flex: 1}} accessibilityLabel={t('next.ownerLibraries.explorer.path')} showSoftInputOnFocus/>
          : <Focusable accessibilityRole="button" accessibilityLabel={t('next.ownerLibraries.explorer.path')} onPress={() => {setEditing(true); setTyped(page?.path ?? '');}} focusKind="row" focusRadius={10} style={{flex: 1, minHeight: 38, justifyContent: 'center', paddingHorizontal: 10, borderRadius: 10, backgroundColor: theme.colors.raised}}>
            <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{page?.path || t('next.ownerLibraries.explorer.places')}</Text>
          </Focusable>}
        <Button variant="icon" size="xs" icon="i-pencil" accessibilityLabel={t('next.ownerLibraries.explorer.typePath')} onPress={() => {setEditing(value => !value); setTyped(page?.path ?? '');}}/>
        <Button variant="icon" size="xs" icon="i-disk" accessibilityLabel={t('next.ownerLibraries.explorer.places')} onPress={() => choose('')}/>
      </View>

      {error !== undefined && <SettingsGroup><SettingsRow icon="i-alert" label={t('next.ownerLibraries.explorer.failed')} description={t('next.ownerLibraries.explorer.failedHelp')} control={<Button variant="glass" size="xs" onPress={() => setRetry(value => value + 1)}>{t('action.tryAgain')}</Button>} first/></SettingsGroup>}

      {!page?.path && (page?.roots?.length ?? 0) > 0 && <SettingsGroup>
        {page!.roots.map((root, index) => <SettingsRow navigates key={root.path} first={index === 0} icon="i-disk" label={root.name || root.path} description={root.description || root.path} onPress={() => choose(root.path)}/>)}
      </SettingsGroup>}

      {page?.path && here && <SettingsGroup>
        <SettingsRow first icon="i-folder" label={t('next.ownerLibraries.explorer.addThisFolder')} description={page.path}
          control={<ExplorerCheck checked={has(here)} onPress={() => add(here)} label={t('next.ownerLibraries.explorer.addThisFolder')}/>}/>
      </SettingsGroup>}

      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 14}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.textTertiary, flex: 1}]}>{selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.filesAndFolders') : t('next.ownerLibraries.explorer.folders')}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.explorer.showHidden')}</Text>
        <Switch testID={testID ? `${testID}-hidden` : undefined} checked={hidden} accessibilityLabel={t('next.ownerLibraries.explorer.showHidden')} onCheckedChange={setHidden}/>
      </View>

      </View>}
      ListEmptyComponent={page || loading ? <SettingsGroup><SettingsRow first label={loading ? t('next.ownerLibraries.explorer.reading') : selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.noFilesOrFolders') : t('next.ownerLibraries.explorer.noFolders')}/></SettingsGroup> : null}
      ListFooterComponent={<View style={{gap: 12, paddingTop: 8}}>
        {page?.nextCursor ? <SettingsGroup><SettingsRow navigates first label={t(loading ? 'next.ownerLibraries.explorer.reading' : 'next.ownerLibraries.explorer.more')} onPress={loading ? undefined : loadMore}/></SettingsGroup> : null}

      {page?.truncated && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, paddingHorizontal: 14}]}>{t('next.ownerLibraries.explorer.truncated')}</Text>}

      {picked.length > 0 && <View style={{gap: 8, paddingHorizontal: 14}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.explorer.chosen')}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{picked.map(folder => <Focusable key={folder.path} accessibilityRole="button" accessibilityLabel={`${folder.name}, ${t('next.ownerLibraries.explorer.removeChosen')}`} onPress={() => setPicked(current => current.filter(value => value.path !== folder.path))} focusKind="chip" focusRadius={14}
          style={{flexDirection: 'row', alignItems: 'center', gap: 6, maxWidth: '100%', paddingVertical: 6, paddingHorizontal: 10, borderRadius: 14, backgroundColor: alpha(theme.colors.text, .08)}}>
          <Icon name="i-folder" size={14}/><Text style={[theme.type.rowHelp, {color: theme.colors.text, flexShrink: 1}]}>{folder.path}</Text><Icon name="i-x" size={12}/>
        </Focusable>)}</View>
      </View>}
      {selectionMode === 'folders' && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, paddingHorizontal: 14}]}>{t('next.ownerLibraries.explorer.disksOnly')}</Text>}
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
      <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{entry.name}{tag ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{`  ${tag}`}</Text> : null}</Text>
      {entry.readable && entry.kind === 'directory' && <Icon name="i-right" size={16} color={theme.colors.lineStrong}/>}
    </Focusable>
  </View>;
}
