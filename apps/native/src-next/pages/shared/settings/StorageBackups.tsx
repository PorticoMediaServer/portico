import React from 'react';
import type {BackupSettings, RestoreTarget} from '../../../../../../packages/client-core/src/server-admin/server-backups';
import type {BackupSummary} from '../../../../../../packages/client-core/src/administration';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button, FormError, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {NativeSettingControl} from '../../../kit/settings/SettingControl';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {StorageLocation} from '../../../kit/settings/StorageParts';
import {SettingsRow} from '../../../kit/shell';
import {FolderExplorer} from '../../../screens/settings-live/owner-libraries/FolderExplorer';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useBackupSchedule} from './useBackupSchedule';
import {type BackupGroup, useBackupGroup, useBackupPassphrase, useBackupRestore, usePendingRestore, useStorageBackups} from './useServerBackups';

type Translate = typeof defaultI18n.t;
type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; advancedContent: React.ReactNode};

export function StorageBackups({ports, runtime, advancedContent}: Props) {
  const group = useBackupGroup({ports, runtime});
  return group ? <Backups group={group} t={runtime.text.t} advancedContent={advancedContent}/>
    : <SectionCard label={runtime.text.t('web.maintenance.backups')}><StateRow kind="loading"/></SectionCard>;
}

function backupKind(backup: BackupSummary, t: Translate) {
  return t(backup.kind === 'scheduled' ? 'web.maintenance.backupKindScheduled'
    : backup.kind === 'pre-restore' ? 'web.maintenance.backupKindPreRestore' : 'web.maintenance.backupKindManual');
}

