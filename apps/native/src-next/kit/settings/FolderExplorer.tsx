import React from 'react';
import {FlatList, Text, View} from 'react-native';
import type {FilesystemEntry} from '../../../../../packages/client-core/src/administration';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button, Focusable, Switch, TextField} from '../controls';
import {BottomSheet} from '../overlays';
import {OverlayFootRow} from '../overlays/surface';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {alpha} from '../shell/visual';
import {SettingsGroup, SettingsRow} from '../shell';
import type {ExplorerFolder, ExplorerSelectionMode} from '../../screens/settings-live/owner-libraries/folder-explorer-model';
import {useFolderExplorer} from '../../pages/shared/settings/useFolderExplorer';

export type {ExplorerFolder} from '../../screens/settings-live/owner-libraries/folder-explorer-model';

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

/** The server's own disks, in a tall sheet (Add folder, a backup's folder, a recording folder, Restore from file): Up,
 * the path (tap to type one), Places; then the places or "Add this folder", the folders here with Show hidden, Show more,
 * what's chosen, and Cancel with Use at the foot. Restore may include readable files. */
export function FolderExplorer({open, onOpenChange, api, title, subtitle, chosen, onUse, multiple = true, selectionMode = 'folders', useLabel, testID}: FolderExplorerProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const view = useFolderExplorer({open, api, chosen, multiple, selectionMode}), {page, picked, folders} = view;
  const inset = {paddingHorizontal: theme.space.sm - 2};
  const quiet = [theme.type.captionStrong, {color: theme.colors.textTertiary}];
  const use = () => {onUse(picked); onOpenChange(false);};
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={title} description={subtitle} testID={testID}
    detents={[Math.min(760, Math.round(theme.width * 1.55))]} scrollable={false}
    footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button disabled={!picked.length} onPress={use}>{useLabel ?? (selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.usePath')
        : t(picked.length === 1 ? 'next.ownerLibraries.explorer.useFolder' : 'next.ownerLibraries.explorer.useFolders', {count: picked.length}))}</Button></OverlayFootRow>}>
    <FlatList data={folders} keyExtractor={entry => entry.path} extraData={picked}
      initialNumToRender={16} maxToRenderPerBatch={16} windowSize={7} keyboardShouldPersistTaps="handled"
      contentContainerStyle={{paddingBottom: theme.space.xs}}
      renderItem={({item: entry, index}) => <ExplorerFolderRow entry={entry} first={index === 0} last={index === folders.length - 1}
        checked={view.has(entry)} multiple={multiple} onOpen={() => entry.kind === 'directory' ? view.choose(entry.path) : view.add(entry)} onCheck={() => view.add(entry)}/>}
      ListHeaderComponent={<View style={{gap: theme.space.sm - 4, paddingBottom: theme.space.xs}}>
        {/* The sheet draws its body edge to edge and each row pads itself; the lines between rows take the rows' 14 pt. */}
        <View style={[{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}, inset]}>
          <Button variant="icon" size="xs" icon="i-left" accessibilityLabel={t('next.ownerLibraries.explorer.up')} disabled={!page?.parent || page.parent === page.path} onPress={() => page?.parent && view.choose(page.parent)}/>
          {view.editing ? <TextField focusId="owner-libraries-explorer-path" value={view.typed} onChangeText={view.setTyped} autoFocus returnKeyType="go"
              onSubmitEditing={() => {if (view.typed.trim()) view.choose(view.typed.trim());}} containerStyle={{flex: 1}} accessibilityLabel={t('next.ownerLibraries.explorer.path')} showSoftInputOnFocus/>
            : <Focusable accessibilityRole="button" accessibilityLabel={t('next.ownerLibraries.explorer.path')} onPress={view.startEditing} focusKind="row" focusRadius={10}
              style={{flex: 1, minHeight: 38, justifyContent: 'center', paddingHorizontal: theme.space.controlGap, borderRadius: 10, backgroundColor: theme.colors.raised}}>
              <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{page?.path || t('next.ownerLibraries.explorer.places')}</Text>
            </Focusable>}
          <Button variant="icon" size="xs" icon="i-pencil" accessibilityLabel={t('next.ownerLibraries.explorer.typePath')} onPress={view.toggleEditing}/>
          <Button variant="icon" size="xs" icon="i-disk" accessibilityLabel={t('next.ownerLibraries.explorer.places')} onPress={() => view.choose('')}/>
        </View>
        {view.error !== undefined && <SettingsGroup><SettingsRow icon="i-alert" label={t('next.ownerLibraries.explorer.failed')} description={t('next.ownerLibraries.explorer.failedHelp')}
          control={<Button variant="glass" size="xs" onPress={view.retry}>{t('action.tryAgain')}</Button>}/></SettingsGroup>}
        {!page?.path && (page?.roots?.length ?? 0) > 0 && <SettingsGroup>
          {page!.roots.map((root, index) => <SettingsRow navigates key={root.path} first={index === 0} icon="i-disk" label={root.name || root.path} description={root.description || root.path} onPress={() => view.choose(root.path)}/>)}
        </SettingsGroup>}
        {page?.path && view.here && <SettingsGroup>
          <SettingsRow first icon="i-folder" label={t('next.ownerLibraries.explorer.addThisFolder')} description={page.path}
            control={<ExplorerCheck checked={view.has(view.here)} onPress={() => view.add(view.here!)} label={t('next.ownerLibraries.explorer.addThisFolder')}/>}/>
        </SettingsGroup>}
        <View style={[{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}, inset]}>
          <Text style={[...quiet, {flex: 1}]}>{selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.filesAndFolders') : t('next.ownerLibraries.explorer.folders')}</Text>
          <Text style={quiet}>{t('next.ownerLibraries.explorer.showHidden')}</Text>
          <Switch testID={testID ? `${testID}-hidden` : undefined} checked={view.hidden} accessibilityLabel={t('next.ownerLibraries.explorer.showHidden')} onCheckedChange={view.setHidden}/>
        </View>
      </View>}
      ListEmptyComponent={page || view.loading ? <SettingsGroup><SettingsRow first label={view.loading ? t('next.ownerLibraries.explorer.reading')
        : selectionMode === 'file-or-folder' ? t('next.ownerLibraries.explorer.noFilesOrFolders') : t('next.ownerLibraries.explorer.noFolders')}/></SettingsGroup> : null}
      ListFooterComponent={<View style={{gap: theme.space.sm - 4, paddingTop: theme.space.xs}}>
        {page?.nextCursor ? <SettingsGroup><SettingsRow navigates first label={t(view.loading ? 'next.ownerLibraries.explorer.reading' : 'next.ownerLibraries.explorer.more')} onPress={view.loading ? undefined : view.loadMore}/></SettingsGroup> : null}
        {page?.truncated && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}, inset]}>{t('next.ownerLibraries.explorer.truncated')}</Text>}
        {picked.length > 0 && <View style={[{gap: theme.space.xs}, inset]}>
          <Text style={quiet}>{t('next.ownerLibraries.explorer.chosen')}</Text>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>{picked.map(folder => <Focusable key={folder.path} accessibilityRole="button"
            accessibilityLabel={`${folder.name}, ${t('next.ownerLibraries.explorer.removeChosen')}`} onPress={() => view.unpick(folder.path)} focusKind="chip" focusRadius={14}
            style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, maxWidth: '100%', paddingVertical: theme.space.tight, paddingHorizontal: theme.space.controlGap, borderRadius: 14, backgroundColor: alpha(theme.colors.text, .08)}}>
            <Icon name="i-folder" size={14}/><Text style={[theme.type.captionStrong, {color: theme.colors.text, flexShrink: 1}]}>{folder.path}</Text><Icon name="i-x" size={12}/>
          </Focusable>)}</View>
        </View>}
        {selectionMode === 'folders' && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}, inset]}>{t('next.ownerLibraries.explorer.disksOnly')}</Text>}
      </View>}/>
  </BottomSheet>;
}

