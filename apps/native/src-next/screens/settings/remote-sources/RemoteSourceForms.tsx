import React, {createContext, useContext, useEffect, useState} from 'react';
import {Linking, Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {defaultRemotePreset, isNotOnServerYet, remotePresets, remoteSourceCreate, type RemoteFolder, type RemoteLimits, type RemotePreset, type RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import type {AdminLibrary} from '../../../../../../packages/contracts/src/next/api';
import {Button} from '../../../kit/controls/Button';
import {TextField} from '../../../kit/controls/TextField';
import {useTheme} from '../../../kit/theme';
import {StandardBackdrop} from '../../../kit/artwork';
import {FormScrollView} from '../../../platform/keyboard';
import type {NativeRemoteSourcesController} from './controller';

type Translate = typeof defaultI18n.t;
/** A folder chosen on a source, for a library. */
export type ChosenSourceFolder = Pick<RemoteFolder, 'name' | 'path'>;

/** The server's refusal in the owner's words; a step this server does not offer yet says so plainly. */
export function remoteSourceError(t: Translate, error: unknown): string {
  if (isNotOnServerYet(error)) return t('next.remoteSources.notOnServerYet');
  const code = error && typeof error === 'object' && 'code' in error && typeof error.code === 'string' ? error.code : '';
  const message = (error as {message?: unknown} | null)?.message;
  if ((code === 'invalid_field' || code === 'invalid_request' || code === 'remote_unreachable') && typeof message === 'string' && message && message.length < 300) return message;
  const keys = {unauthorized: 'next.remoteSources.error.signIn', forbidden: 'next.remoteSources.error.forbidden', source_in_use: 'next.remoteSources.error.changed'} as const;
  return t(Object.prototype.hasOwnProperty.call(keys, code) ? keys[code as keyof typeof keys] : 'next.remoteSources.error.generic');
}
export const SourceWizardContext = createContext<Readonly<{step: number; back: () => void}> | undefined>(undefined);
export const stateWords: Record<string, MessageId> = {
  connected: 'next.remoteSources.state.ready', setup: 'next.remoteSources.state.setup', unreachable: 'next.remoteSources.state.unreachable',
  sign_in: 'next.remoteSources.state.credentials', waiting: 'next.remoteSources.state.waiting', limited: 'next.remoteSources.state.limited',
  needs_attention: 'next.remoteSources.state.attention',
};
const presetLabel = (preset: RemotePreset): MessageId => (`next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.${remotePresets.findIndex(one => one.id === preset)}.label`) as MessageId;

export function Page({title, t, busy, onCancel, children, footer, wizardStep}: {title: string; t: Translate; busy: boolean; onCancel: () => void; children: React.ReactNode; footer: React.ReactNode; wizardStep?: number}) {
  const theme = useTheme(), wizard = useContext(SourceWizardContext), step = wizardStep ?? wizard?.step;
  return <View style={{flex: 1}}><StandardBackdrop/>
    <View style={{padding: 20, flexDirection: 'row', alignItems: 'center', gap: 16}}>
      <Button variant="text" size="small" disabled={busy} onPress={onCancel}>{t('next.remoteSources.cancel')}</Button>
      <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text, flex: 1}]}>{title}</Text>
    </View>
    {step !== undefined && <View style={{paddingHorizontal: 20, gap: 8}}>
      <View style={{flexDirection: 'row', gap: 4}}>{[1, 2, 3, 4, 5].map(value => <View key={value} style={{flex: 1, height: 3, borderRadius: 2, backgroundColor: value <= step ? theme.colors.accentStrong : theme.colors.lineSoft}}/>)}</View><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.stepOf', {step})}</Text></View>}
    <FormScrollView keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive" style={{flex: 1}} contentContainerStyle={{padding: 20, gap: 16}}>{children}</FormScrollView>
    <View style={{padding: 20, borderTopWidth: 1, borderTopColor: theme.colors.line, gap: 12}}>{footer}</View>
  </View>;
}
export function Problem({message}: {message?: string}) {
  const theme = useTheme();
  return message ? <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{message}</Text> : null;
}
function Choice({label, hint, picked, onPress}: {label: string; hint?: string; picked: boolean; onPress: () => void}) {
  return <Button variant={picked ? 'primary' : 'glass'} onPress={onPress} accessibilityState={{selected: picked}}>{hint ? `${label} · ${hint}` : label}</Button>;
}
/** Runs one action, showing its refusal; a second tap while it runs does nothing. */
function useRun(t: Translate, isCurrent: () => boolean) {
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const run = (work: () => Promise<void>) => {
    if (busy) return;
    setBusy(true); setError(undefined);
    void work().catch(failure => {if (isCurrent()) setError(remoteSourceError(t, failure));}).finally(() => {if (isCurrent()) setBusy(false);});
  };
  return {busy, error, run};
}

