import React from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource, SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import type {ServerRequester} from '../../../../../../packages/client-core/src/query/server-api';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {languageName} from '../../../../../../packages/client-core/src/presentation/language-list';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button, FormError, Select, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {LibraryKindTiles, RadioRow, ShortNumberField, TrashItemLine} from '../../../kit/settings/LibraryParts';
import {StorageActions as PageActions, StorageNote as PageNote, StorageSheetBody as SheetBody} from '../../../kit/settings/StorageParts';
import {SettingsRow} from '../../../kit/shell';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {FolderSourcePicker} from '../../../screens/settings-live/owner-libraries/FolderSourcePicker';
import {ownerLibraryIntervals, ownerLibraryKind, ownerLibraryKinds} from '../../../screens/settings-live/owner-libraries/presentation';
import {TRASH_RULES, useAddFolder, useAddLibrary, useLibraryTrash, type AddLibraryInput} from '../../shared/settings/useLibraryForms';

export type {AddLibraryInput};
type Translate = typeof defaultI18n.t;
const t = defaultI18n.t;

/** Settings › Server › Libraries › Add library (frame 17), as one page: its kind, its name, its folders, the metadata source
 * and language, then Cancel and Add at the foot. First-run setup uses the same page (Skip, and "Add library and scan"). */
export function AddLibraryPage({api, serverName, onCreate, onCancel, busy, error, firstRun = false, ports, runtime}: {api: LibraryContentApi; serverName: string;
  onCreate: (input: AddLibraryInput) => void; onCancel: () => void; busy: boolean; error?: unknown; firstRun?: boolean; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime}) {
  const form = useAddLibrary(api, error), {chosenAgent, language} = form;
  return <>
    <SettingsPageFrame title={t(firstRun ? 'web.setup.addLibraryScan' : 'web.empty.addLibrary')} intro={t('web.libraries.addLede')} testID="owner-libraries-add"
      back={firstRun ? undefined : {label: t('settings.server.libraries'), onPress: onCancel}}>
      <SectionCard plain label={t('web.libraries.type')}>
        <LibraryKindTiles value={form.kind} disabled={busy} onChange={kind => form.chooseKind(kind as ManagedLibrary['kind'])} description={t(ownerLibraryKind(form.kind).description)}
          options={ownerLibraryKinds.filter(option => !firstRun || option.kind !== 'comic').map(option => ({kind: option.kind, label: t(option.label), icon: option.icon}))}/>
      </SectionCard>
      <SectionCard plain label={t('next.ownerLibraries.nameLabel')}>
        <TextField focusId="owner-libraries-add-name" value={form.name} onChangeText={form.typeName} maxLength={100} accessibilityLabel={t('next.ownerLibraries.nameLabel')}/>
      </SectionCard>
      <SectionCard label={t('next.ownerLibraries.folders')} help={form.folders.length ? undefined : t('next.ownerLibraries.addLibrary.foldersHelp')}>
        {form.choice.source && <SettingsRow first icon="i-disk" label={form.choice.source.name}/>}
        {form.folders.map((folder, index) => <SettingsRow key={folder.path} first={index === 0 && !form.choice.source} icon="i-folder" label={folder.name} description={folder.path}
          control={<Button variant="icon" size="xs" icon="i-x" accessibilityLabel={t('next.ownerLibraries.explorer.removeChosen')} onPress={() => form.removeFolder(folder.path)}/>}/>)}
        <SettingsRow navigates first={!form.folders.length && !form.choice.source} icon="i-plus" label={t('next.ownerLibraries.explorer.choose')} onPress={() => form.setExploring(true)}/>
      </SectionCard>
      {form.agents.length > 0 && <SectionCard label={t('next.ownerLibraries.metadataSource')}>
        {form.agents.map((option, index) => <RadioRow key={option.id} first={index === 0} label={option.name} description={option.description} checked={option.id === form.agent}
          onPress={() => form.chooseAgent(option)}/>)}
      </SectionCard>}
      {language !== undefined && chosenAgent && chosenAgent.languages.length > 0 && <SectionCard label={t('next.ownerLibraries.addLibrary.language')}>
        <SettingsRow first label={t('next.ownerLibraries.addLibrary.language')} description={t('next.ownerLibraries.addLibrary.languageHelp')}
          control={<Select testID="owner-libraries-add-language" accessibilityLabel={t('next.ownerLibraries.addLibrary.language')} value={language}
            options={chosenAgent.languages.map(code => ({value: code, label: languageName(code, key => defaultI18n.has(key) ? defaultI18n.t(key) : undefined)}))} onValueChange={form.setLanguage}/>}/>
      </SectionCard>}
      {form.formError !== undefined && <SectionCard plain><FormError placement="field">{presentError(form.formError, 'library', {operation: form.errorOperation}).body}</FormError></SectionCard>}
    </SettingsPageFrame>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={busy} onPress={onCancel}>{t(firstRun ? 'web.setup.skip' : 'action.cancel')}</Button>
      {/* The first-run label is a sentence: it wraps to a second line beside Skip rather than end in "…" (deferred row 247). */}
      <Button labelLines={2} loading={busy} disabled={!form.valid || busy} onPress={() => onCreate(form.input())}>{t(firstRun ? 'web.setup.addLibraryScan' : 'web.empty.addLibrary')}</Button>
    </OverlayFootRow></PanelBlock>
    <FolderSourcePicker open={form.exploring} onOpenChange={form.setExploring} api={api} serverName={serverName} ports={firstRun ? undefined : ports} runtime={firstRun ? undefined : runtime}
      chosen={form.choice} onUse={form.setChoice}/>
  </>;
}