function Backups({group, t, advancedContent}: {group: BackupGroup; t: Translate; advancedContent: React.ReactNode}) {
  const model = useStorageBackups({group, t}), {state, backups, running, busy, problem, menu, deleting, typed, picking, restoring} = model;
  const folder = t('next.settingsSpec.server.storage.groups.1.rows.0.label');
  return <>
    <SectionCard label={t('web.maintenance.backups')}>
      <SettingsRow first label={folder} control={<Button size="compact" variant="glass" disabled={!backups || state.busy}
        onPress={() => model.setPicking('folder')}>{t('next.settings.changeFolder')}</Button>}/>
      <StorageLocation path={backups?.folder ?? t('next.settings.loading')}/>
      {backups && !backups.folderWritable && <PanelBlock><FormError placement="inline">{t('server.backups.folderUnwritable')}</FormError></PanelBlock>}
    </SectionCard>
    {state.settings && <NativeBackupSchedule key={state.settings.days.join(',') + ':' + state.settings.hour + ':' + state.settings.keep}
      settings={state.settings} saving={busy} t={t} onSave={model.saveSettings}/>}
    <PanelBlock><OverlayFootRow layout="halves">
      <Button testID="server-backup-now" size="compact" loading={running} disabled={!backups || busy}
        onPress={model.backupNow}>{t(running ? 'web.maintenance.backingUp' : 'web.maintenance.backUpNow')}</Button>
      <Button variant="glass" size="compact" disabled={!backups || busy}
        onPress={() => model.setPicking('restore')}>{t('web.maintenance.restoreFromFile')}</Button>
    </OverlayFootRow></PanelBlock>
    {!running && backups?.lastError && <PanelBlock><FormError placement="inline">
      {t('server.backups.lastFailed', {reason: backups.lastError})}
    </FormError></PanelBlock>}
    {problem && <PanelBlock>
      <FormError placement="inline">{problem}</FormError>
      {!backups && <Button variant="glass" size="compact" onPress={model.load}>{t('action.tryAgain')}</Button>}
    </PanelBlock>}
    <PendingRestore group={group} t={t}/>
    {backups && <SectionCard>
      {!state.list.length && <StateRow kind="empty" icon="i-folder" title={t('web.maintenance.noBackups')}/>}
      {state.list.map((backup, index) => {
        const date = when(Date.parse(backup.createdAt)), kind = backupKind(backup, t);
        return <SettingsRow key={backup.id} first={index === 0} testID={`server-backup-${backup.id}`} label={date}
          accessibilityLabel={date + ', ' + kind} onPress={busy ? undefined : () => model.choose(backup)}
          description={[kind, defaultI18n.bytes(backup.bytes),
            backup.serverVersion ? t('server.backups.madeBy', {version: backup.serverVersion}) : '',
            backup.encrypted ? t('server.backups.protected') : '',
          ].filter(Boolean)}/>;
      })}
    </SectionCard>}
    <Menu testID="server-backup-actions" open={!!menu} onOpenChange={open => {if (!open) model.closeMenu();}} presentation="sheet"
      title={menu ? when(Date.parse(menu.createdAt)) : undefined} items={menu ? [
        {id: 'restore', label: t('web.maintenance.restoreBackup'), onSelect: model.openRestore},
        {id: 'delete', label: t('action.delete'), destructive: true, onSelect: model.openDelete},
      ] : []}/>
    <BottomSheet open={!!deleting} onOpenChange={open => {if (!open) model.cancelDelete();}}
      title={t('web.maintenance.deleteBackupTitle')} description={t('web.maintenance.deleteBackupBody', {id: 'delete'})}
      fitContent keyboardAvoiding footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={model.cancelDelete}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={state.busy} disabled={state.busy || typed.trim() !== 'delete'} onPress={model.remove}>{t('action.delete')}</Button>
      </OverlayFootRow>}>
      <PanelBlock><TextField value={typed} onChangeText={model.setTyped} autoCapitalize="none" autoCorrect={false}
        accessibilityLabel={t('web.deleteAccount.typeLabel', {value: 'delete'})} placeholder={t('web.deleteAccount.typeLabel', {value: 'delete'})}/></PanelBlock>
    </BottomSheet>
    {picking && <FolderExplorer open testID={picking === 'restore' ? 'server-restore-picker' : 'server-backup-folder-picker'} api={group.api} multiple={false}
      title={picking === 'restore' ? t('web.maintenance.restoreFromFile') : folder} selectionMode={picking === 'restore' ? 'file-or-folder' : 'folders'}
      chosen={picking === 'folder' && backups?.folder ? [{path: backups.folder, name: backups.folder}] : []}
      onOpenChange={open => {if (!open) model.setPicking(undefined);}} onUse={paths => model.usePath(paths[0]?.path)}/>}
    <RestoreSheet group={group} t={t} target={restoring?.target} when={restoring?.when} locked={restoring?.protected} onClose={model.closeRestore}/>
    <NativeAdvancedSection testID="server-storage-advanced"
      summary={[state.settings?.includeArtwork ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label') : '',
        backups?.protected ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label') : '',
      ].filter(Boolean).join(' · ') || t('next.settings.allDefaults')}>
      {advancedContent}
      {state.settings && <SectionCard>
        <SettingsRow first label={t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}
          description={t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.help')}
          control={<Switch checked={state.settings.includeArtwork} disabled={state.busy}
            accessibilityLabel={t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}
            onCheckedChange={includeArtwork => model.saveSettings({includeArtwork})}/>}/>
      </SectionCard>}
      {backups && <Passphrase group={group} t={t}/>}
    </NativeAdvancedSection>
  </>;
}