/** The preset and its limits (rules 11, 12): a preset starts its limits, which stay editable. */
export function ReadingFields({t, preset, limits, busy, onPreset, onLimits}: {t: Translate; preset: RemotePreset; limits: RemoteLimits; busy: boolean; onPreset: (preset: RemotePreset) => void; onLimits: (limits: RemoteLimits) => void}) {
  const number = (value: string, fallback: number) => {const parsed = Number.parseInt(value, 10); return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;};
  return <View style={{gap: 12}}>
    {remotePresets.map(option => <Choice key={option.id} label={t(presetLabel(option.id))} picked={preset === option.id} onPress={() => {if (!busy) {onPreset(option.id); onLimits(option.limits);}}}/>)}
    <TextField label={t('next.remoteSources.connections')} value={String(limits.connections)} keyboardType="number-pad" editable={!busy} onChangeText={value => onLimits({...limits, connections: number(value, limits.connections)})}/>
    <TextField label={t('next.remoteSources.perSecond')} value={String(limits.perSecond)} keyboardType="number-pad" editable={!busy} onChangeText={value => onLimits({...limits, perSecond: number(value, limits.perSecond)})}/>
  </View>;
}

type Kind = 'webdav' | 'rclone' | 'cloud' | 'newRclone' | 'drive';
const kinds: readonly Readonly<{id: Kind; label: MessageId; hint: MessageId}>[] = [
  {id: 'webdav', label: 'next.remoteSources.connectWebdav', hint: 'next.remoteSources.kindCommon'},
  {id: 'rclone', label: 'next.remoteSources.connectRclone', hint: 'next.remoteSources.connectRcloneHint'},
  {id: 'cloud', label: 'next.remoteSources.connectCloud', hint: 'next.remoteSources.connectCloudHint'},
  {id: 'newRclone', label: 'next.remoteSources.remoteKind', hint: 'next.remoteSources.kindIncluded'},
  {id: 'drive', label: 'next.remoteSources.connectDrive', hint: 'next.remoteSources.connectDriveHint'},
];