/** The metadata source choice the library page's Metadata tab shows too. */
export function MetadataAgentChoice({option, checked, first, onPress}: {option: Readonly<{id: string; name: string; description: string}>; checked: boolean; first: boolean; onPress: () => void}) {
  return <RadioRow label={option.name} description={option.description} checked={checked} first={first} onPress={onPress}/>;
}

/** One folder's own form (frame 21a): path, name, where it lives, links and how often it is checked; Cancel and Save. */
export function AddFolderPage({api, serverName, source, expectedRevision, onSave, onCancel, busy, error, ports, runtime}: {
  api: LibraryContentApi; serverName: string; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime; source?: InventorySource; expectedRevision?: number;
  onSave: (input: SourceSettings) => void; onCancel: () => void; busy: boolean; error?: unknown;
}) {
  const form = useAddFolder(source, expectedRevision);
  return <>
    <SettingsPageFrame title={t(source ? 'web.libraries.editFolder' : 'web.libraries.addFolder')} testID="owner-libraries-folder" back={{label: t('action.cancel'), onPress: onCancel}}>
      <SectionCard label={t('next.ownerLibraries.addFolder.path')}>
        <SettingsRow navigates first icon="i-folder" label={form.path || t('server.chooseFolder')} description={form.path ? undefined : t('next.ownerLibraries.addFolder.help')} onPress={() => form.setExploring(true)}/>
      </SectionCard>
      <SectionCard plain label={t('next.ownerLibraries.addFolder.name')}>
        <TextField focusId="owner-libraries-folder-name" value={form.name} onChangeText={form.setName} maxLength={100} placeholder={t('next.ownerLibraries.addFolder.namePlaceholder')} accessibilityLabel={t('next.ownerLibraries.addFolder.name')}/>
      </SectionCard>
      <SectionCard>
        <SettingsRow first label={t('next.ownerLibraries.addFolder.location')}
          control={<Select testID="owner-libraries-folder-location" accessibilityLabel={t('next.ownerLibraries.addFolder.location')} value={form.classification} options={[
            {value: 'local', label: t('web.libraries.localDisk'), description: t('next.ownerLibraries.addFolder.localHint')},
            {value: 'network', label: t('web.libraries.networkShare'), description: t('next.ownerLibraries.addFolder.networkHint')}]} onValueChange={value => form.setClassification(value === 'network' ? 'network' : 'local')}/>}/>
        <SettingsRow label={t('next.ownerLibraries.addFolder.follow')} description={t('next.ownerLibraries.addFolder.followHelp')}
          control={<Switch testID="owner-libraries-folder-follow" checked={form.follow} accessibilityLabel={t('next.ownerLibraries.addFolder.follow')} onCheckedChange={form.setFollow}/>}/>
        <SettingsRow label={t('next.ownerLibraries.addFolder.rescan')}
          control={<Select testID="owner-libraries-folder-interval" accessibilityLabel={t('next.ownerLibraries.addFolder.rescan')} value={String(form.interval)}
            options={ownerLibraryIntervals.map(value => ({value: String(value.seconds), label: t(value.label, value.values)}))} onValueChange={value => form.setInterval(Number(value))}/>}/>
      </SectionCard>
      {error !== undefined && <SectionCard plain><FormError placement="field">{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</FormError></SectionCard>}
    </SettingsPageFrame>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={busy} onPress={onCancel}>{t('action.cancel')}</Button>
      <Button loading={busy} disabled={!form.valid || busy} onPress={() => onSave(form.input())}>{t('next.ownerLibraries.save')}</Button>
    </OverlayFootRow></PanelBlock>
    <FolderSourcePicker open={form.exploring} onOpenChange={form.setExploring} api={api} serverName={serverName} ports={ports} runtime={runtime} multiple={false} chosen={form.choice} onUse={form.use}/>
  </>;
}

/** The Scanning tab's missing titles: the library's rule for emptying its trash, how many are in it, and its sheets. */
export function LibraryTrashGroup({api, libraryId, parentCurrent, t: translate}: {api: ServerRequester; libraryId: string; parentCurrent: () => boolean; t: Translate}) {
  const trash = useLibraryTrash({api, libraryId, parentCurrent, t: translate}), {label} = trash, data = trash.trash;
  return <>
    <SectionCard label={translate('next.ownerLibraries.missingTitle')}>
      <SettingsRow first label={label('1.label')} description={label('1.help')} control={data && <Select testID="owner-libraries-trash-rule" accessibilityLabel={label('1.label')} value={data.rule.mode} disabled={trash.busy}
        options={TRASH_RULES.map(([value, key]) => ({value, label: translate(key)}))} onValueChange={mode => {if (mode === 'days' || mode === 'scan' || mode === 'manual') trash.choose({mode, days: data.rule.days});}}/>}/>
      {data?.rule.mode === 'days' && <SettingsRow label={translate('next.ownerLibraries.missingRuleDaysLabel')} control={<PageActions>
        <ShortNumberField focusId="owner-libraries-trash-days" value={trash.days} onChangeText={trash.setDays} editable={!trash.busy} accessibilityLabel={translate('next.ownerLibraries.missingRuleDaysLabel')}/>
        <Button size="xs" variant="glass" disabled={trash.busy || !trash.daysValid || trash.dayCount === data.rule.days} onPress={() => trash.choose({mode: 'days', days: trash.dayCount})}>{translate('next.settings.apply')}</Button>
      </PageActions>}/>}
      <SettingsRow navigates label={label('0.label')} description={label('0.help')} value={data ? defaultI18n.number(data.total) : undefined} onPress={trash.openList}/>
    </SectionCard>
    {trash.failed && <SectionCard plain><PageNote alert onPage>{translate('next.states.pageFailedBody')}</PageNote></SectionCard>}
    <BottomSheet open={trash.listOpen && !trash.confirming} onOpenChange={trash.setListOpen} title={translate('next.ownerLibraries.missingTitle')} description={translate('next.ownerLibraries.missingLede')} scrollable
      footer={<OverlayFootRow layout="halves">
        {data && data.total > 0 && <Button variant="danger" disabled={trash.busy} onPress={trash.startEmpty}>{translate('next.ownerLibraries.emptyTrash')}</Button>}
        <Button variant="glass" onPress={() => trash.setListOpen(false)}>{translate('action.close')}</Button></OverlayFootRow>}>
      <SheetBody>
        {trash.removed !== undefined && <PageNote alert>{translate('next.ownerLibraries.emptyTrashDone', {count: trash.removed})}</PageNote>}
        {!data && !trash.failed && <PageNote>{translate('next.settings.loading')}</PageNote>}
        {data && !data.items.length && <PageNote>{translate('next.ownerLibraries.missingNone', {library: data.name})}</PageNote>}
        {trash.items.map((item, index) => <TrashItemLine key={item.key} first={index === 0} title={item.title} lines={item.lines}/>)}
        {data && data.total > data.items.length && <PageNote>{translate('next.ownerLibraries.missingMore', {shown: data.items.length, count: data.total})}</PageNote>}
      </SheetBody>
    </BottomSheet>
    <BottomSheet open={trash.confirming && !!data} onOpenChange={value => {if (!value) trash.setConfirming(false);}} title={translate('next.ownerLibraries.emptyTrashTitle')}
      description={data ? translate('next.ownerLibraries.emptyTrashBody', {count: data.total, library: data.name}) : undefined}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" disabled={trash.busy} onPress={() => trash.setConfirming(false)}>{translate('action.cancel')}</Button>
        <Button variant="danger" loading={trash.busy} disabled={trash.busy || !data || trash.typed.trim() !== String(data.total)} onPress={trash.empty}>{translate('next.ownerLibraries.emptyTrash')}</Button></OverlayFootRow>}>
      <SheetBody><TextField focusId="owner-libraries-trash-confirm" value={trash.typed} onChangeText={trash.setTyped} keyboardType="number-pad" editable={!trash.busy}
        accessibilityLabel={data ? translate('next.ownerLibraries.emptyTrashType', {count: data.total}) : ''} placeholder={data ? translate('next.ownerLibraries.emptyTrashType', {count: data.total}) : ''}/></SheetBody>
    </BottomSheet>
    <BottomSheet open={!!trash.asking && !!data} onOpenChange={value => {if (!value) trash.setAsking(undefined);}} title={trash.asking ? translate('next.ownerLibraries.ruleRemovesTitle', {count: trash.asking.count}) : ''}
      description={trash.asking && data ? translate('next.ownerLibraries.ruleRemovesBody', {count: trash.asking.count, library: data.name}) : undefined}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" disabled={trash.busy} onPress={() => trash.setAsking(undefined)}>{translate('action.cancel')}</Button>
        <Button variant="danger" disabled={trash.busy} onPress={() => {if (trash.asking) trash.save(trash.asking.rule);}}>{translate('next.ownerLibraries.ruleRemovesConfirm')}</Button></OverlayFootRow>}>
      {null}
    </BottomSheet>
  </>;
}