function ExplorerCheck({checked, onPress, label}: {checked: boolean; onPress: () => void; label: string}) {
  const theme = useTheme();
  return <Focusable accessibilityRole="checkbox" accessibilityLabel={label} accessibilityState={{checked}} onPress={onPress} focusKind="chip" focusRadius={6}
    style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: checked ? theme.colors.accentStrong : 'transparent', alignItems: 'center', justifyContent: 'center'}}>
    {checked && <Icon name="i-check" size={14} color={theme.colors.ink}/>}
  </Focusable>;
}

function ExplorerFolderRow({entry, first, last, checked, multiple, onOpen, onCheck}: {entry: FilesystemEntry; first: boolean; last: boolean; checked: boolean; multiple: boolean; onOpen: () => void; onCheck: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  const tag = entry.hidden ? t('next.ownerLibraries.explorer.hidden') : entry.symlink ? t('next.ownerLibraries.explorer.link') : !entry.readable ? t('next.ownerLibraries.explorer.noAccess') : undefined;
  return <View style={{flexDirection: 'row', alignItems: 'center', minHeight: 50, paddingRight: theme.space.xs, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft, backgroundColor: theme.colors.raised,
    borderTopLeftRadius: first ? 14 : 0, borderTopRightRadius: first ? 14 : 0, borderBottomLeftRadius: last ? 14 : 0, borderBottomRightRadius: last ? 14 : 0, overflow: 'hidden', opacity: entry.readable ? 1 : .55}}>
    {(multiple || entry.kind === 'file') && entry.readable ? <View style={{paddingLeft: theme.space.sm - 4}}><ExplorerCheck checked={checked} label={entry.name} onPress={onCheck}/></View>
      : <View style={{width: theme.space.sm - 4}}/>}
    <Focusable accessibilityRole={entry.readable ? 'button' : 'text'} accessibilityLabel={entry.name} accessibilityHint={tag} disabled={!entry.readable} onPress={entry.readable ? onOpen : undefined} focusKind="row" focusRadius={8}
      style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, paddingLeft: theme.space.controlGap, paddingRight: theme.space.tight, minHeight: 50}}>
      <Icon name={!entry.readable ? 'i-lock' : entry.kind === 'file' ? 'i-note' : 'i-folder'} size={20} color={theme.colors.textSecondary}/>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{entry.name}{tag ? <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{`  ${tag}`}</Text> : null}</Text>
      {entry.readable && entry.kind === 'directory' && <Icon name="i-right" size={16} color={theme.colors.lineStrong}/>}
    </Focusable>
  </View>;
}
