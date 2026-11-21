import React, {type ReactNode} from 'react';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {BackupSettings, RestoreTarget} from '../../../../../../packages/client-core/src/server-admin/server-backups';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {NativeSettingControl} from '../../../kit/settings/SettingControl';
import {BackupRow, PendingRestoreCard, StorageActions, StorageColumn, StorageList, StorageNote, StoragePath, StorageSheetBody, StorageUsage} from '../../../kit/settings/StorageParts';
import {SettingsRow} from '../../../kit/shell';
import {FolderExplorer} from '../../../kit/settings/FolderExplorer';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useBackups, useBackupSchedule, useBackupsGroup, useEmptyCache, useEmptyCacheGroup, usePassphrase, usePendingRestore, useRestore, useStorageKeep,
  useStorageSpace, type BackupsGroup, type Translate} from '../../shared/settings/useServerStorage';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

/** Storage & backups (owner-server 8 and 12, in the settings kit): where the server keeps its own files and what uses space,
 * Empty cache, then Backups (folder, schedule, Back up now, the list, Restore from file) and, under Advanced, how long
 * notifications are kept, the log settings, artwork in backups and the passphrase. Missing titles belongs to Libraries. */
export function ServerStoragePage({ports, runtime, retry, onOpenPage, ...frame}: Frame & {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime;
  retry?: ReactNode; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const t = runtime.text.t as Translate, context = ports.context;
  return <SettingsPageFrame {...frame} title={t('settings.server.storage')}>
    {retry}
    <StorageSpace context={context} runtime={runtime}/>
    <EmptyCache context={context} runtime={runtime}/>
    <ServerBackups ports={ports} runtime={runtime} t={t} advancedContent={<KeepRows context={context} t={t} onOpenPage={onOpenPage}/>}/>
  </SettingsPageFrame>;
}

function StorageSpace({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const t = runtime.text.t as Translate, space = useStorageSpace(context, runtime);
  if (!space) return null;
  if (space.failed) return <SectionCard plain><StorageNote alert onPage>{t('error.unavailable')}</StorageNote>
    <StorageActions onPage><Button size="compact" variant="glass" onPress={space.retry}>{t('action.tryAgain')}</Button></StorageActions></SectionCard>;
  if (!space.usage) return <SectionCard plain><StorageNote onPage>{t('next.settings.loading')}</StorageNote></SectionCard>;
  return <>
    <SectionCard plain>
      <StoragePath testID="server-storage-data-usage" label={t('next.settingsSpec.server.storage.groups.0.rows.0.label')} path={space.usage.stateDirectory}
        help={t('next.settingsSpec.server.storage.groups.0.rows.0.help')}/>
      {space.usage.onNetwork === true && <StoragePath tone="caution" label={t('next.settingsSpec.server.storage.groups.0.rows.1.label')}
        help={t('next.settingsSpec.server.storage.groups.0.rows.1.help')}/>}
    </SectionCard>
    <SectionCard plain label={t('next.settingsSpec.server.storage.groups.0.rows.2.label')}>
      <StorageUsage totalLabel={t('web.maintenance.totalLabel')} total={space.totalText} lede={t('web.maintenance.storageLede')} rows={space.rows}/>
    </SectionCard>
  </>;
}

function EmptyCache({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const group = useEmptyCacheGroup(context, runtime);
  return group ? <EmptyCacheButton group={group} t={runtime.text.t as Translate}/> : null;
}
function EmptyCacheButton({group, t}: {group: NonNullable<ReturnType<typeof useEmptyCacheGroup>>; t: Translate}) {
  const cache = useEmptyCache(group), {state} = cache, busy = state.phase === 'busy', label = t('next.settingsSpec.server.storage.groups.0.rows.3.control.label');
  return <SectionCard plain>
    <StorageActions onPage><Button testID="server-storage-empty-cache" size="compact" variant="glass" disabled={!cache.available} onPress={cache.open}>{label}</Button></StorageActions>
    {state.phase === 'scheduled' && <StorageNote onPage>{t('web.maintenance.restartingTitle')}</StorageNote>}
    {state.phase === 'uncertain' && <StorageNote alert onPage>{t('error.unconfirmedChange')}</StorageNote>}
    <BottomSheet open={cache.held} onOpenChange={value => {if (!value) cache.close();}} title={label} dismissOnBack={!busy} dismissOnGesture={!busy} dismissOnOutsidePress={!busy}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" disabled={busy} onPress={cache.close}>{t('action.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={state.phase !== 'ready'} onPress={() => {void cache.submit();}}>{label}</Button></OverlayFootRow>}>
      <StorageSheetBody><StorageNote>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.confirm')}</StorageNote>
        <StorageNote>{t('next.settingsSpec.server.general.groups.3.rows.1.control.confirm')}</StorageNote></StorageSheetBody>
    </BottomSheet>
  </SectionCard>;
}

/** Backups: the folder, the schedule, Back up now, a restore waiting for a restart, the list, Restore from file, and Advanced. */
export function ServerBackups({ports, runtime, t, advancedContent}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; t: Translate; advancedContent?: ReactNode}) {
  const group = useBackupsGroup(ports, runtime);
  return group ? <Backups group={group} t={t} advancedContent={advancedContent}/> : null;
}

function Backups({group, t, advancedContent}: {group: BackupsGroup; t: Translate; advancedContent?: ReactNode}) {
  const view = useBackups(group, t), {state, backups, busy, running, menu, deleting} = view;
  const [advancedOpen, setAdvancedOpen] = React.useState(false);
  const folderLabel = t('next.settingsSpec.server.storage.groups.1.rows.0.label');
  const artwork = t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label'), protectedLabel = t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label');
  return <>
    <SectionCard plain label={t('web.maintenance.backups')}>
      <StoragePath testID="server-storage-backups" label={folderLabel} path={backups?.folder ?? t('next.settings.loading')}
        warning={backups && !backups.folderWritable ? t('server.backups.folderUnwritable') : undefined}>
        <StorageActions><Button size="compact" variant="glass" disabled={!backups || state.busy} onPress={() => view.setPicking('folder')}>{t('next.settings.changeFolder')}</Button></StorageActions>
      </StoragePath>
    </SectionCard>
    {state.settings && <BackupSchedule key={`${state.settings.days.join(',')}:${state.settings.hour}:${state.settings.keep}`} settings={state.settings} saving={busy} t={t}
      onSave={change => view.act(() => view.source.saveSettings(change))}/>}
    <SectionCard plain>
      <StorageColumn>
        <StorageActions onPage><Button testID="server-backup-now" size="compact" loading={running} disabled={!backups || busy} onPress={() => view.act(() => view.source.backupNow())}>
          {t(running ? 'web.maintenance.backingUp' : 'web.maintenance.backUpNow')}</Button></StorageActions>
        {!running && backups?.lastError && <StorageNote alert onPage>{t('server.backups.lastFailed', {reason: backups.lastError})}</StorageNote>}
        {view.problem && <StorageNote alert onPage>{view.problem}</StorageNote>}
        <PendingRestore group={group} t={t}/>
        {backups && !state.list.length && <StorageNote onPage>{t('web.maintenance.noBackups')}</StorageNote>}
        {!!state.list.length && <StorageList>{state.list.map((backup, index) => <BackupRow key={backup.id} first={index === 0} testID={`server-backup-${backup.id}`}
          accessibilityLabel={`${view.when(backup)}, ${view.kind(backup)}`} disabled={busy} onPress={() => view.chooseMenu(backup)} title={view.when(backup)}
          facts={[view.kind(backup), view.size(backup), ...(backup.serverVersion ? [t('server.backups.madeBy', {version: backup.serverVersion})] : []),
            ...(backup.encrypted ? [t('server.backups.protected')] : [])]}/>)}</StorageList>}
        <StorageActions onPage><Button variant="glass" size="compact" disabled={!backups || busy} onPress={() => view.setPicking('restore')}>{t('web.maintenance.restoreFromFile')}</Button></StorageActions>
      </StorageColumn>
    </SectionCard>
    <NativeAdvancedSection testID="server-storage-advanced" open={advancedOpen} onOpenChange={setAdvancedOpen}
      summary={[state.settings?.includeArtwork ? artwork : '', backups?.protected ? protectedLabel : ''].filter(Boolean).join(' · ') || t('next.settings.allDefaults')}>
      {advancedContent && <SectionCard>{advancedContent}</SectionCard>}
      {(state.settings || backups) && <SectionCard>
        {state.settings && <SettingsRow first label={artwork} description={t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.help')}
          control={<Switch checked={state.settings.includeArtwork} disabled={state.busy} accessibilityLabel={artwork}
            onCheckedChange={includeArtwork => view.act(() => view.source.saveSettings({includeArtwork}))}/>}/>}
        {backups && <Passphrase group={group} t={t} first={!state.settings}/>}
      </SectionCard>}
    </NativeAdvancedSection>
    <Menu testID="server-backup-actions" open={!!menu} onOpenChange={value => {if (!value) view.setMenu(undefined);}} presentation="sheet" title={menu ? view.when(menu) : undefined} items={menu ? [
      {id: 'restore', label: t('web.maintenance.restoreBackup'), onSelect: () => view.restoreFromMenu(menu)},
      {id: 'delete', label: t('action.delete'), destructive: true, onSelect: () => view.deleteFromMenu(menu)},
    ] : []}/>
    <BottomSheet open={!!deleting} onOpenChange={value => {if (!value) view.setDeleting(undefined);}} title={t('web.maintenance.deleteBackupTitle')} description={t('web.maintenance.deleteBackupBody', {id: 'delete'})}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={() => view.setDeleting(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={state.busy} disabled={state.busy || view.typed.trim() !== 'delete'} onPress={view.confirmDelete}>{t('action.delete')}</Button></OverlayFootRow>}>
      <StorageSheetBody><TextField value={view.typed} onChangeText={view.setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('web.deleteAccount.typeLabel', {value: 'delete'})} placeholder={t('web.deleteAccount.typeLabel', {value: 'delete'})}/></StorageSheetBody>
    </BottomSheet>
    {view.picking && <FolderExplorer open testID={view.picking === 'restore' ? 'server-restore-picker' : 'server-backup-folder-picker'} api={group.api} multiple={false}
      title={t(view.picking === 'restore' ? 'web.maintenance.restoreFromFile' : 'next.settingsSpec.server.storage.groups.1.rows.0.label')} selectionMode={view.picking === 'restore' ? 'file-or-folder' : 'folders'}
      chosen={view.picking === 'folder' && backups?.folder ? [{path: backups.folder, name: backups.folder}] : []} onOpenChange={value => {if (!value) view.setPicking(undefined);}}
      onUse={view.use}/>}
    <RestoreSheet group={group} t={t} target={view.restoring?.target} when={view.restoring?.when} locked={view.restoring?.protected} onClose={() => view.setRestoring(undefined)}/>
  </>;
}

export function BackupSchedule({settings, saving, t, onSave}: {settings: BackupSettings; saving: boolean; t: Translate; onSave: (change: Pick<BackupSettings, 'days' | 'hour' | 'keep'>) => void}) {
  const schedule = useBackupSchedule(settings, saving, onSave);
  return <>
    <SectionCard>{schedule.rows.map((row, index) => <NativeSettingControl key={row.id} row={row.row} value={row.value} disabled={saving} t={t} first={index === 0} insetField
      onCustom={row.onCustom} onChange={row.onChange} onValidity={row.onValidity}/>)}</SectionCard>
    {schedule.dirty && <SectionCard plain><StorageActions onPage><Button variant="glass" disabled={saving} onPress={schedule.discard}>{t('action.discard')}</Button>
      <Button loading={saving} disabled={!schedule.valid} onPress={schedule.save}>{t('action.save')}</Button></StorageActions></SectionCard>}
  </>;
}

function Passphrase({group, t, first}: {group: BackupsGroup; t: Translate; first: boolean}) {
  const view = usePassphrase(group, t), label = t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label');
  return <>
    <SettingsRow first={first} label={label} description={t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.help')} control={<StorageActions>
      {view.on && <Button size="compact" variant="glass" disabled={view.disabled} accessibilityLabel={t('next.settings.backup.changeTitle')} onPress={view.change}>{t('next.settings.changeFolder')}</Button>}
      <Switch testID="server-backup-passphrase" checked={view.on} disabled={view.disabled} accessibilityLabel={label} onCheckedChange={view.toggle}/>
    </StorageActions>}/>
    <BottomSheet open={!!view.editing} onOpenChange={value => {if (!value) view.close();}} title={t(view.editing === 'change' ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')} description={t('server.backups.passphraseFuture')}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.close}>{t('next.settings.cancel')}</Button>
        <Button loading={view.busy} disabled={!view.ready} onPress={view.save}>{t(view.editing === 'change' ? 'server.backups.savePassphrase' : 'next.settings.backup.protect')}</Button></OverlayFootRow>}>
      <StorageSheetBody>
        <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={view.phrase} editable={!view.busy} autoCapitalize="none" autoCorrect={false} onChangeText={view.setPhrase} error={view.tooLong ? t('server.backups.passphraseTooLong') : undefined}/>
        <TextField label={t('next.settings.backup.repeat')} secureTextEntry value={view.repeat} editable={!view.busy} autoCapitalize="none" autoCorrect={false} onChangeText={view.setRepeat} error={view.mismatch ? t('next.settings.backup.mismatch') : undefined}/>
        {view.refused && <StorageNote alert>{view.refused}</StorageNote>}
      </StorageSheetBody>
    </BottomSheet>
    <BottomSheet open={view.stopping} onOpenChange={value => {if (!value) view.close();}} title={t('server.backups.passphraseOffTitle')} description={t('server.backups.passphraseOffBody')}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.close}>{t('next.settings.cancel')}</Button>
        <Button variant="danger" loading={view.busy} disabled={view.busy} onPress={view.turnOff}>{t('server.backups.passphraseOff')}</Button></OverlayFootRow>}>
      {view.refused ? <StorageSheetBody><StorageNote alert>{view.refused}</StorageNote></StorageSheetBody> : null}
    </BottomSheet>
  </>;
}

function PendingRestore({group, t}: {group: BackupsGroup; t: Translate}) {
  const view = usePendingRestore(group, t), {state, pending, asks} = view;
  if (state.restarting) return <StorageNote alert onPage>{t('server.backups.restarting')}</StorageNote>;
  if (!pending) return null;
  return <PendingRestoreCard testID="server-backup-pending" title={t('server.backups.pending', {name: pending.backup})}
    note={asks && !asks.canRestart ? t('server.backups.restartByHand') : undefined} error={view.error}
    actions={<>
      {asks?.canRestart && <Button size="compact" disabled={state.busy} onPress={view.restartNow}>{t('server.backups.restartNow')}</Button>}
      <Button size="compact" variant="glass" disabled={state.busy} onPress={view.cancel}>{t('server.backups.cancelRestore')}</Button>
      <BottomSheet open={view.confirm && !!asks} onOpenChange={value => {if (!value) view.setConfirm(false);}} title={t('server.backups.restartTitle')} description={t('server.backups.restartBody', {count: asks?.playing ?? 0})}
        footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={() => view.setConfirm(false)}>{t('next.settings.cancel')}</Button>
          <Button disabled={state.busy} onPress={view.confirmRestart}>{t('server.backups.restartNow')}</Button></OverlayFootRow>}>
        {null}
      </BottomSheet>
    </>}/>;
}

function RestoreSheet({group, t, target, when: madeAt, locked, onClose}: {group: BackupsGroup; t: Translate; target?: RestoreTarget; when?: string; locked?: boolean; onClose: () => void}) {
  const view = useRestore({group, t, target, locked, onClose}), {inspection} = view;
  const field = {autoCapitalize: 'none' as const, autoCorrect: false, editable: !view.busy};
  return <BottomSheet open={!!target} onOpenChange={value => {if (!value) view.close();}} scrollable
    title={t(target && 'path' in target ? 'web.maintenance.restoreFileTitle' : 'web.maintenance.restoreTitle')} description={madeAt ? t('server.backups.restoreWhich', {when: madeAt}) : undefined}
    footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.close}>{t('next.settings.cancel')}</Button>
      <Button loading={view.busy} disabled={!view.ready} onPress={view.restore}>{t('web.maintenance.restoreConfirm')}</Button></OverlayFootRow>}>
    <StorageSheetBody>
      {!inspection && !view.refused && !view.locked && <StorageNote>{t('server.backups.checking')}</StorageNote>}
      {view.locked && !inspection && <>
        <StorageNote>{`${t('next.settings.backup.restoreTitle')}. ${t('server.backups.passphraseNeeded')}`}</StorageNote>
        <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={view.phrase} onChangeText={view.setPhrase} onSubmitEditing={view.open} {...field}/>
        <StorageActions><Button size="compact" loading={view.busy} disabled={view.busy || !view.phrase} onPress={view.open}>{t('server.backups.openBackup')}</Button></StorageActions>
      </>}
      {inspection?.restoring.map(line => <StorageNote key={line}>{`• ${t(`server.backups.restoring.${line}` as MessageId)}`}</StorageNote>)}
      {view.elsewhere && <StorageNote alert>{view.warning || t('server.backups.notMadeHere')}</StorageNote>}
      {inspection && <>
        <TextField label={t('web.deleteAccount.typeLabel', {value: view.word})} value={view.typed} onChangeText={view.setTyped} {...field}/>
        {view.locked && <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={view.phrase} onChangeText={view.setPhrase} {...field}/>}
        <TextField label={t('server.backups.password')} secureTextEntry textContentType="password" value={view.password} onChangeText={view.setPassword} {...field}/>
        <TextField label={t('server.backups.code')} keyboardType="number-pad" textContentType="oneTimeCode" value={view.code} onChangeText={view.setCode} {...field}/>
      </>}
      {view.refused && <StorageNote alert>{view.refused}</StorageNote>}
    </StorageSheetBody>
  </BottomSheet>;
}

export function KeepRows({context, t, onOpenPage}: {context: NativeShellBootstrapContext; t: Translate; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const keep = useStorageKeep(context, t);
  return <>
    {keep.notices && <SettingsRow first label={keep.notification.label} description={keep.notification.help}
      control={<Select testID="storage.keepNotifications" accessibilityLabel={keep.notification.label} value={String(keep.notices.days)} disabled={keep.busy}
        options={keep.notification.options} onValueChange={keep.setDays}/>}/>}
    <SettingsRow navigates label={keep.logsLabel} onPress={() => onOpenPage('troubleshooting')}/>
    {keep.failed && <StorageNote alert>{t('error.unavailable')}</StorageNote>}
  </>;
}
