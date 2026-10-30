import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {InventorySource, SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {TextField} from '../../../kit/controls/TextField';
import {useTheme} from '../../../kit/theme';
import {shellType} from '../../../kit/shell/visual';
import {LibraryGroup, LibraryRow, SectionTitle} from './controls';
import {FolderSourcePicker, type LibraryFolderChoice} from './FolderSourcePicker';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {ownerLibraryIntervals} from './presentation';
import {FormScrollView} from '../../../platform/keyboard';

export type FolderInput = Readonly<{path: string; name: string; classification: 'local' | 'network'; followSymlinks: boolean; intervalSeconds: number}>;

/** One folder's own form: path, name, where it lives, links and how often it is checked (frame 21a). */
export function AddFolderPage({api, serverName, source, expectedRevision, onSave, onCancel, busy, error, ports, runtime}: {
  api: LibraryContentApi; serverName: string; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime; source?: InventorySource; expectedRevision?: number;
  onSave: (input: SourceSettings) => void; onCancel: () => void; busy: boolean; error?: unknown;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [path, setPath] = useState(source?.path ?? ''), [name, setName] = useState(source?.name ?? ''),
    [classification, setClassification] = useState<'local' | 'network'>(source?.classification === 'network' ? 'network' : 'local'),
    [follow, setFollow] = useState(source?.followSymlinks ?? false), [interval, setInterval] = useState(source?.intervalSeconds ?? 3600),
    [exploring, setExploring] = useState(false);
  const [choice, setChoice] = useState<LibraryFolderChoice>({folders: source ? [{path: source.path, name: source.name || source.path}] : []});
  const valid = path.trim().length > 0 && expectedRevision !== undefined;
  return <View style={{flex: 1}}>
    <FormScrollView contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 24}} keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive">
      <SectionTitle>{t('next.ownerLibraries.addFolder.path')}</SectionTitle>
      <LibraryGroup><LibraryRow first icon="i-folder" label={path || t('server.chooseFolder')} help={path ? undefined : t('next.ownerLibraries.addFolder.help')} onPress={() => setExploring(true)}/></LibraryGroup>

      <SectionTitle>{t('next.ownerLibraries.addFolder.name')}</SectionTitle>
      <TextField focusId="owner-libraries-folder-name" value={name} onChangeText={setName} maxLength={100} placeholder={t('next.ownerLibraries.addFolder.namePlaceholder')} accessibilityLabel={t('next.ownerLibraries.addFolder.name')}/>

      <SectionTitle>{t('next.ownerLibraries.addFolder.location')}</SectionTitle>
      <LibraryGroup><LibraryRow first label={t('next.ownerLibraries.addFolder.location')}
        trailing={<Select testID="owner-libraries-folder-location" accessibilityLabel={t('next.ownerLibraries.addFolder.location')} value={classification} options={[
          {value: 'local', label: t('web.libraries.localDisk'), description: t('next.ownerLibraries.addFolder.localHint')},
          {value: 'network', label: t('web.libraries.networkShare'), description: t('next.ownerLibraries.addFolder.networkHint')}]} onValueChange={value => setClassification(value === 'network' ? 'network' : 'local')} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/></LibraryGroup>

      <SectionTitle>{t('next.ownerLibraries.addFolder.links')}</SectionTitle>
      <LibraryGroup><LibraryRow first label={t('next.ownerLibraries.addFolder.follow')} help={t('next.ownerLibraries.addFolder.followHelp')}
        trailing={<Switch testID="owner-libraries-folder-follow" checked={follow} accessibilityLabel={t('next.ownerLibraries.addFolder.follow')} onCheckedChange={setFollow}/>}/></LibraryGroup>

      <SectionTitle>{t('next.ownerLibraries.addFolder.rescan')}</SectionTitle>
      <LibraryGroup><LibraryRow first label={t('next.ownerLibraries.addFolder.rescan')}
        trailing={<Select testID="owner-libraries-folder-interval" accessibilityLabel={t('next.ownerLibraries.addFolder.rescan')} value={String(interval)} options={ownerLibraryIntervals.map(value => ({value: String(value.seconds), label: t(value.label, value.values)}))} onValueChange={value => setInterval(Number(value))} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/></LibraryGroup>

      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record, marginTop: 16}]}>{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</Text>}
    </FormScrollView>
    <View style={{flexDirection: 'row', gap: 10, paddingHorizontal: 20, paddingVertical: 12, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft, backgroundColor: theme.colors.ink}}>
      <Button variant="glass" size="small" disabled={busy} onPress={onCancel}>{t('action.cancel')}</Button>
      <Button size="small" loading={busy} disabled={!valid || busy} onPress={() => onSave({name: name.trim(), path: path.trim(), classification, followSymlinks: follow, intervalSeconds: interval, ...(choice.source ? {expectedRemoteSource: {sourceId: choice.source.id, generation: choice.source.generation}} : {}), expectedRevision: expectedRevision ?? 0, acceptReplacement: false})} style={{flex: 1}}>{t('next.ownerLibraries.save')}</Button>
    </View>
    <FolderSourcePicker open={exploring} onOpenChange={setExploring} api={api} serverName={serverName} ports={ports} runtime={runtime} multiple={false}
      chosen={choice} onUse={next => {const folder = next.folders[0]; if (folder) {setChoice(next); setPath(folder.path); if (!name) setName(folder.name); if (next.source) {setClassification('local'); setFollow(false);}}}}/>

  </View>;
}
