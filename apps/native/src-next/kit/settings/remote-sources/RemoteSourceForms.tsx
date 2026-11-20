import React from 'react';
import {View} from 'react-native';
import type {defaultI18n, MessageId} from '../../../../../../packages/i18n/src';
import {remotePresets, type RemoteLimits, type RemotePreset, type RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import {Button} from '../../controls/Button';
import {TextField} from '../../controls/TextField';
import {useTheme} from '../../theme';
import type {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import {FormError} from '../../controls';

import {PanelBlock, SectionCard, SettingsPageFrame} from '../../frames';
import {SettingsHelp} from '../SettingsParts';
import {stateWords} from '../../../screens/settings/remote-sources/messages';
import {useAddRemoteSource, useRemoteFolderStep, useRemoteSourceEditor} from '../../../pages/shared/settings/useRemoteSourceForms';

type Translate = typeof defaultI18n.t;

const presetLabel = (preset: RemotePreset): MessageId => (`next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.${remotePresets.findIndex(one => one.id === preset)}.label`) as MessageId;

export function RemoteSourceForm({title, t, busy, onCancel, children, footer, wizardStep}: {
  title: string; t: Translate; busy: boolean; onCancel: () => void; children: React.ReactNode; footer: React.ReactNode; wizardStep?: number;
}) {
  const theme = useTheme();
  return <SettingsPageFrame title={title} footer={footer} back={{label: t('next.remoteSources.cancel'), onPress: onCancel, disabled: busy}}>
    {wizardStep !== undefined && <SectionCard><PanelBlock>
      <View style={{flexDirection: 'row', gap: theme.space.hairline * 4}}>
        {[1, 2, 3, 4, 5].map(value => <View key={value} style={{flex: 1, height: theme.space.hairline * 3,
          borderRadius: theme.space.hairline * 2, backgroundColor: value <= wizardStep ? theme.colors.accentStrong : theme.colors.lineSoft}}/>)}
      </View>
      <SettingsHelp>{t('next.remoteSources.stepOf', {step: wizardStep})}</SettingsHelp>
    </PanelBlock></SectionCard>}
    <SectionCard><PanelBlock>{children}</PanelBlock></SectionCard>
  </SettingsPageFrame>;
}

export function Problem({message}: {message?: string}) {
  return message ? <FormError placement="inline">{message}</FormError> : null;
}

function Choice({label, hint, picked, onPress}: {label: string; hint?: string; picked: boolean; onPress: () => void}) {
  return <Button variant={picked ? 'primary' : 'glass'} onPress={onPress} accessibilityState={{selected: picked}}>{hint ? `${label} · ${hint}` : label}</Button>;
}
/** The preset and its limits (rules 11, 12): a preset starts its limits, which stay editable. */
export function ReadingFields({t, preset, limits, busy, onPreset, onLimits}: {t: Translate; preset: RemotePreset; limits: RemoteLimits; busy: boolean; onPreset: (preset: RemotePreset) => void; onLimits: (limits: RemoteLimits) => void}) {
  const number = (value: string, fallback: number) => {const parsed = Number.parseInt(value, 10); return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;};
  return <PanelBlock inset={false}>
    {remotePresets.map(option => <Choice key={option.id} label={t(presetLabel(option.id))} picked={preset === option.id} onPress={() => {if (!busy) {onPreset(option.id); onLimits(option.limits);}}}/>)}
    <TextField label={t('next.remoteSources.connections')} value={String(limits.connections)} keyboardType="number-pad" editable={!busy} onChangeText={value => onLimits({...limits, connections: number(value, limits.connections)})}/>
    <TextField label={t('next.remoteSources.perSecond')} value={String(limits.perSecond)} keyboardType="number-pad" editable={!busy} onChangeText={value => onLimits({...limits, perSecond: number(value, limits.perSecond)})}/>
  </PanelBlock>;
}

const kinds = [
  {id: 'webdav', label: 'next.remoteSources.connectWebdav', hint: 'next.remoteSources.kindCommon'},
  {id: 'rclone', label: 'next.remoteSources.connectRclone', hint: 'next.remoteSources.connectRcloneHint'},
  {id: 'cloud', label: 'next.remoteSources.connectCloud', hint: 'next.remoteSources.connectCloudHint'},
  {id: 'newRclone', label: 'next.remoteSources.remoteKind', hint: 'next.remoteSources.kindIncluded'},
  {id: 'drive', label: 'next.remoteSources.connectDrive', hint: 'next.remoteSources.connectDriveHint'},
] as const;

/** The wizard (rules 8 to 12): what to connect, its details tested and saved together, how Portico reads it, then folders. */
export function AddSourceFlow({controller, t, isCurrent, onCancel, onSaved}: {controller: NativeRemoteSourcesController; t: Translate; isCurrent: () => boolean;
  onCancel: () => void; onSaved: (source: RemoteSource) => void}) {
  const add = useAddRemoteSource({controller, t, isCurrent, onSaved});
  const {busy, error, step, setStep, kind, setKind, f, set, preset, setPreset, limits, setLimits, testAndSave, saveReading, signIn} = add;
  // A pasted rclone configuration is several lines: its box keeps them.
  const field = (label: MessageId, key: keyof typeof f, secure = false, multiline = false) => <TextField label={t(label)} value={String(f[key])} secureTextEntry={secure} editable={!busy}
    multiline={multiline} autoCapitalize="none" autoCorrect={false} onChangeText={value => set({[key]: value} as Partial<typeof f>)}/>;
  const title = t(step === 1 ? 'next.remoteSources.whatToConnect' : step === 2 ? 'next.remoteSources.remote' : 'next.remoteSources.howTreated');
  return <RemoteSourceForm wizardStep={step} title={title} t={t} busy={busy} onCancel={onCancel} footer={step === 1 ? <Button variant="primary" align="stretch" onPress={() => setStep(2)}>{t('next.remoteSources.continue')}</Button>
      : step === 2 ? <Button variant="primary" align="stretch" loading={busy} disabled={!f.name.trim()} onPress={testAndSave}>{t('next.remoteSources.test')}</Button>
        : <Button variant="primary" align="stretch" loading={busy} onPress={saveReading}>{t('next.remoteSources.saveAndContinue')}</Button>}>
      <Problem message={error}/>
      {step === 1 && kinds.map(option => <Choice key={option.id} label={t(option.label)} hint={t(option.hint)} picked={kind === option.id} onPress={() => setKind(option.id)}/>)}
      {step === 2 && <>
        {field('next.remoteSources.name', 'name')}
        {kind === 'webdav' && <>{field('next.remoteSources.address', 'address')}{field('next.remoteSources.username', 'user')}{field('next.remoteSources.password', 'password', true)}</>}
        {kind === 'rclone' && <>{field('next.remoteSources.connectRclone', 'config', false, true)}{field('next.remoteSources.cloudFolder', 'path')}</>}
        {kind === 'cloud' && <>{(['onedrive', 'dropbox'] as const).map(provider => <Choice key={provider} label={t(provider === 'onedrive' ? 'next.remoteSources.cloudOneDrive' : 'next.remoteSources.cloudDropbox')}
          picked={f.provider === provider} onPress={() => set({provider})}/>)}{field('next.remoteSources.cloudToken', 'token')}{field('next.remoteSources.cloudFolder', 'path')}</>}
        {kind === 'newRclone' && <>{field('next.remoteSources.remoteKind', 'remoteType')}{field('next.remoteSources.connectRcloneHint', 'options', false, true)}{field('next.remoteSources.cloudFolder', 'path')}</>}
        {kind === 'drive' && <>
          <Choice label={t('next.remoteSources.driveServiceAccount')} picked={f.driveMode === 'key'} onPress={() => set({driveMode: 'key'})}/>
          <Choice label={t('next.remoteSources.driveOAuth')} picked={f.driveMode === 'oauth'} onPress={() => set({driveMode: 'oauth'})}/>
          {f.driveMode === 'key' ? field('next.remoteSources.driveKey', 'key') : <>
            {field('next.remoteSources.driveClientId', 'clientId')}{field('next.remoteSources.driveClientSecret', 'clientSecret', true)}
            <Button variant="glass" disabled={busy || !f.clientId.trim() || !f.clientSecret} onPress={signIn}>{t('next.remoteSources.driveOAuthSignIn')}</Button>
            {!!f.signIn && field('next.remoteSources.driveOAuthPasteAction', 'redirectedUrl')}</>}
          {field('next.remoteSources.cloudFolder', 'path')}</>}
      </>}
      {step === 3 && <ReadingFields t={t} preset={preset} limits={limits} busy={busy} onPreset={setPreset} onLimits={setLimits}/>}
  </RemoteSourceForm>;
}

/** Continue setup (rule 10): open a source's folders, choose one, and put it in a library, new or existing. */
export function FolderStep({controller, t, source, isCurrent, onDone, onCancel}: {controller: NativeRemoteSourcesController; t: Translate; source: RemoteSource;
  isCurrent: () => boolean; onDone: () => void; onCancel: () => void}) {
  const {busy, error, path, setPath, folders, libraries, target, setTarget, name, setName, kind, setKind, save} =
    useRemoteFolderStep({controller, t, source, isCurrent, onDone});
  const kinds = ['movies', 'shows', 'anime', 'music', 'audiobooks', 'books', 'comics'] as const;
  return <RemoteSourceForm title={t('next.remoteSources.foldersHeader', {name: source.name})} t={t} busy={busy} onCancel={onCancel} wizardStep={4}
    footer={<Button variant="primary" align="stretch" loading={busy} disabled={!target && !name.trim()} onPress={save}>{t('next.remoteSources.saveAndContinue')}</Button>}>
    <Problem message={error}/>
    <SettingsHelp>{t('next.remoteSources.folderPath')} /{path}</SettingsHelp>
    {!!path && <Button variant="text" size="small" onPress={() => setPath(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')}>{t('next.remoteSources.back')}</Button>}
    {folders === undefined ? <SettingsHelp>{t('next.remoteSources.folderListing')}</SettingsHelp> : folders.map(folder => <Button key={folder.path} variant="glass" onPress={() => setPath(folder.path)}>{folder.name}</Button>)}
    {libraries.map(library => <Choice key={library.id} label={library.name} picked={target === library.id} onPress={() => setTarget(library.id)}/>)}
    <Choice label={t('next.remoteSources.newLibrary')} picked={target === ''} onPress={() => setTarget('')}/>
    {!target && <><TextField label={t('next.remoteSources.libraryName')} value={name} onChangeText={setName} editable={!busy}/>
      {kinds.map((value, index) => <Choice key={value} label={t(`next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.${index}.label` as MessageId)} picked={kind === value} onPress={() => setKind(value)}/>)}</>}
  </RemoteSourceForm>;
}

/** A saved source: test, rename, preset and limits, replace its password or configuration (tested again), continue setup, disconnect. */
export function ManageSource({controller, t, source, isCurrent, onClose, onContinue}: {controller: NativeRemoteSourcesController; t: Translate; source: RemoteSource;
  isCurrent: () => boolean; onClose: () => void; onContinue: (source: RemoteSource) => void}) {
  const {busy, error, shown, name, setName, secret, setSecret, confirm, setConfirm, preset, setPreset, limits, setLimits, save, test, disconnect} =
    useRemoteSourceEditor({controller, t, source, isCurrent, onClose});
  return <RemoteSourceForm title={shown.name} t={t} busy={busy} onCancel={onClose} footer={<Button variant="primary" align="stretch" loading={busy} disabled={!name.trim()} onPress={save}>{t('next.remoteSources.save')}</Button>}>
    <SettingsHelp>{t(stateWords[shown.state] ?? 'next.remoteSources.state.problem')}{shown.problem ? ` · ${shown.problem}` : ''}</SettingsHelp>
    <Button variant="glass" disabled={busy} onPress={test}>{t('next.remoteSources.test')}</Button>
    {shown.state === 'setup' && <Button variant="primary" onPress={() => onContinue(shown)}>{t('next.remoteSources.continueSetup')}</Button>}
    <TextField label={t('next.remoteSources.name')} value={name} onChangeText={setName} editable={!busy}/>
    <ReadingFields t={t} preset={preset} limits={limits} busy={busy} onPreset={setPreset} onLimits={setLimits}/>
    <TextField label={t(source.kind === 'webdav' ? 'next.remoteSources.password' : source.kind === 'rclone' ? 'next.remoteSources.connectRclone' : 'next.remoteSources.driveKey')}
      value={secret} secureTextEntry={source.kind === 'webdav'} multiline={source.kind === 'rclone'} onChangeText={setSecret} editable={!busy} autoCapitalize="none" autoCorrect={false}/>
    {confirm === undefined ? <Button variant="text" onPress={() => setConfirm('')}>{t('next.remoteSources.disconnect')}</Button> : <>
      <SettingsHelp>{t('next.remoteSources.disconnectBodyNext')}</SettingsHelp>
      <TextField label={t('next.remoteSources.disconnectConfirm')} value={confirm} onChangeText={setConfirm}/>
      <Button variant="glass" disabled={busy || confirm !== shown.name} onPress={disconnect}>{t('next.remoteSources.disconnect')}</Button></>}
    <Problem message={error}/>
  </RemoteSourceForm>;
}
