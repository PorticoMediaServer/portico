import React from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {languageName} from '../../../../../../packages/client-core/src/presentation/language-list';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {LibraryKindTiles} from '../../../kit/settings/libraries/LibraryKindTiles';
import {MetadataAgentChoice} from '../../../kit/settings/libraries/MetadataAgentChoice';
import {SettingsRow} from '../../../kit/shell';
import {FolderSourcePicker} from '../../../kit/settings/libraries/FolderSourcePicker';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useAddOwnerLibrary, type AddLibraryInput} from './useAddOwnerLibrary';

export function AddLibraryPage({api, serverName, onCreate, onCancel, busy, error, firstRun = false, ports, runtime}: {
  api: LibraryContentApi; serverName: string; onCreate: (input: AddLibraryInput) => void; onCancel: () => void; busy: boolean;
  error?: unknown; firstRun?: boolean; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime;
}) {
  const form = useAddOwnerLibrary(api, onCreate), {t, agents, chosenAgent, folders, choice} = form;
  const formError = error ?? form.metadataError;
  return <>
    <SettingsPageFrame title={t('web.empty.addLibrary')} intro={t('web.libraries.addLede')} hideHeader={firstRun}
      back={firstRun ? undefined : {label: t('settings.server.libraries'), onPress: onCancel, disabled: busy}}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" size="small" disabled={busy} onPress={onCancel}>{t(firstRun ? 'web.setup.skip' : 'action.cancel')}</Button>
        <Button size="small" labelLines={2} loading={busy} disabled={!form.valid || busy} onPress={form.save}>
          {t(firstRun ? 'web.setup.addLibraryScan' : 'web.empty.addLibrary')}
        </Button>
      </OverlayFootRow>}>
      <SectionCard label={t('web.libraries.type')}>
        <PanelBlock><LibraryKindTiles kind={form.kind} onChange={form.chooseKind} disabled={busy} firstRun={firstRun}/></PanelBlock>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.nameLabel')}>
        <PanelBlock><TextField focusId="owner-libraries-add-name" value={form.name} onChangeText={form.enterName}
          maxLength={100} accessibilityLabel={t('next.ownerLibraries.nameLabel')}/></PanelBlock>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.folders')} help={!folders.length ? t('next.ownerLibraries.addLibrary.foldersHelp') : undefined}>
        {choice.source && <SettingsRow first icon="i-disk" label={choice.source.name}/>}
        {folders.map((folder, index) => <SettingsRow key={folder.path} first={index === 0 && !choice.source} icon="i-folder" label={folder.name} description={folder.path}
          control={<Button variant="icon" size="xs" icon="i-x" accessibilityLabel={t('next.ownerLibraries.explorer.removeChosen')} onPress={() => form.remove(folder.path)}/>}/>)}
        <SettingsRow navigates first={!folders.length && !choice.source} icon="i-plus" label={t('next.ownerLibraries.explorer.choose')} onPress={() => form.setExploring(true)}/>
      </SectionCard>
      {agents.length > 0 && <SectionCard label={t('next.ownerLibraries.metadataSource')}>
        {agents.map((option, index) => <MetadataAgentChoice key={option.id} option={option} first={index === 0} checked={option.id === form.agent}
          onPress={() => form.chooseAgent(option)}/>)}
      </SectionCard>}
      {form.language !== undefined && chosenAgent && chosenAgent.languages.length > 0 && <SectionCard label={t('next.ownerLibraries.addLibrary.language')}>
        <SettingsRow first label={t('next.ownerLibraries.addLibrary.language')} description={t('next.ownerLibraries.addLibrary.languageHelp')}
          control={<Select testID="owner-libraries-add-language" accessibilityLabel={t('next.ownerLibraries.addLibrary.language')} value={form.language}
            options={chosenAgent.languages.map(code => ({value: code, label: languageName(code, key => defaultI18n.has(key) ? t(key) : undefined)}))}
            onValueChange={form.setLanguage}/>}/>
      </SectionCard>}
      {formError !== undefined && <PanelBlock><FormError placement="field">
        {presentError(formError, 'library', {operation: error !== undefined ? 'save' : 'load'}).body}
      </FormError></PanelBlock>}
    </SettingsPageFrame>
    <FolderSourcePicker open={form.exploring} onOpenChange={form.setExploring} api={api} serverName={serverName}
      ports={firstRun ? undefined : ports} runtime={firstRun ? undefined : runtime} chosen={choice} onUse={form.setChoice}/>
  </>;
}
