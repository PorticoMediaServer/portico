import React from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {InventorySource, SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError, Select, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsRow} from '../../../kit/shell';
import {FolderSourcePicker} from '../../../kit/settings/libraries/FolderSourcePicker';
import {ownerLibraryIntervals} from '../../../screens/settings-live/owner-libraries/presentation';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useAddOwnerFolder} from './useAddOwnerFolder';

export function AddFolderPage({api, serverName, source, expectedRevision, onSave, onCancel, busy, error, ports, runtime}: {
  api: LibraryContentApi; serverName: string; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime; source?: InventorySource; expectedRevision?: number;
  onSave: (input: SourceSettings) => void; onCancel: () => void; busy: boolean; error?: unknown;
}) {
  const t = defaultI18n.t, form = useAddOwnerFolder(source, expectedRevision, onSave);
  return <>
    <SettingsPageFrame title={t(source ? 'web.libraries.editFolder' : 'web.libraries.addFolder')}
      back={{label: t('next.ownerLibraries.folders'), onPress: onCancel, disabled: busy}}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" size="small" disabled={busy} onPress={onCancel}>{t('action.cancel')}</Button>
        <Button size="small" loading={busy} disabled={!form.valid || busy} onPress={form.save}>{t('next.ownerLibraries.save')}</Button>
      </OverlayFootRow>}>
      <SectionCard label={t('next.ownerLibraries.addFolder.path')}>
        <SettingsRow navigates first icon="i-folder" label={form.path || t('server.chooseFolder')}
          description={form.path ? undefined : t('next.ownerLibraries.addFolder.help')} onPress={() => form.setExploring(true)}/>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.addFolder.name')}>
        <PanelBlock><TextField focusId="owner-libraries-folder-name" value={form.name} onChangeText={form.setName} maxLength={100}
          placeholder={t('next.ownerLibraries.addFolder.namePlaceholder')} accessibilityLabel={t('next.ownerLibraries.addFolder.name')}/></PanelBlock>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.addFolder.location')}>
        <SettingsRow first label={t('next.ownerLibraries.addFolder.location')}
          control={<Select testID="owner-libraries-folder-location" accessibilityLabel={t('next.ownerLibraries.addFolder.location')} value={form.classification}
            options={[
              {value: 'local', label: t('web.libraries.localDisk'), description: t('next.ownerLibraries.addFolder.localHint')},
              {value: 'network', label: t('web.libraries.networkShare'), description: t('next.ownerLibraries.addFolder.networkHint')},
            ]} onValueChange={value => form.setClassification(value === 'network' ? 'network' : 'local')}/>}/>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.addFolder.links')}>
        <SettingsRow first label={t('next.ownerLibraries.addFolder.follow')} description={t('next.ownerLibraries.addFolder.followHelp')}
          control={<Switch testID="owner-libraries-folder-follow" checked={form.follow} accessibilityLabel={t('next.ownerLibraries.addFolder.follow')}
            onCheckedChange={form.setFollow}/>}/>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.addFolder.rescan')}>
        <SettingsRow first label={t('next.ownerLibraries.addFolder.rescan')}
          control={<Select testID="owner-libraries-folder-interval" accessibilityLabel={t('next.ownerLibraries.addFolder.rescan')} value={String(form.interval)}
            options={ownerLibraryIntervals.map(value => ({value: String(value.seconds), label: t(value.label, value.values)}))}
            onValueChange={value => form.setInterval(Number(value))}/>}/>
      </SectionCard>
      {error !== undefined && <PanelBlock><FormError placement="field">
        {error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}
      </FormError></PanelBlock>}
    </SettingsPageFrame>
    <FolderSourcePicker open={form.exploring} onOpenChange={form.setExploring} api={api} serverName={serverName} ports={ports} runtime={runtime}
      multiple={false} chosen={form.choice} onUse={form.choose}/>
  </>;
}