/** The wizard (rules 8 to 12): what to connect, its details tested and saved together, how Portico reads it, then folders. */
export function AddSourceFlow({controller, t, isCurrent, onCancel, onSaved}: {controller: NativeRemoteSourcesController; t: Translate; isCurrent: () => boolean;
  onCancel: () => void; onSaved: (source: RemoteSource) => void}) {
  const {busy, error, run} = useRun(t, isCurrent);
  const [step, setStep] = useState<1 | 2 | 3>(1), [kind, setKind] = useState<Kind>('webdav'), [created, setCreated] = useState<RemoteSource>();
  const [f, setF] = useState({name: '', address: '', user: '', password: '', config: '', path: '', token: '', provider: 'onedrive' as 'onedrive' | 'dropbox', remoteType: '', options: '',
    driveMode: 'key' as 'key' | 'oauth', key: '', clientId: '', clientSecret: '', signIn: '', redirectedUrl: ''});
  const [preset, setPreset] = useState<RemotePreset>(defaultRemotePreset), [limits, setLimits] = useState<RemoteLimits>(remotePresets[1]!.limits);
  const set = (patch: Partial<typeof f>) => setF(value => ({...value, ...patch}));
  const field = (label: MessageId, key: keyof typeof f, secure = false) => <TextField label={t(label)} value={String(f[key])} secureTextEntry={secure} editable={!busy}
    autoCapitalize="none" autoCorrect={false} onChangeText={value => set({[key]: value} as Partial<typeof f>)}/>;
  const testAndSave = () => run(async () => {
    let source: RemoteSource;
    if (kind === 'cloud') source = await controller.addCloudToken({name: f.name.trim(), provider: f.provider, token: f.token, path: f.path, preset});
    else if (kind === 'newRclone') source = await controller.addRcloneFromFields({name: f.name.trim(), type: f.remoteType.trim(), path: f.path, preset,
      options: Object.fromEntries(f.options.split('\n').map(line => line.split('=')).filter(parts => parts.length >= 2).map(([key, ...rest]) => [key!.trim(), rest.join('=').trim()]))});
    else {
      const body = remoteSourceCreate(kind === 'webdav' ? {kind, name: f.name, address: f.address, user: f.user, password: f.password}
        : kind === 'rclone' ? {kind, name: f.name, config: f.config, path: f.path}
          : f.driveMode === 'key' ? {kind: 'drive', name: f.name, path: f.path, serviceAccountKey: f.key} : {kind: 'drive', name: f.name, path: f.path, signIn: f.signIn, redirectedUrl: f.redirectedUrl}, preset);
      if ('missing' in body) throw Object.assign(new Error(''), {code: 'invalid_request'});
      source = await controller.add(body);
    }
    if (isCurrent()) {setCreated(source); setStep(3);}
  });
  const saveReading = () => run(async () => {
    if (!created) return;
    const source = await controller.change(created.id, {preset, limits});
    if (isCurrent()) onSaved(source);
  });
  const signIn = () => run(async () => {
    const started = await controller.startDriveSignIn(f.clientId, f.clientSecret);
    if (!isCurrent()) return;
    set({signIn: started.signIn});
    await Linking.openURL(started.address);
  });
  const title = t(step === 1 ? 'next.remoteSources.whatToConnect' : step === 2 ? 'next.remoteSources.remote' : 'next.remoteSources.howTreated');
  return <SourceWizardContext.Provider value={{step, back: () => setStep(value => (value > 1 ? value - 1 : value) as 1 | 2 | 3)}}>
    <Page title={title} t={t} busy={busy} onCancel={onCancel} footer={step === 1 ? <Button variant="primary" onPress={() => setStep(2)}>{t('next.remoteSources.continue')}</Button>
      : step === 2 ? <Button variant="primary" loading={busy} disabled={!f.name.trim()} onPress={testAndSave}>{t('next.remoteSources.test')}</Button>
        : <Button variant="primary" loading={busy} onPress={saveReading}>{t('next.remoteSources.saveAndContinue')}</Button>}>
      <Problem message={error}/>
      {step === 1 && kinds.map(option => <Choice key={option.id} label={t(option.label)} hint={t(option.hint)} picked={kind === option.id} onPress={() => setKind(option.id)}/>)}
      {step === 2 && <>
        {field('next.remoteSources.name', 'name')}
        {kind === 'webdav' && <>{field('next.remoteSources.address', 'address')}{field('next.remoteSources.username', 'user')}{field('next.remoteSources.password', 'password', true)}</>}
        {kind === 'rclone' && <>{field('next.remoteSources.connectRclone', 'config')}{field('next.remoteSources.cloudFolder', 'path')}</>}
        {kind === 'cloud' && <>{(['onedrive', 'dropbox'] as const).map(provider => <Choice key={provider} label={t(provider === 'onedrive' ? 'next.remoteSources.cloudOneDrive' : 'next.remoteSources.cloudDropbox')}
          picked={f.provider === provider} onPress={() => set({provider})}/>)}{field('next.remoteSources.cloudToken', 'token')}{field('next.remoteSources.cloudFolder', 'path')}</>}
        {kind === 'newRclone' && <>{field('next.remoteSources.remoteKind', 'remoteType')}{field('next.remoteSources.connectRcloneHint', 'options')}{field('next.remoteSources.cloudFolder', 'path')}</>}
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
    </Page>
  </SourceWizardContext.Provider>;
}

/** Continue setup (rule 10): open a source's folders, choose one, and put it in a library, new or existing. */
export function FolderStep({controller, t, source, isCurrent, onDone, onCancel}: {controller: NativeRemoteSourcesController; t: Translate; source: RemoteSource;
  isCurrent: () => boolean; onDone: () => void; onCancel: () => void}) {
  const {busy, error, run} = useRun(t, isCurrent);
  const [path, setPath] = useState(''), [folders, setFolders] = useState<readonly RemoteFolder[]>(), [libraries, setLibraries] = useState<readonly AdminLibrary[]>([]);
  const [target, setTarget] = useState(''), [name, setName] = useState(source.name), [kind, setKind] = useState('movies');
  useEffect(() => {setFolders(undefined); void controller.folders(source.id, path).then(list => {if (isCurrent()) setFolders(list);}, () => {if (isCurrent()) setFolders([]);});}, [controller, source.id, path, isCurrent]);
  useEffect(() => {void controller.libraries().then(list => {if (isCurrent()) setLibraries(list);}, () => {});}, [controller, isCurrent]);
  const kinds = ['movies', 'shows', 'anime', 'music', 'audiobooks', 'books', 'comics'] as const;
  return <Page title={t('next.remoteSources.foldersHeader', {name: source.name})} t={t} busy={busy} onCancel={onCancel} wizardStep={4}
    footer={<Button variant="primary" loading={busy} disabled={!target && !name.trim()} onPress={() => run(async () => {
      await controller.putFolderInLibrary(source, path, target ? {id: target} : {name: name.trim(), kind});
      if (isCurrent()) onDone();
    })}>{t('next.remoteSources.saveAndContinue')}</Button>}>
    <Problem message={error}/>
    <Text>{t('next.remoteSources.folderPath')} /{path}</Text>
    {!!path && <Button variant="text" size="small" onPress={() => setPath(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')}>{t('next.remoteSources.back')}</Button>}
    {folders === undefined ? <Text>{t('next.remoteSources.folderListing')}</Text> : folders.map(folder => <Button key={folder.path} variant="glass" onPress={() => setPath(folder.path)}>{folder.name}</Button>)}
    {libraries.map(library => <Choice key={library.id} label={library.name} picked={target === library.id} onPress={() => setTarget(library.id)}/>)}
    <Choice label={t('next.remoteSources.newLibrary')} picked={target === ''} onPress={() => setTarget('')}/>
    {!target && <><TextField label={t('next.remoteSources.libraryName')} value={name} onChangeText={setName} editable={!busy}/>
      {kinds.map((value, index) => <Choice key={value} label={t(`next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.${index}.label` as MessageId)} picked={kind === value} onPress={() => setKind(value)}/>)}</>}
  </Page>;
}

/** A saved source: test, rename, preset and limits, replace its password or configuration (tested again), continue setup, disconnect. */
export function ManageSource({controller, t, source, isCurrent, onClose, onContinue}: {controller: NativeRemoteSourcesController; t: Translate; source: RemoteSource;
  isCurrent: () => boolean; onClose: () => void; onContinue: (source: RemoteSource) => void}) {
  const {busy, error, run} = useRun(t, isCurrent);
  const [shown, setShown] = useState(source), [name, setName] = useState(source.name), [secret, setSecret] = useState(''), [confirm, setConfirm] = useState<string>();
  const [preset, setPreset] = useState<RemotePreset>(remotePresets.find(one => one.id === source.preset)?.id ?? defaultRemotePreset), [limits, setLimits] = useState(source.limits);
  const save = () => run(async () => {
    const replaced = !secret ? {} : source.kind === 'webdav' ? {webdav: {address: shown.webdav!.address, ...(shown.webdav?.user ? {user: shown.webdav.user} : {}), password: secret}}
      : source.kind === 'rclone' ? {rclone: {config: secret}} : {drive: {serviceAccountKey: secret}};
    const next = await controller.change(source.id, {name: name.trim(), preset, limits, ...replaced});
    if (isCurrent()) {setShown(next); setSecret('');}
  });
  return <Page title={shown.name} t={t} busy={busy} onCancel={onClose} footer={<Button variant="primary" loading={busy} disabled={!name.trim()} onPress={save}>{t('next.remoteSources.save')}</Button>}>
    <Text>{t(stateWords[shown.state] ?? 'next.remoteSources.state.problem')}{shown.problem ? ` · ${shown.problem}` : ''}</Text>
    <Problem message={error}/>
    <Button variant="glass" disabled={busy} onPress={() => run(async () => {const tested = await controller.test(source.id); if (isCurrent()) setShown(tested);})}>{t('next.remoteSources.test')}</Button>
    {shown.state === 'setup' && <Button variant="primary" onPress={() => onContinue(shown)}>{t('next.remoteSources.continueSetup')}</Button>}
    <TextField label={t('next.remoteSources.name')} value={name} onChangeText={setName} editable={!busy}/>
    <ReadingFields t={t} preset={preset} limits={limits} busy={busy} onPreset={setPreset} onLimits={setLimits}/>
    <TextField label={t(source.kind === 'webdav' ? 'next.remoteSources.password' : source.kind === 'rclone' ? 'next.remoteSources.connectRclone' : 'next.remoteSources.driveKey')}
      value={secret} secureTextEntry={source.kind === 'webdav'} onChangeText={setSecret} editable={!busy} autoCapitalize="none" autoCorrect={false}/>
    {confirm === undefined ? <Button variant="text" onPress={() => setConfirm('')}>{t('next.remoteSources.disconnect')}</Button> : <>
      <Text>{t('next.remoteSources.disconnectBody')}</Text>
      <TextField label={t('next.remoteSources.disconnectConfirm')} value={confirm} onChangeText={setConfirm}/>
      <Button variant="glass" disabled={busy || confirm !== shown.name} onPress={() => run(async () => {await controller.remove(source.id); if (isCurrent()) onClose();})}>{t('next.remoteSources.disconnect')}</Button></>}
  </Page>;
}