export function NativeBackupSchedule({settings, saving, t, onSave}: {
  settings: BackupSettings; saving: boolean; t: Translate; onSave: (change: Pick<BackupSettings, 'days' | 'hour' | 'keep'>) => void;
}) {
  const {mode, read, change, dirty, valid, setValid, values, discard} = useBackupSchedule(settings, saving);
  const row = (id: string) => <NativeSettingControl key={id} row={settingsRowIndex.get(id)!.row}
    value={id === 'storage.backupSchedule' ? mode : read(id)} disabled={saving} t={t}
    onCustom={id === 'storage.backupSchedule' ? () => change(id, 'custom') : undefined} onChange={next => change(id, next)}
    onValidity={id === 'storage.backupKeep' ? setValid : undefined}/>;
  return <SectionCard>
    <PanelBlock>{row('storage.backupKeep')}</PanelBlock>
    {row('storage.backupSchedule')}
    {mode !== 'never' && row('storage.backupTime')}
    {mode === 'custom' && row('storage.backupDays')}
    {dirty && <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={saving} onPress={discard}>{t('action.discard')}</Button>
      <Button loading={saving} disabled={!valid} onPress={() => {if (!saving && valid) onSave(values);}}>{t('action.save')}</Button>
    </OverlayFootRow></PanelBlock>}
  </SectionCard>;
}

function Passphrase({group, t}: {group: BackupGroup; t: Translate}) {
  const model = useBackupPassphrase({group, t});
  const {on, disabled, editing, stopping, phrase, repeat, tooLong, mismatch, ready, busy, refused} = model;
  const label = t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label');
  return <>
    <SectionCard>
      <SettingsRow first label={label} description={t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.help')}
        control={<Switch testID="server-backup-passphrase" checked={on} disabled={disabled} accessibilityLabel={label}
          onCheckedChange={next => {if (next) model.setEditing('set'); else model.setStopping(true);}}/>}/>
      {on && <PanelBlock><Button size="compact" variant="glass" disabled={disabled}
        accessibilityLabel={t('next.settings.backup.changeTitle')} onPress={() => model.setEditing('change')}>
        {t('next.settings.changeFolder')}</Button></PanelBlock>}
    </SectionCard>
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open) model.close();}}
      title={t(editing === 'change' ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')}
      description={t('server.backups.passphraseFuture')} keyboardAvoiding
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={model.close}>{t('next.settings.cancel')}</Button>
        <Button loading={busy} disabled={!ready} onPress={model.save}>
          {t(editing === 'change' ? 'server.backups.savePassphrase' : 'next.settings.backup.protect')}
        </Button>
      </OverlayFootRow>}>
      <PanelBlock><TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={phrase} editable={!busy}
        autoCapitalize="none" autoCorrect={false} onChangeText={model.setPhrase} error={tooLong ? t('server.backups.passphraseTooLong') : undefined}/></PanelBlock>
      <PanelBlock><TextField label={t('next.settings.backup.repeat')} secureTextEntry value={repeat} editable={!busy}
        autoCapitalize="none" autoCorrect={false} onChangeText={model.setRepeat} error={mismatch ? t('next.settings.backup.mismatch') : undefined}/></PanelBlock>
      {refused && <PanelBlock><FormError placement="inline">{refused}</FormError></PanelBlock>}
    </BottomSheet>
    <BottomSheet open={stopping} onOpenChange={open => {if (!open) model.close();}} title={t('server.backups.passphraseOffTitle')}
      description={t('server.backups.passphraseOffBody')} fitContent
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={model.close}>{t('next.settings.cancel')}</Button>
        <Button variant="danger" loading={busy} disabled={busy} onPress={model.turnOff}>{t('server.backups.passphraseOff')}</Button>
      </OverlayFootRow>}>
      {refused && <PanelBlock><FormError placement="inline">{refused}</FormError></PanelBlock>}
    </BottomSheet>
  </>;
}

function PendingRestore({group, t}: {group: BackupGroup; t: Translate}) {
  const model = usePendingRestore({group, t}), {state, pending, asks, confirm, setConfirm, error} = model;
  if (state.restarting) return <SettingsHelp>{t('server.backups.restarting')}</SettingsHelp>;
  if (!pending) return null;
  return <SectionCard label={t('server.backups.pending', {name: pending.backup})}
    help={asks && !asks.canRestart ? t('server.backups.restartByHand') : undefined}>
    <PanelBlock><OverlayFootRow layout="halves">
      {asks?.canRestart && <Button size="compact" disabled={state.busy} onPress={model.askRestart}>{t('server.backups.restartNow')}</Button>}
      <Button size="compact" variant="glass" disabled={state.busy} onPress={model.cancelRestore}>{t('server.backups.cancelRestore')}</Button>
    </OverlayFootRow></PanelBlock>
    {error && <PanelBlock><FormError placement="inline">{error}</FormError></PanelBlock>}
    <BottomSheet open={confirm && !!asks} onOpenChange={open => {if (!open) setConfirm(false);}}
      title={t('server.backups.restartTitle')} description={t('server.backups.restartBody', {count: asks?.playing ?? 0})} fitContent
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={() => setConfirm(false)}>{t('next.settings.cancel')}</Button>
        <Button disabled={state.busy} onPress={model.restart}>{t('server.backups.restartNow')}</Button>
      </OverlayFootRow>}>{null}</BottomSheet>
  </SectionCard>;
}

function RestoreSheet({group, t, target, when: madeAt, locked, onClose}: {
  group: BackupGroup; t: Translate; target?: RestoreTarget; when?: string; locked?: boolean; onClose: () => void;
}) {
  const model = useBackupRestore({group, t, target, locked, onClose});
  const {inspection, refused, warning, password, code, typed, phrase, busy, word, elsewhere, ready} = model;
  const field = {autoCapitalize: 'none' as const, autoCorrect: false, editable: !busy};
  const phraseLabel = t('next.settings.backup.passphrase');
  return <BottomSheet open={!!target} onOpenChange={open => {if (!open) model.close();}} scrollable keyboardAvoiding defaultDetent={1}
    title={t(target && 'path' in target ? 'web.maintenance.restoreFileTitle' : 'web.maintenance.restoreTitle')}
    description={madeAt ? t('server.backups.restoreWhich', {when: madeAt}) : undefined}
    footer={<OverlayFootRow layout="halves">
      <Button variant="glass" onPress={model.close}>{t('next.settings.cancel')}</Button>
      <Button loading={busy} disabled={!ready} onPress={model.restore}>{t('web.maintenance.restoreConfirm')}</Button>
    </OverlayFootRow>}>
    {!inspection && !refused && !model.locked && <SettingsHelp>{t('server.backups.checking')}</SettingsHelp>}
    {model.locked && !inspection && <>
      <SettingsHelp>{t('next.settings.backup.restoreTitle')}. {t('server.backups.passphraseNeeded')}</SettingsHelp>
      <PanelBlock><TextField label={phraseLabel} secureTextEntry value={phrase} onChangeText={model.setPhrase} onSubmitEditing={model.open} {...field}/></PanelBlock>
      <PanelBlock><Button size="compact" loading={busy} disabled={busy || !phrase} onPress={model.open}>{t('server.backups.openBackup')}</Button></PanelBlock>
    </>}
    {inspection?.restoring.map(line => <SettingsHelp key={line}>• {t(`server.backups.restoring.${line}` as MessageId)}</SettingsHelp>)}
    {elsewhere && <PanelBlock><FormError placement="inline">{warning || t('server.backups.notMadeHere')}</FormError></PanelBlock>}
    {inspection && <>
      <PanelBlock><TextField label={t('web.deleteAccount.typeLabel', {value: word})} value={typed} onChangeText={model.setTyped} {...field}/></PanelBlock>
      {model.locked && <PanelBlock><TextField label={phraseLabel} secureTextEntry value={phrase} onChangeText={model.setPhrase} {...field}/></PanelBlock>}
      <PanelBlock><TextField label={t('server.backups.password')} secureTextEntry textContentType="password" value={password} onChangeText={model.setPassword} {...field}/></PanelBlock>
      <PanelBlock><TextField label={t('server.backups.code')} keyboardType="number-pad" textContentType="oneTimeCode"
        value={code} onChangeText={model.setCode} {...field}/></PanelBlock>
    </>}
    {refused && <PanelBlock><FormError placement="inline">{refused}</FormError></PanelBlock>}
  </BottomSheet>;
}
